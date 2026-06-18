// Package sfhmac implements the HMAC-SHA256 request signing scheme
// used between the agent and the server (v1.0 §4.2).
//
// Both sides share this code so the algorithm definition lives in one
// place. The package is named sfhmac (not hmac) so it does not shadow
// the stdlib crypto/hmac import in callers that need both.
//
// Wire format. Every agent-side request carries five headers:
//
//	X-SF-Device-Id     opaque device identifier minted at enroll
//	X-SF-Timestamp     unix seconds (string-encoded), to scope the signature in time
//	X-SF-Nonce         random per-request value, to scope it in space (replay protection)
//	X-SF-Body-SHA256   lowercase hex sha256 of the request body (empty string allowed for "")
//	X-SF-Signature     lowercase hex hmac-sha256 of the canonical string below
//
// Canonical string (no trailing newline, "\n" between fields):
//
//	method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + body_sha256
//
// `method` is upper-case. `path` is exactly what hit the server (no
// host, no query rewriting); the agent sends it as r.URL.Path. The
// caller is responsible for choosing a unique nonce; the package
// supplies NewNonce for convenience.
package sfhmac

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header names. Exposed so middleware on both ends can refer to a
// single source of truth.
const (
	HeaderDeviceID  = "X-SF-Device-Id"
	HeaderTimestamp = "X-SF-Timestamp"
	HeaderNonce     = "X-SF-Nonce"
	HeaderBodyHash  = "X-SF-Body-SHA256"
	HeaderSignature = "X-SF-Signature"
)

// NonceByteLen is the random byte count used by NewNonce. 18 bytes →
// 24 base64url chars, comfortably below typical header size limits.
const NonceByteLen = 18

// Errors returned from Verify / Parse.
var (
	ErrMissingHeader = errors.New("sfhmac: required header missing")
	ErrBadTimestamp  = errors.New("sfhmac: timestamp not a unix integer")
	ErrSignature     = errors.New("sfhmac: signature mismatch")
	ErrBodyHash      = errors.New("sfhmac: body hash mismatch")
)

// BodySHA256 returns lowercase hex sha256(body). An empty body still
// hashes (to the well-known empty-string digest); callers don't need
// to special-case nil.
func BodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Sign returns the lowercase hex HMAC-SHA256 over the canonical
// string described in the package doc. `ts` is rendered as the
// decimal unix seconds it represents.
func Sign(secret, method, path string, ts time.Time, nonce, bodyHash string) string {
	m := hmac.New(sha256.New, []byte(secret))
	// Build the canonical string in a single Write per field to avoid
	// the temptation of joining into one string with strings.Join
	// (which allocates).
	m.Write([]byte(strings.ToUpper(method)))
	m.Write([]byte("\n"))
	m.Write([]byte(path))
	m.Write([]byte("\n"))
	m.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	m.Write([]byte("\n"))
	m.Write([]byte(nonce))
	m.Write([]byte("\n"))
	m.Write([]byte(bodyHash))
	return hex.EncodeToString(m.Sum(nil))
}

// Verify returns nil iff `sig` matches the expected HMAC for the given
// inputs. Comparison is constant-time.
func Verify(secret, method, path string, ts time.Time, nonce, bodyHash, sig string) error {
	expect := Sign(secret, method, path, ts, nonce, bodyHash)
	if !hmac.Equal([]byte(expect), []byte(sig)) {
		return ErrSignature
	}
	return nil
}

// NewNonce returns a fresh base64url-encoded random nonce of
// NonceByteLen bytes of entropy. crypto/rand failure (effectively
// impossible on Linux) bubbles up as an error.
func NewNonce() (string, error) {
	var buf [NonceByteLen]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("sfhmac: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// SignRequest is the agent-side convenience: hash the (possibly nil)
// body, sign the canonical string with the device secret, install all
// five X-SF-* headers, and replace r.Body so the request can still be
// transported by net/http. The same body bytes are visible to the
// receiving server when it computes its own body hash.
//
// If nonce == "", a fresh one is minted via NewNonce.
func SignRequest(r *http.Request, deviceID, secret, nonce string, ts time.Time, body []byte) error {
	if r == nil {
		return errors.New("sfhmac: nil request")
	}
	if deviceID == "" || secret == "" {
		return errors.New("sfhmac: deviceID and secret required")
	}
	if nonce == "" {
		n, err := NewNonce()
		if err != nil {
			return err
		}
		nonce = n
	}
	bodyHash := BodySHA256(body)
	sig := Sign(secret, r.Method, r.URL.Path, ts, nonce, bodyHash)

	r.Header.Set(HeaderDeviceID, deviceID)
	r.Header.Set(HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
	r.Header.Set(HeaderNonce, nonce)
	r.Header.Set(HeaderBodyHash, bodyHash)
	r.Header.Set(HeaderSignature, sig)

	// net/http needs Body re-set even if we got the bytes from outside,
	// so the transport can read them. ContentLength is updated to match.
	if body == nil {
		r.Body = http.NoBody
		r.ContentLength = 0
	} else {
		// Use a closure-based reader so each attempt sees fresh bytes;
		// http.Request.GetBody is used during redirects.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		r.ContentLength = int64(len(body))
		bb := body
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(bb))), nil
		}
	}
	return nil
}

// Headers groups the parsed signature headers extracted from an
// incoming request. Server-side middleware feeds these into Verify
// after additional checks (device_id known, status approved, ts in
// window, nonce not seen).
type Headers struct {
	DeviceID  string
	Timestamp time.Time
	Nonce     string
	BodyHash  string
	Signature string
}

// Parse extracts the five X-SF-* headers from r and validates that
// none are empty and the timestamp is a unix integer. It does NOT
// validate the signature itself — the caller (middleware) does so
// after also enforcing the time window and nonce-uniqueness rules.
func Parse(r *http.Request) (Headers, error) {
	h := Headers{
		DeviceID:  r.Header.Get(HeaderDeviceID),
		Nonce:     r.Header.Get(HeaderNonce),
		BodyHash:  r.Header.Get(HeaderBodyHash),
		Signature: r.Header.Get(HeaderSignature),
	}
	if h.DeviceID == "" || h.Nonce == "" || h.BodyHash == "" || h.Signature == "" {
		return Headers{}, ErrMissingHeader
	}
	raw := r.Header.Get(HeaderTimestamp)
	if raw == "" {
		return Headers{}, ErrMissingHeader
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Headers{}, ErrBadTimestamp
	}
	h.Timestamp = time.Unix(secs, 0)
	return h, nil
}
