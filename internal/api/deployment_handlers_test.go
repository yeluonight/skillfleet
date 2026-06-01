package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
)

func TestDeployPlan_BuildsPlan(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "deploy-helper", oneSkillFile("deploy-helper", "deploy helper v1"))

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/plan", deployTestBody("deploy-helper", v.ID, ""))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got planResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Plan.VersionID != v.ID {
		t.Errorf("plan.version_id = %q, want %q", got.Plan.VersionID, v.ID)
	}
	if got.Plan.SkillName != "deploy-helper" {
		t.Errorf("plan.skill_name = %q, want deploy-helper", got.Plan.SkillName)
	}
	if len(got.Plan.Files) == 0 {
		t.Error("plan.files is empty")
	}
	if got.Plan.ContentSHA256 == "" {
		t.Error("plan.content_sha256 is empty")
	}
}

func TestDeployPlan_UnknownVersion(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/plan", map[string]string{"version_id": "sv_ghost"})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "version_not_found" {
		t.Errorf("error = %q, want version_not_found", got)
	}
}

func TestDeployPlan_MissingVersionID(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/plan", map[string]string{"skill_name": "x"})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDeployExecute_CreatesJob(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "deploy-helper", oneSkillFile("deploy-helper", "deploy helper v1"))
	seedDeployTestDevice(t, d, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/execute", deployTestBody("deploy-helper", v.ID, "dev1"))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var job deploymentJobView
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" {
		t.Error("id is empty")
	}
	if job.Operation != "install" {
		t.Errorf("operation = %q, want install", job.Operation)
	}
	if job.Status != "pending" {
		t.Errorf("status = %q, want pending", job.Status)
	}
	if job.DeviceID != "dev1" {
		t.Errorf("device_id = %q, want dev1", job.DeviceID)
	}
	if job.SkillName != "deploy-helper" {
		t.Errorf("skill_name = %q, want deploy-helper", job.SkillName)
	}

	listReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/deployments?device=dev1", nil)
	listResp := authedDo(t, sc, cc, listReq)
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	jobs := decodeDeployJobs(t, listResp)
	if !hasDeployJob(jobs, job.ID) {
		t.Fatalf("list jobs = %+v, want job %q", jobs, job.ID)
	}
}

func TestDeployExecute_RequiresDeviceID(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "deploy-helper", oneSkillFile("deploy-helper", "deploy helper v1"))

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/execute", deployTestBody("deploy-helper", v.ID, ""))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDeployExecute_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/execute", map[string]string{
		"version_id": "sv_any",
		"device_id":  "dev1",
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDeployExecute_RequiresCSRF(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "deploy-helper", oneSkillFile("deploy-helper", "deploy helper v1"))
	seedDeployTestDevice(t, d, "dev1")

	// Session and csrf cookies are present, but X-CSRF-Token is deliberately omitted.
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/execute", deployTestBody("deploy-helper", v.ID, "dev1"))
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

func TestDeployList_FiltersByDevice(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "deploy-helper", oneSkillFile("deploy-helper", "deploy helper v1"))
	seedDeployTestDevice(t, d, "dev1")
	seedDeployTestDevice(t, d, "dev2")

	job1 := executeDeployTestJob(t, srv.URL, sc, cc, "deploy-helper", v.ID, "dev1")
	job2 := executeDeployTestJob(t, srv.URL, sc, cc, "deploy-helper", v.ID, "dev2")

	listReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/deployments?device=dev1", nil)
	listResp := authedDo(t, sc, cc, listReq)
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	jobs := decodeDeployJobs(t, listResp)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1: %+v", len(jobs), jobs)
	}
	if jobs[0].ID != job1.ID || jobs[0].DeviceID != "dev1" {
		t.Errorf("filtered job = %+v, want id %q on dev1", jobs[0], job1.ID)
	}
	if jobs[0].ID == job2.ID || jobs[0].DeviceID == "dev2" {
		t.Errorf("dev2 job leaked into dev1 filter: %+v", jobs[0])
	}
}

func TestDeployRollback_RejectsNonSucceeded(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "deploy-helper", oneSkillFile("deploy-helper", "deploy helper v1"))
	seedDeployTestDevice(t, d, "dev1")
	job := executeDeployTestJob(t, srv.URL, sc, cc, "deploy-helper", v.ID, "dev1")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/"+job.ID+"/rollback", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "not_rollbackable" {
		t.Errorf("error = %q, want not_rollbackable", got)
	}
}

func TestDeployRollback_UnknownJob(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/deployments/dj_ghost/rollback", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := decodeDeployError(t, resp); got != "not_found" {
		t.Errorf("error = %q, want not_found", got)
	}
}

func deployTestBody(skillName, versionID, deviceID string) map[string]string {
	body := map[string]string{
		"skill_name": skillName,
		"version_id": versionID,
		"tool_key":   "claude-code",
		"scope":      "user",
	}
	if deviceID != "" {
		body["device_id"] = deviceID
	}
	return body
}

func seedDeployTestDevice(t *testing.T, d *sql.DB, id string) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO devices(id, name, status, created_at) VALUES(?, ?, 'approved', 1)`, id, id); err != nil {
		t.Fatal(err)
	}
}

func executeDeployTestJob(t *testing.T, baseURL string, sc, cc *http.Cookie, skillName, versionID, deviceID string) deploymentJobView {
	t.Helper()
	req := newJSONReq(t, http.MethodPost, baseURL+"/api/deployments/execute", deployTestBody(skillName, versionID, deviceID))
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("execute status = %d, want 201", resp.StatusCode)
	}
	var job deploymentJobView
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	return job
}

func decodeDeployJobs(t *testing.T, resp *http.Response) []deploymentJobView {
	t.Helper()
	var got struct {
		Jobs []deploymentJobView `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got.Jobs
}

func decodeDeployError(t *testing.T, resp *http.Response) string {
	t.Helper()
	var got struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got.Error
}

func hasDeployJob(jobs []deploymentJobView, id string) bool {
	for _, job := range jobs {
		if job.ID == id {
			return true
		}
	}
	return false
}
