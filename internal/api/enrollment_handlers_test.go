package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/csrf"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
)

// authedDo replays the canonical (session + csrf) cookie pair plus
// the matching X-CSRF-Token header so the test reads like the WebUI's
// fetch wrapper.
func authedDo(t *testing.T, sc, cc *http.Cookie, req *http.Request) *http.Response {
	t.Helper()
	req.AddCookie(sc)
	req.AddCookie(cc)
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		req.Header.Set(csrf.HeaderName, cc.Value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestEnrollmentToken_CreateReturnsPlaintextOnce(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/enrollment-tokens", nil)
	req.Header.Set("Content-Type", "application/json")
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}

	var got createEnrollmentTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ID, "tok_") {
		t.Errorf("ID shape: %s", got.ID)
	}
	if !strings.HasPrefix(got.Token, enrollment.TokenPrefix) {
		t.Errorf("Token missing sfen_ prefix: %s", got.Token)
	}
	if got.Status != enrollment.StatusPending {
		t.Errorf("Status = %s", got.Status)
	}
	if got.ExpiresAt <= got.CreatedAt {
		t.Errorf("ExpiresAt %d <= CreatedAt %d", got.ExpiresAt, got.CreatedAt)
	}

	// DB row carries only the hash, never plaintext.
	var hash string
	if err := d.QueryRow(`SELECT token_hash FROM enrollment_tokens WHERE id=?`, got.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == got.Token {
		t.Error("DB stored plaintext")
	}

	// audit row
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='enrollment.token.created'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit count = %d, want 1", n)
	}
}

func TestEnrollmentToken_CreateRequiresAuth(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Post(srv.URL+"/api/enrollment-tokens", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestEnrollmentToken_CreateRequiresCSRF(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, _ := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/enrollment-tokens", nil)
	req.AddCookie(sc)
	// No CSRF cookie / header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestEnrollmentToken_ListNeverLeaksPlaintext(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Mint two tokens.
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/enrollment-tokens", nil)
		req.Header.Set("Content-Type", "application/json")
		authedDo(t, sc, cc, req).Body.Close()
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/enrollment-tokens", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// Plaintext tokens begin with sfen_; a list response must not
	// carry that string anywhere.
	if strings.Contains(string(body), enrollment.TokenPrefix) {
		t.Errorf("list leaked plaintext: %s", body)
	}

	var got struct {
		Tokens []enrollmentTokenView `json:"tokens"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tokens) != 2 {
		t.Errorf("len = %d, want 2", len(got.Tokens))
	}
}

func TestEnrollmentToken_RevokeHappy(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Create.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/enrollment-tokens", nil)
	req.Header.Set("Content-Type", "application/json")
	resp := authedDo(t, sc, cc, req)
	var created createEnrollmentTokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Revoke.
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/enrollment-tokens/"+created.ID+"/revoke", nil)
	resp = authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}

	var status string
	_ = d.QueryRow(`SELECT status FROM enrollment_tokens WHERE id=?`, created.ID).Scan(&status)
	if status != enrollment.StatusRevoked {
		t.Errorf("status = %s, want revoked", status)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='enrollment.token.revoked'`).Scan(&n)
	if n != 1 {
		t.Errorf("revoke audit count = %d", n)
	}
}

func TestEnrollmentToken_RevokeUnknown(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/enrollment-tokens/tok_doesnotexist/revoke", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
