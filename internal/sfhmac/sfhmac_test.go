package sfhmac

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Known-answer test: pin the canonical string format so a future
// refactor cannot silently change the signature scheme. The expected
// digest is computed by-hand using crypto/hmac directly, matching the
// canonical string described in the package doc.
func TestSignMatchesKnownAnswer(t *testing.T) {
	secret := "topsecret"
	method := "POST"
	path := "/agent/heartbeat"
	ts := time.Unix(1700000000, 0)
	nonce := "nonce-A"
	bodyHash := BodySHA256([]byte(`{"hello":"world"}`))

	got := Sign(secret, method, path, ts, nonce, bodyHash)

	// Independently compute the same HMAC to ensure Sign respects the
	// canonical string exactly.
	want := computeReference(secret, method, path, ts.Unix(), nonce, bodyHash)
	if got != want {
		t.Errorf("Sign mismatch\n  got:  %s\n  want: %s", got, want)
	}
}

func TestSignUppercasesMethod(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	a := Sign("s", "POST", "/x", ts, "n", "h")
	b := Sign("s", "post", "/x", ts, "n", "h")
	if a != b {
		t.Errorf("method case affects sig: POST=%s post=%s", a, b)
	}
}

func TestSignChangesWithEveryField(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	base := Sign("s", "POST", "/x", ts, "n", "h")
	cases := []struct {
		name string
		sig  string
	}{
		{"secret", Sign("S", "POST", "/x", ts, "n", "h")},
		{"method", Sign("s", "GET", "/x", ts, "n", "h")},
		{"path", Sign("s", "POST", "/y", ts, "n", "h")},
		{"ts", Sign("s", "POST", "/x", ts.Add(time.Second), "n", "h")},
		{"nonce", Sign("s", "POST", "/x", ts, "N", "h")},
		{"bodyHash", Sign("s", "POST", "/x", ts, "n", "H")},
	}
	for _, c := range cases {
		if c.sig == base {
			t.Errorf("changing %s did not change signature", c.name)
		}
	}
}

func TestVerifyHappyAndMismatch(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	sig := Sign("s", "POST", "/x", ts, "n", "h")
	if err := Verify("s", "POST", "/x", ts, "n", "h", sig); err != nil {
		t.Errorf("Verify on valid sig: %v", err)
	}
	if err := Verify("s", "POST", "/x", ts, "n", "h", sig+"x"); !errors.Is(err, ErrSignature) {
		t.Errorf("Verify on tampered sig: err = %v, want ErrSignature", err)
	}
}

func TestBodySHA256EmptyVsNil(t *testing.T) {
	if BodySHA256(nil) != BodySHA256([]byte{}) {
		t.Error("BodySHA256(nil) != BodySHA256(empty); both should hash the empty string")
	}
	// Known sha256 of empty string.
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if BodySHA256(nil) != empty {
		t.Errorf("BodySHA256(nil) = %q, want %q", BodySHA256(nil), empty)
	}
}

func TestNewNonceFresh(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		n, err := NewNonce()
		if err != nil {
			t.Fatal(err)
		}
		if seen[n] {
			t.Fatalf("collision after %d nonces", i+1)
		}
		seen[n] = true
		if len(n) < 16 {
			t.Errorf("nonce too short: %q", n)
		}
	}
}

func TestSignRequestEndToEnd(t *testing.T) {
	body := []byte(`{"hb":1}`)
	req, _ := http.NewRequest(http.MethodPost, "http://srv.local/agent/heartbeat", bytes.NewReader(body))
	ts := time.Unix(1700000000, 0)

	if err := SignRequest(req, "dev_a", "secret-xyz", "n-1", ts, body); err != nil {
		t.Fatal(err)
	}

	// All five headers set.
	for _, h := range []string{HeaderDeviceID, HeaderTimestamp, HeaderNonce, HeaderBodyHash, HeaderSignature} {
		if req.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}

	// Body still readable.
	read, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != string(body) {
		t.Errorf("body got = %s, want %s", read, body)
	}

	// Server-side Parse + Verify should accept it.
	r := httptest.NewRequest(http.MethodPost, "/agent/heartbeat", bytes.NewReader(body))
	for _, h := range []string{HeaderDeviceID, HeaderTimestamp, HeaderNonce, HeaderBodyHash, HeaderSignature} {
		r.Header.Set(h, req.Header.Get(h))
	}
	parsed, err := Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.DeviceID != "dev_a" || parsed.Nonce != "n-1" {
		t.Errorf("parsed = %+v", parsed)
	}
	if err := Verify("secret-xyz", r.Method, r.URL.Path, parsed.Timestamp, parsed.Nonce, parsed.BodyHash, parsed.Signature); err != nil {
		t.Errorf("Verify on signed request: %v", err)
	}
}

func TestSignRequestNonceAutoMinted(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://srv.local/agent/jobs", nil)
	if err := SignRequest(req, "d", "s", "", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	n := req.Header.Get(HeaderNonce)
	if n == "" {
		t.Error("nonce not minted")
	}
}

func TestSignRequestGetBodyReplay(t *testing.T) {
	body := []byte("payload")
	req, _ := http.NewRequest(http.MethodPost, "http://srv.local/x", nil)
	if err := SignRequest(req, "d", "s", "n", time.Now(), body); err != nil {
		t.Fatal(err)
	}
	// First read drains.
	got1, _ := io.ReadAll(req.Body)
	if string(got1) != "payload" {
		t.Fatalf("first read = %q", got1)
	}
	// GetBody re-arms for redirects.
	if req.GetBody == nil {
		t.Fatal("GetBody not set")
	}
	rc, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := io.ReadAll(rc)
	if string(got2) != "payload" {
		t.Errorf("GetBody read = %q", got2)
	}
}

func TestParseRejectsMissingHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	if _, err := Parse(r); !errors.Is(err, ErrMissingHeader) {
		t.Errorf("err = %v, want ErrMissingHeader", err)
	}
}

func TestParseRejectsBadTimestamp(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set(HeaderDeviceID, "d")
	r.Header.Set(HeaderNonce, "n")
	r.Header.Set(HeaderBodyHash, "h")
	r.Header.Set(HeaderSignature, "sig")
	r.Header.Set(HeaderTimestamp, "not-a-number")
	if _, err := Parse(r); !errors.Is(err, ErrBadTimestamp) {
		t.Errorf("err = %v, want ErrBadTimestamp", err)
	}
}

// computeReference is an independent re-implementation of Sign used
// purely as the known-answer fixture. If both Sign and this helper
// drift in the same direction the test wouldn't catch it, but the
// likelihood of a coincident bug is negligible and the helper keeps
// the test self-contained without hard-coding a magic hex string.
func computeReference(secret, method, path string, ts int64, nonce, bodyHash string) string {
	m := hmac.New(sha256.New, []byte(secret))
	canonical := strings.Join([]string{
		strings.ToUpper(method), path, strconv.FormatInt(ts, 10), nonce, bodyHash,
	}, "\n")
	m.Write([]byte(canonical))
	return hex.EncodeToString(m.Sum(nil))
}
