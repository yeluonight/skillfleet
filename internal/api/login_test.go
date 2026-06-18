package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
	"github.com/yeluonight/skillfleet/internal/session"
	"github.com/yeluonight/skillfleet/migrations"
)

func newTestServerWithLimits(t *testing.T, ipRate, userRate ratelimit.Rate) (*httptest.Server, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "login.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewRouter(Deps{
		DB:         d,
		Now:        time.Now,
		Audit:      audit.New(d, nil, time.Now),
		SessionTTL: time.Hour,
		LoginIP:    ipRate,
		LoginUser:  userRate,
	}))
	t.Cleanup(func() {
		srv.Close()
		_ = d.Close()
	})
	return srv, d
}

// setupFixture seeds an admin user by driving setup.EnsureCode +
// /api/setup directly. After it returns, the server has exactly one
// admin user matching (username, password).
func setupFixture(t *testing.T, srv *httptest.Server, d *sql.DB, username, password string) {
	t.Helper()
	// EnsureCode in DB; we'd parse stderr if we used the binary, but
	// in unit tests we go straight through the package.
	// (Imported via internal/setup at top.)
	code, _, err := ensureSetupCode(t, d)
	if err != nil {
		t.Fatalf("ensureSetupCode: %v", err)
	}
	resp := postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     code,
		"username": username,
		"password": password,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("setup status = %d, body=%s", resp.StatusCode, body)
	}
}

func TestLogin_HappyPath(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 50, Window: time.Minute},
	)
	setupFixture(t, srv, d, "alice", "correcthorsebatterystaple")

	resp := postJSON(t, srv.URL+"/api/login", map[string]string{
		"username": "alice",
		"password": "correcthorsebatterystaple",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}

	// Cookie set, HttpOnly + SameSite=Strict + Path=/
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("sf_session cookie not set")
	}
	if !sessionCookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/" {
		t.Errorf("Path = %q", sessionCookie.Path)
	}
	if sessionCookie.Secure {
		t.Error("Secure should be false when HTTPS=false")
	}

	// Body
	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	if lr.Username != "alice" || !strings.HasPrefix(lr.UserID, "usr_") {
		t.Errorf("body = %+v", lr)
	}

	// audit row recorded
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='auth.login.success'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("success audit count = %d", count)
	}

	// session row recorded
	if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, lr.UserID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("session count = %d", count)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 50, Window: time.Minute},
	)
	setupFixture(t, srv, d, "alice", "correcthorsebatterystaple")

	resp := postJSON(t, srv.URL+"/api/login", map[string]string{
		"username": "alice",
		"password": "wrong",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName {
			t.Error("cookie set on failed login")
		}
	}

	var failures int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='auth.login.failure'`).Scan(&failures)
	if failures != 1 {
		t.Errorf("failure audit count = %d", failures)
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 50, Window: time.Minute},
	)
	setupFixture(t, srv, d, "alice", "correcthorsebatterystaple")

	resp := postJSON(t, srv.URL+"/api/login", map[string]string{
		"username": "ghost",
		"password": "anything12345",
	})
	defer resp.Body.Close()
	// Response shape must be indistinguishable from wrong-password.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid_credentials") {
		t.Errorf("body = %s", body)
	}
}

func TestLogin_RateLimit_PerIP(t *testing.T) {
	// 2 IP attempts per minute → 3rd call denied.
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 2, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	setupFixture(t, srv, d, "alice", "correcthorsebatterystaple")

	for i := 0; i < 2; i++ {
		resp := postJSON(t, srv.URL+"/api/login", map[string]string{
			"username": "alice", "password": "wrong",
		})
		resp.Body.Close()
	}
	resp := postJSON(t, srv.URL+"/api/login", map[string]string{
		"username": "alice", "password": "wrong",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}

	var rl int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='auth.login.rate_limited'`).Scan(&rl)
	if rl < 1 {
		t.Errorf("rate-limited audit count = %d", rl)
	}
}

func TestLogin_RateLimit_PerUser(t *testing.T) {
	// 2 user attempts but very high IP rate → bucket trips on user.
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 2, Window: time.Minute},
	)
	setupFixture(t, srv, d, "alice", "correcthorsebatterystaple")

	for i := 0; i < 2; i++ {
		postJSON(t, srv.URL+"/api/login", map[string]string{
			"username": "alice", "password": "wrong",
		}).Body.Close()
	}
	resp := postJSON(t, srv.URL+"/api/login", map[string]string{
		"username": "alice", "password": "wrong",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
}

func TestLogin_RejectsBadContentType(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Post(srv.URL+"/api/login", "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestLogin_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Get(srv.URL + "/api/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
