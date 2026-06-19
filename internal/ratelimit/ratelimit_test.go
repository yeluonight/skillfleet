package ratelimit

import (
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	cases := map[string]Rate{
		"10/min":   {10, time.Minute},
		"5/sec":    {5, time.Second},
		"1/hour":   {1, time.Hour},
		"100/s":    {100, time.Second},
		" 3 / m ":  {3, time.Minute},
	}
	for in, want := range cases {
		got, err := ParseRate(in)
		if err != nil {
			t.Errorf("ParseRate(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRate(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestParseRateRejectsBad(t *testing.T) {
	cases := []string{"", "10", "10/", "/min", "0/min", "-1/min", "10/lightyear", "abc/min"}
	for _, in := range cases {
		if _, err := ParseRate(in); err == nil {
			t.Errorf("ParseRate(%q) should error", in)
		}
	}
}

// fakeClock is a hand-cranked time.Now substitute.
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time     { return f.now }
func (f *fakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }

func TestAllowBurstThenWait(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	l := New(Rate{Limit: 3, Window: time.Minute}, clock.Now)

	for i := 0; i < 3; i++ {
		ok, _ := l.Allow("k")
		if !ok {
			t.Fatalf("burst slot %d should allow", i)
		}
	}
	ok, wait := l.Allow("k")
	if ok {
		t.Fatalf("4th call should be denied")
	}
	if wait <= 0 || wait > time.Minute {
		t.Errorf("wait = %v", wait)
	}
}

func TestAllowRefills(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	l := New(Rate{Limit: 2, Window: time.Second}, clock.Now)

	// Consume both tokens.
	l.Allow("k")
	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("should be denied immediately after burst")
	}

	// 500ms = one refill at rate 2/sec.
	clock.Advance(500 * time.Millisecond)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("should be allowed after 500ms refill")
	}
}

func TestAllowSeparatesKeys(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	l := New(Rate{Limit: 1, Window: time.Minute}, clock.Now)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a #1 should allow")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b #1 should allow (separate key)")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a #2 should deny")
	}
}

func TestAllowPrunesIdle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	l := New(Rate{Limit: 1, Window: time.Second}, clock.Now)

	l.Allow("ephemeral")
	if got := len(l.buckets); got != 1 {
		t.Fatalf("buckets = %d", got)
	}

	clock.Advance(11 * time.Second) // past 10x window
	l.Allow("trigger")              // triggers pruneLocked

	// "ephemeral" should be evicted.
	if _, has := l.buckets["ephemeral"]; has {
		t.Errorf("ephemeral bucket not pruned")
	}
}
