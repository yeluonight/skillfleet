package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/source"
)

// quietLog returns a logger that discards everything, so tests don't spew.
func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeLister returns a canned slice of sources (and optionally an error) for
// every ListAll call.
type fakeLister struct {
	srcs []source.Source
	err  error

	mu    sync.Mutex
	calls int
}

func (f *fakeLister) ListAll(_ context.Context) ([]source.Source, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.srcs, nil
}

func (f *fakeLister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeChecker records every Check call and can be programmed to block, fail,
// or panic. It tracks concurrent in-flight checks so a test can assert the
// poller never exceeds its configured concurrency.
type fakeChecker struct {
	// behaviour knobs, set before Run:
	delay     time.Duration               // how long each Check blocks
	failIDs   map[string]bool             // these source IDs return an error
	panicIDs  map[string]bool             // these source IDs panic
	stateByID map[string]source.UpstreamState // override returned state

	// observed, read after Run:
	mu          sync.Mutex
	checkedIDs  []string
	inFlight    int32
	maxInFlight int32
}

func (f *fakeChecker) Check(ctx context.Context, sourceID string, _ time.Time) (source.CheckResult, error) {
	cur := atomic.AddInt32(&f.inFlight, 1)
	for {
		max := atomic.LoadInt32(&f.maxInFlight)
		if cur <= max || atomic.CompareAndSwapInt32(&f.maxInFlight, max, cur) {
			break
		}
	}
	defer atomic.AddInt32(&f.inFlight, -1)

	f.mu.Lock()
	f.checkedIDs = append(f.checkedIDs, sourceID)
	f.mu.Unlock()

	if f.panicIDs[sourceID] {
		panic("boom from " + sourceID)
	}
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return source.CheckResult{SourceID: sourceID, State: source.StateCheckFailed}, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.failIDs[sourceID] {
		return source.CheckResult{SourceID: sourceID, State: source.StateCheckFailed}, errors.New("network down")
	}
	state := source.StateUpToDate
	if s, ok := f.stateByID[sourceID]; ok {
		state = s
	}
	return source.CheckResult{SourceID: sourceID, State: state, RemoteCommit: "c"}, nil
}

func (f *fakeChecker) checked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.checkedIDs))
	copy(out, f.checkedIDs)
	return out
}

func (f *fakeChecker) peakConcurrency() int {
	return int(atomic.LoadInt32(&f.maxInFlight))
}

func mkSources(n int) []source.Source {
	out := make([]source.Source, n)
	for i := range out {
		id := "src_" + string(rune('a'+i))
		out[i] = source.Source{ID: id, Name: id, Type: source.TypeGitHubRepo}
	}
	return out
}

// TestRun_ImmediateSweepThenCancel proves the poller checks every source once
// on startup (no waiting a full interval) and returns promptly on cancel.
func TestRun_ImmediateSweepThenCancel(t *testing.T) {
	lister := &fakeLister{srcs: mkSources(3)}
	checker := &fakeChecker{}
	// Long interval: if the immediate sweep didn't happen, the test would
	// time out waiting rather than see 3 checks.
	s := New(lister, checker, time.Hour, 2, time.Now, quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	// Wait for the immediate sweep to check all 3 sources.
	waitFor(t, func() bool { return len(checker.checked()) == 3 }, time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}
	if got := len(checker.checked()); got != 3 {
		t.Errorf("checked %d sources on immediate sweep, want 3", got)
	}
}

// TestRun_DisabledIsNoOp proves a non-positive interval makes Run return
// immediately without sweeping.
func TestRun_DisabledIsNoOp(t *testing.T) {
	lister := &fakeLister{srcs: mkSources(2)}
	checker := &fakeChecker{}
	s := New(lister, checker, 0, 2, time.Now, quietLog())
	if s.Enabled() {
		t.Fatal("Enabled() = true for zero interval")
	}
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run disabled: %v", err)
	}
	if checker.peakConcurrency() != 0 || len(checker.checked()) != 0 {
		t.Errorf("disabled poller checked sources: %v", checker.checked())
	}
	if lister.callCount() != 0 {
		t.Errorf("disabled poller listed sources %d times, want 0", lister.callCount())
	}
}

