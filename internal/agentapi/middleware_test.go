package agentapi

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
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
	"github.com/yeluonight/skillfleet/migrations"
)

// authFixture sets up a router with one HMAC-guarded route and an
// already-enrolled, approved device. Tests can flip the device status
// or replay nonces to probe individual middleware branches.
type authFixture struct {
	srv          *httptest.Server
	db           *sql.DB
	deviceID     string
	secretPT     string // plaintext as the agent stores it
	hmacKey      string // sha256(secretPT) — what the server stores
	clockNow     time.Time
	maxClockSkew time.Duration
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}

	f := &authFixture{
		db:           d,
		clockNow:     time.Unix(1_700_000_000, 0),
		maxClockSkew: 5 * time.Minute,
	}

	// Enroll + approve in one tx so the device starts in the state the
	// middleware expects to admit.
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := enrollment.Create(ctx, d, time.Hour, f.clockNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Consume(ctx, tx, tok.Plaintext, f.clockNow); err != nil {
		t.Fatal(err)
	}
	res, err := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n", OS: "linux", Arch: "amd64"}, f.clockNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := devices.SetStatus(ctx, d, res.Device.ID, devices.StatusApproved); err != nil {
		t.Fatal(err)
	}
	f.deviceID = res.Device.ID
	f.secretPT = res.Secret
	f.hmacKey = devices.HMACKey(res.Secret)

	deps := Deps{
		DB:           d,
		Now:          func() time.Time { return f.clockNow },
		Audit:        audit.New(d, nil, func() time.Time { return f.clockNow }),
		MaxClockSkew: f.maxClockSkew,
	}
	mux := http.NewServeMux()
	mux.Handle("POST /agent/echo", deps.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "no auth context", http.StatusInternalServerError)
			return
		}
		// Echo the body + observed device_id so tests can assert the
		// body survived the middleware buffer/restore step.
		body, _ := io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_id": ac.DeviceID,
			"echo":      string(body),
		})
	})))
	f.srv = httptest.NewServer(mux)

	t.Cleanup(func() {
		f.srv.Close()
		_ = d.Close()
	})
	return f
}

