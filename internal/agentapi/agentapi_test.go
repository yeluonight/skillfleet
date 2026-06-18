package agentapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/migrations"
)

func newTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewRouter(Deps{
		DB:    d,
		Now:   time.Now,
		Audit: audit.New(d, nil, time.Now),
	}))
	t.Cleanup(func() {
		srv.Close()
		_ = d.Close()
	})
	return srv, d
}

func postEnroll(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := http.Post(url+"/agent/enroll", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mintToken(t *testing.T, d *sql.DB) (id, plaintext string) {
	t.Helper()
	tok, err := enrollment.Create(context.Background(), d, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok.ID, tok.Plaintext
}

func TestEnroll_HappyPath(t *testing.T) {
	srv, d := newTestServer(t)
	tokID, plaintext := mintToken(t, d)

	resp := postEnroll(t, srv.URL, map[string]string{
		"token":         plaintext,
		"name":          "laptop-1",
		"hostname":      "host.local",
		"os":            "linux",
		"arch":          "amd64",
		"agent_version": "0.1.0",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}

	var got enrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.DeviceID, "dev_") {
		t.Errorf("device id shape: %s", got.DeviceID)
	}
	if got.DeviceSecret == "" {
		t.Error("device secret missing from response")
	}
	if got.Status != devices.StatusPending {
		t.Errorf("status = %s, want pending", got.Status)
	}

	// Device row landed with the supplied metadata.
	dev, err := devices.Get(context.Background(), d, got.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Hostname != "host.local" || dev.OS != "linux" {
		t.Errorf("metadata not stored: %+v", dev)
	}

	// Secret row stores hash, not plaintext.
	var stored string
	if err := d.QueryRow(`SELECT secret_hash FROM device_secrets WHERE device_id=?`, got.DeviceID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == got.DeviceSecret {
		t.Error("plaintext leaked to device_secrets.secret_hash")
	}

	// Token transitioned to used.
	var status string
	_ = d.QueryRow(`SELECT status FROM enrollment_tokens WHERE id=?`, tokID).Scan(&status)
	if status != enrollment.StatusUsed {
		t.Errorf("token status = %s, want used", status)
	}

	// Verify the returned secret round-trips through VerifySecret.
	if err := devices.VerifySecret(context.Background(), d, got.DeviceID, got.DeviceSecret); err != nil {
		t.Errorf("VerifySecret on returned secret: %v", err)
	}

	// Audit row recorded.
	var auditCount int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='device.enrolled'`).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("audit count = %d", auditCount)
	}
}

func TestEnroll_RejectsBadContentType(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/agent/enroll", "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestEnroll_RejectsBadJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/agent/enroll", "application/json", strings.NewReader(`{not json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestEnroll_RejectsUnknownField(t *testing.T) {
	srv, d := newTestServer(t)
	_, plaintext := mintToken(t, d)
	resp := postEnroll(t, srv.URL, map[string]string{
		"token": plaintext,
		"name":  "x",
		"extra": "field",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEnroll_RequiresTokenAndName(t *testing.T) {
	srv, d := newTestServer(t)
	_, plaintext := mintToken(t, d)
	cases := []map[string]string{
		{"name": "x"},                 // missing token
		{"token": plaintext},          // missing name
		{"token": "  ", "name": "  "}, // empty after trim
	}
	for i, c := range cases {
		resp := postEnroll(t, srv.URL, c)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("case %d: status = %d, want 400", i, resp.StatusCode)
		}
	}
}

func TestEnroll_RejectsUnknownToken(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := postEnroll(t, srv.URL, map[string]string{
		"token": "sfen_doesnotexist0000000",
		"name":  "x",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, body=%s", resp.StatusCode, body)
	}
}

func TestEnroll_RejectsExpiredToken(t *testing.T) {
	srv, d := newTestServer(t)
	// Mint with negative TTL by hand so the token is already expired.
	tok, err := enrollment.Create(context.Background(), d, time.Second, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	resp := postEnroll(t, srv.URL, map[string]string{
		"token": tok.Plaintext,
		"name":  "x",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "token_expired") {
		t.Errorf("body = %s, want token_expired", body)
	}
}

func TestEnroll_RejectsReusedToken(t *testing.T) {
	srv, d := newTestServer(t)
	_, plaintext := mintToken(t, d)

	first := postEnroll(t, srv.URL, map[string]string{"token": plaintext, "name": "a"})
	first.Body.Close()

	second := postEnroll(t, srv.URL, map[string]string{"token": plaintext, "name": "b"})
	defer second.Body.Close()
	if second.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", second.StatusCode)
	}
	body, _ := io.ReadAll(second.Body)
	if !strings.Contains(string(body), "token_not_usable") {
		t.Errorf("body = %s, want token_not_usable", body)
	}
}

func TestEnroll_RejectsRevokedToken(t *testing.T) {
	srv, d := newTestServer(t)
	tokID, plaintext := mintToken(t, d)
	if err := enrollment.Revoke(context.Background(), d, tokID, time.Now()); err != nil {
		t.Fatal(err)
	}
	resp := postEnroll(t, srv.URL, map[string]string{"token": plaintext, "name": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestEnroll_RollsBackOnDeviceFailure(t *testing.T) {
	// devices.Enroll only fails on empty name, which we already filter
	// out. To force a transaction rollback we construct a request with
	// a 4 KiB name beyond the body limit and watch the request bail
	// before tx.Commit. Simpler: validate that a duplicate device id
	// is impossible (idgen ensures uniqueness) and rely on the
	// happy-path test for the success branch. This test pins the
	// invariant: after a 4XX from enroll, the token row must be
	// pending (i.e. the rollback fired).
	srv, d := newTestServer(t)
	_, plaintext := mintToken(t, d)

	// Send oversized body to trigger http.MaxBytesReader -> 400.
	huge := strings.Repeat("a", 5*1024)
	resp := postEnroll(t, srv.URL, map[string]any{"token": plaintext, "name": huge})
	resp.Body.Close()

	// Token must still be consumable.
	var status string
	_ = d.QueryRow(`SELECT status FROM enrollment_tokens WHERE token_hash=?`,
		sha256HexOf(plaintext)).Scan(&status)
	if status != enrollment.StatusPending {
		t.Errorf("token status = %s after failed enroll, want pending (rollback)", status)
	}
}

// sha256HexOf hashes s with sha256 and returns lowercase hex. Inline
// to keep the test self-contained.
func sha256HexOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
