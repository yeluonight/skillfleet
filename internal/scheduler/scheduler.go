// Package scheduler runs the background update-check poller (§2.9 t7).
//
// The server has one Scheduler. On Run it sweeps every bound source on a
// fixed cadence (config: scheduler.update_check_interval) and asks the §8.4
// engine whether each source's upstream skill subtree changed. The engine
// (internal/source.Checker) owns the actual decision — including THE CORE
// GUARD that a moved commit with unchanged content is NOT an update — so the
// scheduler is deliberately thin: it decides WHEN and HOW MANY to check, not
// WHAT counts as an update.
//
// Three properties this package is responsible for (the t7 acceptance bar):
//
//   - Graceful cancellation: Run returns promptly when its context is
//     cancelled, mid-sweep included. No check outlives the server.
//   - Bounded concurrency: at most cfg.Concurrency sources are checked at
//     once, so a fleet of bound sources can't open a hundred simultaneous
//     clones and trip upstream rate limits.
//   - Failure isolation: one source's check failing (or panicking) never
//     aborts the sweep or kills the poller — the next source, and the next
//     sweep, still run. The engine already records a failed check's cursor;
//     the scheduler just logs and moves on.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yeluonight/skillfleet/internal/source"
)

// sourceLister is the read surface the poller needs: enumerate every bound
// source to sweep. *source.Store satisfies it.
type sourceLister interface {
	ListAll(ctx context.Context) ([]source.Source, error)
}

// sourceChecker runs one §8.4 update check. *source.Checker satisfies it.
// Defined here (consumer side) so tests inject a fake without a real fetch.
type sourceChecker interface {
	Check(ctx context.Context, sourceID string, now time.Time) (source.CheckResult, error)
}

// Scheduler periodically sweeps all bound sources and runs an update check
// on each. It owns no persistent state; collaborators carry it.
type Scheduler struct {
	sources sourceLister
	checker sourceChecker
	now     func() time.Time
	log     *slog.Logger

	interval    time.Duration
	concurrency int
}

// New wires a Scheduler. interval is the sweep cadence; a non-positive
// interval is the caller's "disabled" signal and Run becomes a no-op (the
// caller normally checks Enabled before starting a goroutine, but Run guards
// it too so a misconfiguration can't busy-loop). concurrency is clamped to
// at least 1 so an enabled poller always makes progress. now and log default
// to safe values when nil so the zero-dependency path never panics.
func New(sources sourceLister, checker sourceChecker, interval time.Duration, concurrency int, now func() time.Time, log *slog.Logger) *Scheduler {
	if concurrency < 1 {
		concurrency = 1
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		sources:     sources,
		checker:     checker,
		now:         now,
		log:         log,
		interval:    interval,
		concurrency: concurrency,
	}
}

// Enabled reports whether the poller will actually run. A non-positive
// interval means "disabled" (manual check-updates only).
func (s *Scheduler) Enabled() bool { return s.interval > 0 }