// signedPost crafts a POST /agent/echo carrying a freshly signed
// request. Caller can mutate the returned http.Request before sending.
func (f *authFixture) signedPost(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", f.clockNow, body); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestAuthenticate_HappyPath(t *testing.T) {
	f := newAuthFixture(t)
	body := []byte(`{"hb":1}`)
	req := f.signedPost(t, body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["device_id"] != f.deviceID {
		t.Errorf("device_id surfaced = %v", got["device_id"])
	}
	if got["echo"] != string(body) {
		t.Errorf("body did not survive: %v", got["echo"])
	}

	// last_seen_at populated for approved device.
	var lastSeen sql.NullInt64
	_ = f.db.QueryRow(`SELECT last_seen_at FROM devices WHERE id = ?`, f.deviceID).Scan(&lastSeen)
	if !lastSeen.Valid || lastSeen.Int64 != f.clockNow.UnixMilli() {
		t.Errorf("last_seen_at = %+v, want %d", lastSeen, f.clockNow.UnixMilli())
	}

	// Nonce was recorded.
	var nonceCount int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM agent_nonces WHERE device_id=?`, f.deviceID).Scan(&nonceCount)
	if nonceCount != 1 {
		t.Errorf("nonce count = %d", nonceCount)
	}
}

func TestAuthenticate_RejectsMissingHeaders(t *testing.T) {
	f := newAuthFixture(t)
	// Plain unsigned POST — middleware must trip on Parse.
	resp, err := http.Post(f.srv.URL+"/agent/echo", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "bad_signature") {
		t.Errorf("body = %s, want bad_signature", raw)
	}
}

func TestAuthenticate_RejectsBadTimestamp(t *testing.T) {
	f := newAuthFixture(t)
	req := f.signedPost(t, nil)
	req.Header.Set(sfhmac.HeaderTimestamp, "not-a-number")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestAuthenticate_RejectsExpiredTimestamp(t *testing.T) {
	f := newAuthFixture(t)
	// Sign with a timestamp 10 minutes in the past — outside the 5-min
	// window the fixture configures.
	body := []byte(`{}`)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	pastTs := f.clockNow.Add(-10 * time.Minute)
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", pastTs, body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "timestamp_out_of_window") {
		t.Errorf("body = %s", raw)
	}
}

func TestAuthenticate_RejectsFutureTimestamp(t *testing.T) {
	f := newAuthFixture(t)
	body := []byte(`{}`)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	futureTs := f.clockNow.Add(10 * time.Minute)
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", futureTs, body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestAuthenticate_RejectsUnknownDevice(t *testing.T) {
	f := newAuthFixture(t)
	body := []byte(`{}`)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	if err := sfhmac.SignRequest(req, "dev_doesnotexist", "any-key", "", f.clockNow, body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "bad_signature") {
		t.Errorf("body = %s, want bad_signature (opaque)", raw)
	}
}

func TestAuthenticate_RejectsPendingDevice(t *testing.T) {
	f := newAuthFixture(t)
	// Flip the device back to revoked (a non-approved status) so the
	// middleware's status gate fires.
	if err := devices.SetStatus(context.Background(), f.db, f.deviceID, devices.StatusRevoked); err != nil {
		t.Fatal(err)
	}
	req := f.signedPost(t, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "device_not_approved") {
		t.Errorf("body = %s, want device_not_approved", raw)
	}

	// Audit row recorded for non-approved.
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='device.unauthorized'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit count = %d, want 1", n)
	}
}

func TestAuthenticate_RejectsTamperedBody(t *testing.T) {
	f := newAuthFixture(t)
	signedBody := []byte(`{"hb":1}`)
	req := f.signedPost(t, signedBody)
	// Replace the body but keep the (now-incorrect) X-SF-Body-SHA256.
	tampered := []byte(`{"hb":2}`)
	req.Body = io.NopCloser(bytes.NewReader(tampered))
	req.ContentLength = int64(len(tampered))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(tampered)), nil }

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "body_mismatch") {
		t.Errorf("body = %s", raw)
	}
}

func TestAuthenticate_RejectsTooLargeBody(t *testing.T) {
	f := newAuthFixture(t)
	// 2 MiB body — over MaxAgentBodyBytes. Sign honestly so the body
	// hash matches; the middleware should bail at the io.ReadAll cap.
	body := bytes.Repeat([]byte("a"), 2<<20)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", f.clockNow, body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestAuthenticate_RejectsReplayedNonce(t *testing.T) {
	f := newAuthFixture(t)
	body := []byte(`{"hb":1}`)
	// First request uses an explicit nonce so we can replay it byte-for-byte.
	req1, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	if err := sfhmac.SignRequest(req1, f.deviceID, f.hmacKey, "fixed-nonce", f.clockNow, body); err != nil {
		t.Fatal(err)
	}
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d", resp1.StatusCode)
	}

	// Re-sign with the SAME nonce + same timestamp = exact replay.
	req2, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	if err := sfhmac.SignRequest(req2, f.deviceID, f.hmacKey, "fixed-nonce", f.clockNow, body); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("replay status = %d, want 401", resp2.StatusCode)
	}
	raw, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(raw), "nonce_replay") {
		t.Errorf("body = %s", raw)
	}
}

func TestAuthenticate_RejectsBadSignature(t *testing.T) {
	f := newAuthFixture(t)
	body := []byte(`{}`)
	req := f.signedPost(t, body)
	// Tamper the signature; everything else is consistent.
	bad := req.Header.Get(sfhmac.HeaderSignature)
	if bad == "" {
		t.Fatal("missing sig header")
	}
	req.Header.Set(sfhmac.HeaderSignature, strings.Repeat("a", len(bad)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "bad_signature") {
		t.Errorf("body = %s", raw)
	}
}

func TestAuthenticate_RejectsWrongSecret(t *testing.T) {
	f := newAuthFixture(t)
	body := []byte(`{}`)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/echo", bytes.NewReader(body))
	// Sign with a key the server doesn't have — should fail at sig.
	if err := sfhmac.SignRequest(req, f.deviceID, "wrong-key", "", f.clockNow, body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestAuthenticate_FromContextOnlyInsideMiddleware(t *testing.T) {
	// Out-of-band sanity: FromContext returns false on a background ctx.
	if _, ok := FromContext(context.Background()); ok {
		t.Error("FromContext claimed an auth context exists on a background ctx")
	}
}

func TestAuthenticate_HMACKeyDerivationMatchesServer(t *testing.T) {
	f := newAuthFixture(t)
	// Pin the contract: the value the agent uses as HMAC key
	// (devices.HMACKey applied to the plaintext from agent.json) MUST
	// equal what the server stores in device_secrets.secret_hash.
	var stored string
	if err := f.db.QueryRow(`SELECT secret_hash FROM device_secrets WHERE device_id=?`, f.deviceID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	derived := devices.HMACKey(f.secretPT)
	if stored != derived {
		t.Fatalf("HMAC key contract broken:\n  stored = %s\n  derived = %s", stored, derived)
	}
}
