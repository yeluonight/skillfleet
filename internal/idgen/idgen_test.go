package idgen

import (
	"strings"
	"testing"
)

func TestNewShape(t *testing.T) {
	got := New("usr")
	if !strings.HasPrefix(got, "usr_") {
		t.Errorf("missing prefix: %q", got)
	}
	if len(got) != 4+26 {
		t.Errorf("length = %d, want 30 (prefix 4 + body 26): %q", len(got), got)
	}
	body := strings.TrimPrefix(got, "usr_")
	for _, c := range body {
		if !(c >= 'a' && c <= 'z') && !(c >= '2' && c <= '7') {
			t.Errorf("body has non-lower-base32 char %q: %q", c, got)
			break
		}
	}
}

func TestNewUniqueness(t *testing.T) {
	seen := map[string]bool{}
	const n = 1000
	for i := 0; i < n; i++ {
		id := New("ses")
		if seen[id] {
			t.Fatalf("collision after %d ids", i+1)
		}
		seen[id] = true
	}
}

func TestNewRejectsBadPrefix(t *testing.T) {
	cases := []string{"", "a", "TOOLONGPREFIX", "Has1Digit", "WITHUPPER", "us r"}
	for _, p := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%q) did not panic", p)
				}
			}()
			_ = New(p)
		}()
	}
}
