package api

import (
	"bytes"
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
	"github.com/yeluonight/skillfleet/internal/setup"
	"github.com/yeluonight/skillfleet/migrations"
)

func newTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
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
		LoginIP:    ratelimit.Rate{Limit: 100, Window: time.Minute},
		LoginUser:  ratelimit.Rate{Limit: 50, Window: time.Minute},
	}))
	t.Cleanup(func() {
		srv.Close()
		_ = d.Close()
	})
	return srv, d
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestStatus_BeforeAndAfterSetup(t *testing.T) {
	srv, d := newTestServer(t)
	must := func(req *http.Response, _ error) *http.Response {
		t.Helper()
		return req
	}
	// Before:
	r := must(http.Get(srv.URL + "/api/status"))
	defer r.Body.Close()
	if got := decodeBool(t, r.Body, "setup_required"); !got {
		t.Errorf("setup_required = false before setup")
	}

	// Drive a real setup directly (we test the handler separately
	// below; here we just want to flip status).
	code, _, err := setup.EnsureCode(context.Background(), d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Consume(context.Background(), d, code, "alice", "correcthorsebatterystaple", time.Now()); err != nil {
		t.Fatal(err)
	}

	r2 := must(http.Get(srv.URL + "/api/status"))
	defer r2.Body.Close()
	if got := decodeBool(t, r2.Body, "setup_required"); got {
		t.Errorf("setup_required = true after setup")
	}
}

func TestSetup_HappyPath(t *testing.T) {
	srv, d := newTestServer(t)
	code, _, err := setup.EnsureCode(context.Background(), d, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     code,
		"username": "alice",
		"password": "correcthorsebatterystaple",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got setupResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "alice" || !strings.HasPrefix(got.UserID, "usr_") {
		t.Errorf("body = %+v", got)
	}
}

func TestSetup_TwiceRejected(t *testing.T) {
	srv, d := newTestServer(t)
	code, _, _ := setup.EnsureCode(context.Background(), d, time.Now())
	postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     code,
		"username": "alice",
		"password": "correcthorsebatterystaple",
	}).Body.Close()

	resp := postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     code,
		"username": "bob",
		"password": "correcthorsebatterystaple",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestSetup_WrongCode(t *testing.T) {
	srv, d := newTestServer(t)
	if _, _, err := setup.EnsureCode(context.Background(), d, time.Now()); err != nil {
		t.Fatal(err)
	}
	resp := postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     "SF-SETUP-AAAA-BBBB",
		"username": "alice",
		"password": "correcthorsebatterystaple",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestSetup_NoPending(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     "SF-SETUP-AAAA-BBBB",
		"username": "alice",
		"password": "correcthorsebatterystaple",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestSetup_RejectsBadContentType(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/setup", "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", resp.StatusCode)
	}
}

func TestSetup_RejectsUnknownFields(t *testing.T) {
	srv, d := newTestServer(t)
	if _, _, err := setup.EnsureCode(context.Background(), d, time.Now()); err != nil {
		t.Fatal(err)
	}
	resp := postJSON(t, srv.URL+"/api/setup", map[string]string{
		"code":     "SF-SETUP-AAAA-BBBB",
		"username": "alice",
		"password": "correcthorsebatterystaple",
		"extra":    "should reject",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSetup_RejectsBadJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/setup", "application/json", strings.NewReader(`{not json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSetup_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// http.ServeMux returns 405 with Allow header for method-mismatched routes.
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// --- helpers ---

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeBool(t *testing.T, body io.Reader, key string) bool {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	v, ok := m[key].(bool)
	if !ok {
		t.Fatalf("key %q missing or not a bool: %v", key, m)
	}
	return v
}
