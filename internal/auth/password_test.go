package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("encoded prefix wrong: %s", h)
	}
	if err := VerifyPassword(h, "correct horse battery staple"); err != nil {
		t.Errorf("verify on matching password: %v", err)
	}
	if err := VerifyPassword(h, "wrong password"); err != ErrPasswordMismatch {
		t.Errorf("verify on wrong password: %v, want ErrPasswordMismatch", err)
	}
}

func TestHashUniquePerCall(t *testing.T) {
	a, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("salt not randomised: both hashes identical")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":      "",
		"junk":       "not-an-argon2-string",
		"wrong-alg":  "$argon2d$v=19$m=65536,t=3,p=2$AAAA$BBBB",
		"bad-params": "$argon2id$v=19$bad$AAAA$BBBB",
		"truncated":  "$argon2id$v=19$m=65536,t=3,p=2$AAAA",
	}
	for name, encoded := range cases {
		if err := VerifyPassword(encoded, "x"); err != ErrPasswordMismatch {
			t.Errorf("%s: err = %v, want ErrPasswordMismatch", name, err)
		}
	}
}

func TestHashRejectsEmptyPassword(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Errorf("HashPassword(\"\") should error")
	}
}
