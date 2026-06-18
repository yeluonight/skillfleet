// Package csrf implements the double-submit cookie pattern described
// in IMPLEMENTATION_PLAN.md §9.3.
//
// The cookie sf_csrf carries a random 32-byte token. Mutating requests
// (POST / PUT / DELETE / PATCH) must echo the same value in the
// X-CSRF-Token header. A browser running attacker JS on another origin
// cannot read the cookie (SameSite=Strict, plus we'd refuse cross-site
// requests anyway), so it cannot construct a valid header — the
// double-submit invariant catches the forgery.
//
// The cookie intentionally is NOT HttpOnly: the WebUI's JS reads it to
// populate the header. SameSite=Strict + Secure (under HTTPS) carries
// the confidentiality. The DB never persists the token; rotation just
// overwrites the cookie.
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
)

// CookieName is the cookie carrying the CSRF token.
const CookieName = "sf_csrf"

// HeaderName is the request header mutating requests must echo.
const HeaderName = "X-CSRF-Token"

// tokenLen matches the session token (32 bytes -> 43 base64url chars).
const tokenLen = 32

// ErrMissing is returned when either the cookie or the header is
// absent.
var ErrMissing = errors.New("csrf: token missing")

// ErrMismatch is returned when the cookie and header values differ.
var ErrMismatch = errors.New("csrf: token mismatch")

// NewToken returns a fresh base64url-encoded token.
func NewToken() (string, error) {
	var raw [tokenLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("csrf: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// SetCookie writes the sf_csrf cookie on w.
//
// secure should be true under HTTPS so the cookie is not exposed on
// plaintext requests. SameSite=Strict is unconditional — there is no
// legitimate cross-site write path for SkillFleet.
func SetCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // WebUI reads this from JS — see package doc
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   0, // session cookie; tied to the browser session
	})
}

// ClearCookie writes an immediately-expiring sf_csrf cookie on w. Use
// on logout to wipe the value from the browser.
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// Verify checks that r's CSRF cookie matches its X-CSRF-Token header
// in constant time. Both must be present and identical; either being
// missing is ErrMissing, a value diff is ErrMismatch.
func Verify(r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return ErrMissing
	}
	header := r.Header.Get(HeaderName)
	if header == "" {
		return ErrMissing
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 {
		return ErrMismatch
	}
	return nil
}
