package csrf

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewTokenShape(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 32 {
		t.Errorf("token too short: %d", len(tok))
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Errorf("token must be base64url (no +/=): %q", tok)
	}
}

func TestSetCookieAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	SetCookie(w, "abc", true)
	resp := w.Result()
	defer resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieName || c.Value != "abc" {
		t.Errorf("cookie name/val = %s=%s", c.Name, c.Value)
	}
	if c.HttpOnly {
		t.Error("HttpOnly must be false — JS reads this cookie")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if !c.Secure {
		t.Error("Secure should be true when secure=true")
	}
	if c.Path != "/" {
		t.Errorf("Path = %q", c.Path)
	}
}

func TestClearCookieExpires(t *testing.T) {
	w := httptest.NewRecorder()
	ClearCookie(w, false)
	c := w.Result().Cookies()[0]
	if c.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("Value = %q, want empty", c.Value)
	}
}

func TestVerifyHappyPath(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "tok-123"})
	r.Header.Set(HeaderName, "tok-123")
	if err := Verify(r); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestVerifyMissingCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(HeaderName, "tok-123")
	if err := Verify(r); !errors.Is(err, ErrMissing) {
		t.Errorf("err = %v, want ErrMissing", err)
	}
}

func TestVerifyMissingHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "tok-123"})
	if err := Verify(r); !errors.Is(err, ErrMissing) {
		t.Errorf("err = %v, want ErrMissing", err)
	}
}

func TestVerifyEmptyCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: ""})
	r.Header.Set(HeaderName, "tok-123")
	if err := Verify(r); !errors.Is(err, ErrMissing) {
		t.Errorf("err = %v, want ErrMissing", err)
	}
}

func TestVerifyMismatch(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "tok-A"})
	r.Header.Set(HeaderName, "tok-B")
	if err := Verify(r); !errors.Is(err, ErrMismatch) {
		t.Errorf("err = %v, want ErrMismatch", err)
	}
}
