// Package ratelimit implements the per-key token buckets behind
// IMPLEMENTATION_PLAN.md §8.1 `auth.rate_limit`. Buckets are kept in
// memory keyed by an arbitrary string (the auth layer keys by IP and
// by username separately).
//
// The rate string format is "<N>/<unit>" where unit is one of
// s | sec | second | m | min | minute | h | hour. N also serves as
// the burst capacity, matching what operators tend to expect from a
// config line like "10/min" — ten attempts up front, then refill.
//
// Old buckets are pruned lazily on Allow; a janitor goroutine is
// overkill for a handful of login attempts per second.
package ratelimit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate is a parsed "N/unit" pair.
type Rate struct {
	Limit  int           // events allowed per Window; also burst size
	Window time.Duration // duration over which Limit applies
}

// ParseRate turns "10/min" into Rate{10, time.Minute}.
func ParseRate(s string) (Rate, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return Rate{}, fmt.Errorf("ratelimit: bad rate %q (want N/unit)", s)
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n <= 0 {
		return Rate{}, fmt.Errorf("ratelimit: bad count in %q", s)
	}
	d, err := parseUnit(strings.TrimSpace(parts[1]))
	if err != nil {
		return Rate{}, fmt.Errorf("ratelimit: %w (in %q)", err, s)
	}
	return Rate{Limit: n, Window: d}, nil
}

func parseUnit(u string) (time.Duration, error) {
	switch strings.ToLower(u) {
	case "s", "sec", "second":
		return time.Second, nil
	case "m", "min", "minute":
		return time.Minute, nil
	case "h", "hour":
		return time.Hour, nil
	}
	return 0, errors.New("unit must be s|m|h")
}

// Limiter is a sharded, per-key token bucket. The zero value is not
// usable; construct via New.
//
// Buckets refill linearly at Rate.Limit / Rate.Window. Allow consumes
// one token if available and returns true; otherwise it returns false
// and the duration until the next token becomes available.
type Limiter struct {
	rate Rate
	now  func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

// New constructs a Limiter with the given rate. If now is nil, time.Now
// is used; tests inject a fake clock to get deterministic refill.
func New(r Rate, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		rate:    r,
		now:     now,
		buckets: make(map[string]*bucket),
	}
}

// Allow attempts to consume one token for key.
//
// Returns (true, 0) on success and (false, wait) on rejection where
// wait is the duration the caller should respect before retrying.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		// New caller starts with a full bucket minus this attempt.
		l.buckets[key] = &bucket{
			tokens:   float64(l.rate.Limit) - 1,
			updated:  now,
			lastSeen: now,
		}
		return true, 0
	}

	// Refill since last touch.
	elapsed := now.Sub(b.updated).Seconds()
	refillPerSec := float64(l.rate.Limit) / l.rate.Window.Seconds()
	b.tokens += elapsed * refillPerSec
	if b.tokens > float64(l.rate.Limit) {
		b.tokens = float64(l.rate.Limit)
	}
	b.updated = now
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}

	// Compute wait: tokens needed = 1 - b.tokens
	need := 1 - b.tokens
	waitSec := need / refillPerSec
	wait := time.Duration(waitSec * float64(time.Second))
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return false, wait
}

// pruneLocked drops buckets idle for more than 10x the window. This
// keeps memory bounded under enumeration probes without breaking the
// guarantees for active callers.
func (l *Limiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-10 * l.rate.Window)
	for k, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
