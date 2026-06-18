package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yeluonight/skillfleet/internal/deploy"
)

func TestRegisterDeviceRoot_CreatesJob(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")
	storeSampleInventory(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots", map[string]string{
		"tool_key": "codex",
		"scope":    "user",
		"path":     "/h/.agents/skills",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var job deploymentJobView
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.Operation != "register_root" || job.Status != "pending" || job.DeviceID != "dev1" {
		t.Fatalf("job = %+v", job)
	}

	stored := loadRootJobRequest(t, d, job.ID)
	if stored.Operation != deploy.OpRegisterRoot || stored.RootPath != "/h/.agents/skills" {
		t.Fatalf("request = %+v", stored)
	}
	if stored.Target.ToolKey != "codex" || stored.Target.Scope != "user" || stored.Target.RootID != "" {
		t.Errorf("target = %+v", stored.Target)
	}
	if stored.RequestedBy == "" {
		t.Error("requested_by is empty")
	}

	assertAuditCount(t, d, "device.root_register_requested", job.ID, 1)
}

func TestRegisterDeviceRoot_AllowsCustomPathJob(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")
	storeSampleInventory(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots", map[string]any{
		"tool_key": "claude-code",
		"scope":    "user",
		"path":     "/h/custom/skills",
		"custom":   true,
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var job deploymentJobView
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	stored := loadRootJobRequest(t, d, job.ID)
	if stored.Operation != deploy.OpRegisterRoot || stored.RootPath != "/h/custom/skills" {
		t.Fatalf("request = %+v", stored)
	}
}

func TestRegisterDeviceRoot_RejectsNonCandidate(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")
	storeSampleInventory(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots", map[string]string{
		"tool_key": "codex",
		"scope":    "user",
		"path":     "/etc/evil",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "root_not_a_candidate" {
		t.Errorf("error = %q, want root_not_a_candidate", got)
	}
	assertDeploymentJobCount(t, d, "dev1", 0)
}

func TestRemoveDeviceRoot_CreatesJob(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")
	storeSampleInventory(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots/claude_user/remove", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var job deploymentJobView
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.Operation != "remove_root" || job.Status != "pending" || job.DeviceID != "dev1" {
		t.Fatalf("job = %+v", job)
	}

	stored := loadRootJobRequest(t, d, job.ID)
	if stored.Operation != deploy.OpRemoveRoot || stored.Target.RootID != "claude_user" || stored.RootPath != "" {
		t.Fatalf("request = %+v", stored)
	}
	assertAuditCount(t, d, "device.root_remove_requested", job.ID, 1)
}

func TestRemoveDeviceRoot_RejectsUnknownRootID(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")
	storeSampleInventory(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots/codex_user/remove", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "root_not_registered" {
		t.Errorf("error = %q, want root_not_registered", got)
	}
	assertDeploymentJobCount(t, d, "dev1", 0)
}

func TestRegisterDeviceRoot_RequiresAuthAndCSRF(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")
	storeSampleInventory(t, d, "dev1")
	body := map[string]string{"tool_key": "codex", "scope": "user", "path": "/h/.agents/skills"}

	unauth := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots", body)
	unauthResp, err := http.DefaultClient.Do(unauth)
	if err != nil {
		t.Fatal(err)
	}
	defer unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", unauthResp.StatusCode)
	}

	noCSRF := newJSONReq(t, http.MethodPost, srv.URL+"/api/devices/dev1/roots", body)
	noCSRF.AddCookie(sc)
	noCSRF.AddCookie(cc)
	csrfResp, err := http.DefaultClient.Do(noCSRF)
	if err != nil {
		t.Fatal(err)
	}
	defer csrfResp.Body.Close()
	if csrfResp.StatusCode != http.StatusForbidden {
		t.Fatalf("no-CSRF status = %d, want 403", csrfResp.StatusCode)
	}
}

func loadRootJobRequest(t *testing.T, d *sql.DB, jobID string) deploy.Request {
	t.Helper()
	var raw string
	if err := d.QueryRowContext(context.Background(), `SELECT request_json FROM deployment_jobs WHERE id = ?`, jobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var req deploy.Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func assertDeploymentJobCount(t *testing.T, d *sql.DB, deviceID string, want int) {
	t.Helper()
	var got int
	if err := d.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM deployment_jobs WHERE device_id = ?`, deviceID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("deployment job count = %d, want %d", got, want)
	}
}

func assertAuditCount(t *testing.T, d *sql.DB, action, targetID string, want int) {
	t.Helper()
	var got int
	if err := d.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_logs WHERE action = ? AND target_id = ?`, action, targetID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("audit count = %d, want %d", got, want)
	}
}