// Run blocks until ctx is cancelled, sweeping all bound sources every
// interval. It runs one sweep immediately on start (so a freshly booted
// server reflects upstream state without waiting a full interval), then ticks.
//
// Run returns nil on graceful cancellation. A disabled poller (non-positive
// interval) returns immediately without error — callers may start it
// unconditionally.
func (s *Scheduler) Run(ctx context.Context) error {
	if !s.Enabled() {
		s.log.Info("update-check poller disabled", slog.String("reason", "scheduler.update_check_interval <= 0"))
		return nil
	}
	s.log.Info("update-check poller starting",
		slog.Duration("interval", s.interval),
		slog.Int("concurrency", s.concurrency),
	)

	// Immediate first sweep, but honour an already-cancelled context.
	if ctx.Err() != nil {
		return nil
	}
	s.sweep(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("update-check poller stopping", slog.String("reason", ctx.Err().Error()))
			return nil
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep runs one full pass over all bound sources, checking up to
// s.concurrency at a time. It never returns an error: listing failures are
// logged (the next tick retries) and per-source failures are isolated so the
// sweep always completes for the sources it can reach. sweep returns when
// every source in the batch has been checked or ctx is cancelled.
func (s *Scheduler) sweep(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	srcs, err := s.sources.ListAll(ctx)
	if err != nil {
		s.log.Error("update-check sweep: list sources failed", slog.String("err", err.Error()))
		return
	}
	if len(srcs) == 0 {
		s.log.Debug("update-check sweep: no bound sources")
		return
	}

	start := s.now()
	s.log.Info("update-check sweep starting", slog.Int("sources", len(srcs)))

	// Bounded worker pool: a buffered channel of size concurrency is the
	// semaphore. Acquiring is select-guarded on ctx so a cancellation during
	// a saturated sweep unblocks immediately rather than waiting for a slot.
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, s.concurrency)
		mu       sync.Mutex
		counts   = map[source.UpstreamState]int{}
		canceled bool
	)

	for _, src := range srcs {
		select {
		case <-ctx.Done():
			canceled = true
		case sem <- struct{}{}:
		}
		if canceled {
			break
		}

		wg.Add(1)
		go func(src source.Source) {
			defer wg.Done()
			defer func() { <-sem }()
			state := s.checkOne(ctx, src)
			mu.Lock()
			counts[state]++
			mu.Unlock()
		}(src)
	}

	wg.Wait()

	attrs := []any{
		slog.Int("sources", len(srcs)),
		slog.Duration("took", s.now().Sub(start)),
		slog.Int("up_to_date", counts[source.StateUpToDate]),
		slog.Int("update_available", counts[source.StateUpdateAvailable]),
		slog.Int("no_skill_change", counts[source.StateRemoteChangedNoSkillChange]),
		slog.Int("failed", counts[source.StateCheckFailed]),
	}
	if canceled {
		s.log.Info("update-check sweep canceled mid-flight", attrs...)
		return
	}
	s.log.Info("update-check sweep complete", attrs...)
}

// checkOne runs a single source's update check with panic isolation. A panic
// in the engine (or anything it calls) is recovered and reported as a failed
// check so one pathological source can never take down the poller goroutine.
// It returns the resulting UpstreamState for sweep-level tallying; a
// recovered panic or error maps to StateCheckFailed.
func (s *Scheduler) checkOne(ctx context.Context, src source.Source) (state source.UpstreamState) {
	state = source.StateCheckFailed
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("update-check panic recovered",
				slog.String("source_id", src.ID),
				slog.String("name", src.Name),
				slog.String("panic", fmt.Sprint(r)),
			)
			state = source.StateCheckFailed
		}
	}()

	res, err := s.checker.Check(ctx, src.ID, s.now())
	if err != nil {
		// check_failed is the expected, isolated outcome for network/ref
		// problems; the engine has already advanced last_checked_at without
		// moving the commit cursor, so the next sweep retries cleanly. Log at
		// warn (operationally interesting) without aborting the sweep.
		s.log.Warn("update-check failed",
			slog.String("source_id", src.ID),
			slog.String("name", src.Name),
			slog.String("state", string(res.State)),
			slog.String("err", err.Error()),
		)
		if res.State != "" {
			return res.State
		}
		return source.StateCheckFailed
	}

	switch res.State {
	case source.StateUpdateAvailable:
		s.log.Info("update available",
			slog.String("source_id", src.ID),
			slog.String("name", src.Name),
			slog.String("remote_commit", res.RemoteCommit),
			slog.String("pending_version_id", res.PendingVersionID),
		)
	case source.StateRemoteChangedNoSkillChange:
		// THE CORE GUARD outcome surfaced by the poller: the repo moved but
		// the skill didn't. Log at debug so it's observable without noise.
		s.log.Debug("remote moved but skill unchanged",
			slog.String("source_id", src.ID),
			slog.String("name", src.Name),
			slog.String("remote_commit", res.RemoteCommit),
		)
	default:
		s.log.Debug("update check done",
			slog.String("source_id", src.ID),
			slog.String("name", src.Name),
			slog.String("state", string(res.State)),
		)
	}
	return res.State
}