// TestSweep_RespectsConcurrency proves no more than cfg.Concurrency checks run
// at once, even with many sources queued. Each Check blocks long enough that
// the pool would visibly exceed the cap if the semaphore were broken.
func TestSweep_RespectsConcurrency(t *testing.T) {
	const n, limit = 10, 3
	lister := &fakeLister{srcs: mkSources(n)}
	checker := &fakeChecker{delay: 20 * time.Millisecond}
	s := New(lister, checker, time.Hour, limit, time.Now, quietLog())

	s.sweep(context.Background())

	if got := len(checker.checked()); got != n {
		t.Errorf("checked %d, want all %d", got, n)
	}
	if peak := checker.peakConcurrency(); peak > limit {
		t.Errorf("peak concurrency %d exceeded limit %d", peak, limit)
	}
	if peak := checker.peakConcurrency(); peak < 2 {
		t.Errorf("peak concurrency %d — pool never parallelized; concurrency not exercised", peak)
	}
}

// TestSweep_FailureIsolation proves one source failing and another panicking
// do not stop the sweep: every other source is still checked.
func TestSweep_FailureIsolation(t *testing.T) {
	srcs := mkSources(5) // a b c d e
	lister := &fakeLister{srcs: srcs}
	checker := &fakeChecker{
		failIDs:  map[string]bool{"src_b": true},
		panicIDs: map[string]bool{"src_d": true},
	}
	s := New(lister, checker, time.Hour, 4, time.Now, quietLog())

	// Must not panic out of sweep despite src_d panicking.
	s.sweep(context.Background())

	if got := len(checker.checked()); got != 5 {
		t.Errorf("checked %d sources, want all 5 (failure/panic must not abort sweep): %v", got, checker.checked())
	}
}

// TestSweep_ListErrorIsLoggedNotFatal proves a ListAll failure doesn't panic
// or check anything; the next tick simply retries.
func TestSweep_ListErrorIsLoggedNotFatal(t *testing.T) {
	lister := &fakeLister{err: errors.New("db gone")}
	checker := &fakeChecker{}
	s := New(lister, checker, time.Hour, 2, time.Now, quietLog())

	s.sweep(context.Background()) // must not panic
	if len(checker.checked()) != 0 {
		t.Errorf("checked sources despite list error: %v", checker.checked())
	}
}

// TestSweep_CancelMidFlight proves a context cancelled during a saturated
// sweep stops queuing new checks promptly. With concurrency 1 and a delay,
// cancelling after the first check starts means not all sources get checked.
func TestSweep_CancelMidFlight(t *testing.T) {
	lister := &fakeLister{srcs: mkSources(8)}
	checker := &fakeChecker{delay: 50 * time.Millisecond}
	s := New(lister, checker, time.Hour, 1, time.Now, quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.sweep(ctx); close(done) }()

	// Let the first check start, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep did not return promptly after mid-flight cancel")
	}
	// With concurrency 1 and 50ms per check, an 8-source sweep would take
	// ~400ms; cancelling at 20ms must have skipped most. Assert we did NOT
	// check all 8 — the cancel actually curtailed the sweep.
	if got := len(checker.checked()); got >= 8 {
		t.Errorf("checked %d sources, expected cancel to curtail the sweep before all 8", got)
	}
}

// TestRun_TicksRepeatedly proves the poller keeps sweeping on its interval,
// not just once. A short interval drives multiple sweeps; we count list calls.
func TestRun_TicksRepeatedly(t *testing.T) {
	lister := &fakeLister{srcs: mkSources(1)}
	checker := &fakeChecker{}
	s := New(lister, checker, 15*time.Millisecond, 1, time.Now, quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()

	// Immediate sweep = 1 list call; then ~1 per 15ms. Wait for >= 3 to prove
	// the ticker fired at least twice beyond the immediate sweep.
	waitFor(t, func() bool { return lister.callCount() >= 3 }, time.Second)
	cancel()
	<-done
}

// TestNew_ClampsConcurrency proves a sub-1 concurrency is clamped so an
// enabled poller always makes progress.
func TestNew_ClampsConcurrency(t *testing.T) {
	lister := &fakeLister{srcs: mkSources(2)}
	checker := &fakeChecker{}
	s := New(lister, checker, time.Hour, 0, time.Now, quietLog())
	if s.concurrency != 1 {
		t.Errorf("concurrency = %d, want clamped to 1", s.concurrency)
	}
	s.sweep(context.Background())
	if len(checker.checked()) != 2 {
		t.Errorf("checked %d, want 2 (clamped pool must still run)", len(checker.checked()))
	}
}

// waitFor polls cond until it returns true or the timeout elapses, failing
// the test on timeout. Used instead of fixed sleeps so the concurrency tests
// stay fast and non-flaky.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
