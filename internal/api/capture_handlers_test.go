package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yeluonight/skillfleet/internal/registry"
)

// captureBody builds a capture-local request body with one or more files.
func captureBody(files map[string]string, baseVersionID string) map[string]any {
	fs := make([]map[string]string, 0, len(files))
	for path, content := range files {
		fs = append(fs, map[string]string{"path": path, "content": content})
	}
	body := map[string]any{"files": fs}
	if baseVersionID != "" {
		body["base_version_id"] = baseVersionID
	}
	return body
}

// TestCaptureLocal_PublishesLocalEdit: capturing a device's edited tree
// publishes a new version with kind=local_edit and the given base.
func TestCaptureLocal_PublishesLocalEdit(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// The skill exists with an initial manual version; that is the base
	// the local copy was edited from.
	base := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1"))

	body := captureBody(map[string]string{
		"SKILL.md": "---\nname: deploy\ndescription: x\n---\n# deploy LOCAL EDIT\n",
	}, base.ID)
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", body)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got captureLocalResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version.Kind != "local_edit" {
		t.Errorf("kind = %q, want local_edit", got.Version.Kind)
	}
	if got.Version.BaseVersionID != base.ID {
		t.Errorf("base_version_id = %q, want %q", got.Version.BaseVersionID, base.ID)
	}
	if got.Version.ContentSHA256 == "" || got.Version.ContentSHA256 == base.ContentSHA256 {
		t.Errorf("captured sha should differ from base: got %q (base %q)", got.Version.ContentSHA256, base.ContentSHA256)
	}

	// The new version is now in the registry under the same name.
	versions, err := reg.ListByName(context.Background(), "deploy")
	if err != nil {
		t.Fatal(err)
	}
	var foundLocalEdit bool
	for _, v := range versions {
		if v.Kind == registry.KindLocalEdit {
			foundLocalEdit = true
		}
	}
	if !foundLocalEdit {
		t.Error("registry has no local_edit version after capture")
	}
}

// TestCaptureLocal_Idempotent: capturing bytes identical to an existing
// version reuses it (content-addressed dedup) rather than erroring.
func TestCaptureLocal_Idempotent(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1"))

	sameBytes := "---\nname: deploy\ndescription: x\n---\n# deploy LOCAL\n"
	body := captureBody(map[string]string{"SKILL.md": sameBytes}, "")

	first := authedDo(t, sc, cc, newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", body))
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first capture status = %d", first.StatusCode)
	}
	var v1 captureLocalResponse
	json.NewDecoder(first.Body).Decode(&v1)

	second := authedDo(t, sc, cc, newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", body))
	defer second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second capture status = %d", second.StatusCode)
	}
	var v2 captureLocalResponse
	json.NewDecoder(second.Body).Decode(&v2)

	if v1.Version.ID != v2.Version.ID {
		t.Errorf("identical capture must reuse the version: %q vs %q", v1.Version.ID, v2.Version.ID)
	}
}

func TestCaptureLocal_SkillNotFound(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	body := captureBody(map[string]string{"SKILL.md": "---\nname: ghost\n---\n# ghost\n"}, "")
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/ghost/capture-local", body)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestCaptureLocal_BaseFromOtherSkillRejected: a base_version_id that
// belongs to a different skill is rejected (meaningless provenance).
func TestCaptureLocal_BaseFromOtherSkillRejected(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1"))
	other := publishVersion(t, reg, "lint", oneSkillFile("lint", "lint v1"))

	body := captureBody(map[string]string{
		"SKILL.md": "---\nname: deploy\ndescription: x\n---\n# deploy edit\n",
	}, other.ID) // base belongs to "lint", not "deploy"
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", body)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (base from other skill)", resp.StatusCode)
	}
}

func TestCaptureLocal_EmptyFilesRejected(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1"))

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", map[string]any{"files": []any{}})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (empty files)", resp.StatusCode)
	}
}

func TestCaptureLocal_RequiresAuth(t *testing.T) {
	srv, _, _ := newTestServerWithRegistry(t)
	body := captureBody(map[string]string{"SKILL.md": "x"}, "")
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCaptureLocal_RequiresCSRF(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1"))

	// Session cookie present, X-CSRF-Token header omitted.
	body := captureBody(map[string]string{"SKILL.md": "x"}, "")
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy/capture-local", body)
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
