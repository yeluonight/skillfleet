package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// stateChangeTestBody builds the POST body for /api/deployments/state-change.
func stateChangeTestBody(skill, toolKey, scope, deviceID, desired string) map[string]string {
	body := map[string]string{
		"skill_name":    skill,
		"tool_key":      toolKey,
		"scope":         scope,
		"desired_state": desired,
	}
	if deviceID != "" {
		body["device_id"] = deviceID
	}
	return body
}

// TestStateChange_CreatesJob: a valid claude-code off request creates a
// pending state_change job for the device.
func TestStateChange_CreatesJob(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("deploy", "claude-code", "user", "dev1", "off"))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var job deploymentJobView
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.Operation != "state_change" {
		t.Errorf("operation = %q, want state_change", job.Operation)
	}
	if job.Status != "pending" {
		t.Errorf("status = %q, want pending", job.Status)
	}
	if job.DeviceID != "dev1" {
		t.Errorf("device_id = %q, want dev1", job.DeviceID)
	}
	if job.SkillName != "deploy" {
		t.Errorf("skill_name = %q, want deploy", job.SkillName)
	}
}

// TestStateChange_RejectsUnsupportedState: codex cannot be "ask" — the
// planner rejects it 422 before any job is minted.
func TestStateChange_RejectsUnsupportedState(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("deploy", "codex", "user", "dev1", "ask"))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "unsupported_state" {
		t.Errorf("error = %q, want unsupported_state", got)
	}

	// And no job was created.
	listReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/deployments?device=dev1", nil)
	listResp := authedDo(t, sc, cc, listReq)
	defer listResp.Body.Close()
	if jobs := decodeDeployJobs(t, listResp); len(jobs) != 0 {
		t.Errorf("a rejected state change still created %d job(s)", len(jobs))
	}
}

// TestStateChange_RejectsUnsupportedTool: antigravity has no state-change
// support at all → 422.
func TestStateChange_RejectsUnsupportedTool(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("deploy", "antigravity", "user", "dev1", "off"))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "unsupported_state" {
		t.Errorf("error = %q, want unsupported_state", got)
	}
}

func TestStateChange_RequiresSkillName(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("", "claude-code", "user", "dev1", "off"))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStateChange_RequiresDeviceID(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("deploy", "claude-code", "user", "", "off"))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStateChange_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("deploy", "claude-code", "user", "dev1", "off"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestStateChange_RequiresCSRF: session + csrf cookies present but the
// X-CSRF-Token header omitted → 403. Guards the write behind CSRF.
func TestStateChange_RequiresCSRF(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDeployTestDevice(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/state-change",
		stateChangeTestBody("deploy", "claude-code", "user", "dev1", "off"))
	req.AddCookie(sc)
	req.AddCookie(cc)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (CSRF)", resp.StatusCode)
	}
}
