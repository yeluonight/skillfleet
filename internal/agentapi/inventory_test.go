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
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
	"github.com/yeluonight/skillfleet/migrations"
)

// invFixture wires an approved device behind the real NewRouter so the
// full middleware -> handler -> inventory.Store path is under test.
type invFixture struct {
	srv      *httptest.Server
	db       *sql.DB
	deviceID string
	hmacKey  string
	now      time.Time
}

func newInvFixture(t *testing.T) *invFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	f := &invFixture{db: d, now: time.Unix(1_700_000_000, 0)}

	tx, _ := d.BeginTx(ctx, nil)
	tok, _ := enrollment.Create(ctx, d, time.Hour, f.now)
	_, _ = enrollment.Consume(ctx, tx, tok.Plaintext, f.now)
	res, _ := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n"}, f.now)
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

func (f *invFixture) signedInv(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/agent/inventory", bytes.NewReader(body))
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", f.now, body); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestInventory_HappyPath(t *testing.T) {
	f := newInvFixture(t)
	body := []byte(`{
		"agent_version": "0.3.0",
		"tools": [
			{
				"tool_key": "claude-code", "display_name": "Claude Code", "scope": "user",
				"root_id": "claude_user", "root_path": "/home/me/.claude/skills",
				"skills": [
					{"name": "deploy", "skill_path": "/p/deploy", "has_skill_md": true,
					 "description": "deploys", "effective_state": "on", "native_state": "available",
					 "content_sha256": "abc", "file_count": 3, "total_bytes": 100}
				]
			}
		]
	}`)
	req := f.signedInv(t, body)
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
	if got["status"] != "ok" {
		t.Errorf("status = %v", got["status"])
	}
	if got["skill_count"].(float64) != 1 || got["root_count"].(float64) != 1 {
		t.Errorf("counts = %v / %v", got["skill_count"], got["root_count"])
	}

	// DB landed the skill.
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM discovered_skills WHERE device_id=?`, f.deviceID).Scan(&n)
	if n != 1 {
		t.Errorf("discovered_skills count = %d, want 1", n)
	}

	// Audit row.
	var auditN int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='device.inventory'`).Scan(&auditN)
	if auditN != 1 {
		t.Errorf("audit count = %d, want 1", auditN)
	}
}

func TestInventory_EmptyToolsOK(t *testing.T) {
	f := newInvFixture(t)
	req := f.signedInv(t, []byte(`{"tools":[]}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestInventory_InvalidScopeRejected(t *testing.T) {
	f := newInvFixture(t)
	body := []byte(`{"tools":[{"tool_key":"x","display_name":"X","scope":"galaxy","root_id":"r","root_path":"/p","skills":[]}]}`)
	req := f.signedInv(t, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, body=%s, want 400", resp.StatusCode, raw)
	}
}

func TestInventory_UnknownFieldRejected(t *testing.T) {
	f := newInvFixture(t)
	body := []byte(`{"tools":[],"bogus":1}`)
	req := f.signedInv(t, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestInventory_RequiresAuth(t *testing.T) {
	f := newInvFixture(t)
	resp, err := http.Post(f.srv.URL+"/agent/inventory", "application/json", bytes.NewReader([]byte(`{"tools":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestInventory_ReplacesPrior(t *testing.T) {
	f := newInvFixture(t)
	// First upload: 2 skills.
	body1 := []byte(`{"tools":[{"tool_key":"claude-code","display_name":"C","scope":"user","root_id":"r","root_path":"/p","skills":[
		{"name":"a","skill_path":"/p/a","has_skill_md":true,"effective_state":"on"},
		{"name":"b","skill_path":"/p/b","has_skill_md":true,"effective_state":"off"}
	]}]}`)
	req1 := f.signedInv(t, body1)
	resp1, _ := http.DefaultClient.Do(req1)
	resp1.Body.Close()

	// Second upload (different nonce auto-minted): 1 skill.
	body2 := []byte(`{"tools":[{"tool_key":"pi","display_name":"Pi","scope":"user","root_id":"r2","root_path":"/p2","skills":[
		{"name":"only","skill_path":"/p2/only","has_skill_md":true,"effective_state":"on"}
	]}]}`)
	req2 := f.signedInv(t, body2)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("second upload status = %d, body=%s", resp2.StatusCode, raw)
	}

	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM discovered_skills WHERE device_id=?`, f.deviceID).Scan(&n)
	if n != 1 {
		t.Errorf("after replace, discovered_skills = %d, want 1", n)
	}
}
