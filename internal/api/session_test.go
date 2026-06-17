package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/csrf"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
	"github.com/yeluonight/skillfleet/internal/session"
)

func setupAndLogin(t *testing.T, srv *httptest.Server, d *sql.DB, username, password string) (sessionCookie, csrfCookie *http.Cookie) {
	t.Helper()
	setupFixture(t, srv, d, username, password)
	resp := postJSON(t, srv.URL+"/api/login", map[string]string{
		"username": username, "password": password,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	for _, c := range resp.Cookies() {
		switch c.Name {
		case session.CookieName:
			sessionCookie = c
		case csrf.CookieName:
			csrfCookie = c
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("missing cookies: session=%v csrf=%v", sessionCookie, csrfCookie)
	}
	return
}

func TestMe_HappyPath(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, _ := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/me", nil)
	req.AddCookie(sc)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	var got meResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Username != "alice" || !strings.HasPrefix(got.UserID, "usr_") {
		t.Errorf("body = %+v", got)
	}
}

func TestMe_RejectsNoCookie(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Get(srv.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMe_RejectsBogusCookie(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "not-a-real-token"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMe_RefreshesMissingCsrfCookie(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, _ := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Send only the session cookie; expect /api/me to top up sf_csrf.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/me", nil)
	req.AddCookie(sc)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()

	var refreshed *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == csrf.CookieName {
			refreshed = c
		}
	}
	if refreshed == nil || refreshed.Value == "" {
		t.Errorf("expected sf_csrf to be refreshed: %v", refreshed)
	}
}

func TestLogout_RequiresCSRF(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, _ := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/logout", nil)
	req.AddCookie(sc)
	// No CSRF cookie, no header → 403.
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestLogout_RejectsCsrfMismatch(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/logout", nil)
	req.AddCookie(sc)
	req.AddCookie(cc)
	req.Header.Set(csrf.HeaderName, "wrong-value")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "csrf_mismatch") {
		t.Errorf("body = %s", body)
	}
}

func TestLogout_HappyPath(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Logout with matching CSRF.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/logout", nil)
	req.AddCookie(sc)
	req.AddCookie(cc)
	req.Header.Set(csrf.HeaderName, cc.Value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}

	// Cookie wipes returned?
	var seenSess, seenCSRF bool
	for _, c := range resp.Cookies() {
		switch c.Name {
		case session.CookieName:
			if c.MaxAge != -1 && !c.Expires.Before(time.Now()) {
				t.Errorf("session cookie not expired: %+v", c)
			}
			seenSess = true
		case csrf.CookieName:
			if c.MaxAge != -1 && !c.Expires.Before(time.Now()) {
				t.Errorf("csrf cookie not expired: %+v", c)
			}
			seenCSRF = true
		}
	}
	if !seenSess || !seenCSRF {
		t.Errorf("missing cookie-clear directives: sess=%v csrf=%v", seenSess, seenCSRF)
	}

	// Re-using the session cookie after logout must fail.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/me", nil)
	req2.AddCookie(sc)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil { t.Fatal(err) }
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-logout /api/me status = %d, want 401", resp2.StatusCode)
	}

	// audit row?
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='auth.logout'`).Scan(&n)
	if n != 1 {
		t.Errorf("logout audit count = %d", n)
	}
}

func TestLogin_IssuesCsrfCookie(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	_, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	if cc.HttpOnly {
		t.Error("CSRF cookie must NOT be HttpOnly")
	}
	if cc.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", cc.SameSite)
	}
}
