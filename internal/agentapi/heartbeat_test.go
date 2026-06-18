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

// heartbeatFixture wires up an approved device behind the real
// NewRouter so the full middleware → handler chain is under test.
type heartbeatFixture struct {
	srv      *httptest.Server
	db       *sql.DB
	deviceID string
	hmacKey  string
	now      time.Time
}

func newHeartbeatFixture(t *testing.T) *heartbeatFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "hb.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}

	f := &heartbeatFixture{db: d, now: time.Unix(1_700_000_000, 0)}

	tx, _ := d.BeginTx(ctx, nil)
	tok, _ := enrollment.Create(ctx, d, time.Hour, f.now)
	_, _ = enrollment.Consume(ctx, tx, tok.Plaintext, f.now)
	res, _ := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n", AgentVersion: "0.1.0"}, f.now)
	_ = tx.Commit()
	_ = devices.SetStatus(ctx, d, res.Device.ID, devices.StatusApproved)

	f.deviceID = res.Device.ID
	f.hmacKey = devices.HMACKey(res.Secret)

	router := NewRouter(Deps{
		DB:           d,
		Now:          func() time.Time { return f.now },
		Audit:        audit.New(d, nil, func() time.Time { return f.now }),
		MaxClockSkew: 5 * time.Minute,
	})
	f.srv = httptest.NewServer(router)
	t.Cleanup(func() {
		f.srv.Close()
		_ = d.Close()
	})
	return f
}

func (f *heartbeatFixture) signedHB(t *testing.T, body []byte, contentType string) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/heartbeat", rdr)
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", f.now, body); err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

func TestHeartbeat_HappyPath(t *testing.T) {
	f := newHeartbeatFixture(t)
	req := f.signedHB(t, []byte(`{"agent_version":"0.1.0"}`), "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "ok" {
		t.Errorf("status field = %q", got["status"])
	}

	var lastSeen sql.NullInt64
	_ = f.db.QueryRow(`SELECT last_seen_at FROM devices WHERE id = ?`, f.deviceID).Scan(&lastSeen)
	if !lastSeen.Valid {
		t.Error("last_seen_at not set by middleware")
	}
}

func TestHeartbeat_AcceptsEmptyBody(t *testing.T) {
	f := newHeartbeatFixture(t)
	req := f.signedHB(t, nil, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestHeartbeat_UpdatesAgentVersionOnChange(t *testing.T) {
	f := newHeartbeatFixture(t)
	req := f.signedHB(t, []byte(`{"agent_version":"0.2.0-rc.1"}`), "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	var ver sql.NullString
	_ = f.db.QueryRow(`SELECT agent_version FROM devices WHERE id = ?`, f.deviceID).Scan(&ver)
	if !ver.Valid || ver.String != "0.2.0-rc.1" {
		t.Errorf("agent_version = %+v, want 0.2.0-rc.1", ver)
	}
}

func TestHeartbeat_RejectsUnknownField(t *testing.T) {
	f := newHeartbeatFixture(t)
	req := f.signedHB(t, []byte(`{"agent_version":"0.1.0","bogus":1}`), "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestHeartbeat_RejectsBadContentType(t *testing.T) {
	f := newHeartbeatFixture(t)
	req := f.signedHB(t, []byte(`{}`), "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestHeartbeat_RequiresAuth(t *testing.T) {
	f := newHeartbeatFixture(t)
	resp, err := http.Post(f.srv.URL+"/agent/heartbeat", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
