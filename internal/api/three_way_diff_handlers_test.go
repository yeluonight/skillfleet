package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeluonight/skillfleet/internal/inventory"
	"github.com/yeluonight/skillfleet/internal/source"
)

// stageAndCheck primes the fetcher with a changed upstream tree at a new
// commit and runs a check, publishing the pending upstream version a
// three-way diff compares the baseline against. Mirrors the diff tests.
func stageAndCheck(t *testing.T, srv *httptest.Server, sc, cc *http.Cookie, fetcher *fakeFetcher, name string, files []source.FetchedFile, commit string) {
	t.Helper()
	stageUpstreamUpdate(t, files, fetcher, commit)
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/"+name+"/check-updates", nil)
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check status = %d, want 200", resp.StatusCode)
	}
}

func TestThreeWayDiff_NotBound(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "lonely")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/lonely/three-way-diff", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not bound)", resp.StatusCode)
	}
}

// TestThreeWayDiff_NoPendingLocalVsBase: only the baseline upstream
// exists (no pending), but the device reports a local sha. The response
// has_remote_update=false, yet still reports local vs base.
func TestThreeWayDiff_NoPendingLocalVsBase(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	baseSHA := bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Device runs an edited copy (sha differs from baseline).
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "deploy-helper", SkillPath: "/s/deploy-helper", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-local-edit"},
	})

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/skills/deploy-helper/three-way-diff?device_id=dev1&tool_key=claude-code&scope=user", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got threeWayDiffResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.HasRemoteUpdate {
		t.Errorf("has_remote_update = true, want false (baseline only)")
	}
	if got.Local == nil {
		t.Fatal("local side missing")
	}
	if got.Local.ContentAvailable {
		t.Error("Phase 7 local content must be unavailable (sha-only)")
	}
	if got.Local.SHA != "sha-local-edit" {
		t.Errorf("local sha = %q, want sha-local-edit", got.Local.SHA)
	}
	if got.Local.VsBase != "different" {
		t.Errorf("vs_base = %q, want different (edited copy vs baseline %s)", got.Local.VsBase, baseSHA[:8])
	}
}

// TestThreeWayDiff_BaseVsRemoteLineLevel: with a pending update, base and
// remote (both in the registry) diff at file level, and the local side's
// sha is compared to both.
func TestThreeWayDiff_BaseVsRemoteLineLevel(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	baseSHA := bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Pending: SKILL.md body changes, run.sh stays identical.
	changed := []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: deploy-helper\ndescription: upstream skill\n---\n\n# deploy-helper\n\nNEW body\n")},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
	stageAndCheck(t, srv, sc, cc, fetcher, "deploy-helper", changed, "commit2")

	// Device still runs the baseline bytes → local matches base, not remote.
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "deploy-helper", SkillPath: "/s/deploy-helper", HasSkillMD: true, EffectiveState: "on", ContentSHA256: baseSHA},
	})

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/skills/deploy-helper/three-way-diff?device_id=dev1&tool_key=claude-code&scope=user", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got threeWayDiffResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.HasRemoteUpdate {
		t.Fatal("has_remote_update = false, want true")
	}
	if got.BaseVersionID == "" || got.RemoteVersionID == "" || got.BaseVersionID == got.RemoteVersionID {
		t.Errorf("base/remote ids wrong: base=%q remote=%q", got.BaseVersionID, got.RemoteVersionID)
	}
	// base vs remote: only SKILL.md changed (line-level content present).
	if len(got.Files) != 1 {
		t.Fatalf("changed files = %d, want 1 (SKILL.md): %+v", len(got.Files), got.Files)
	}
	skillMd := findDiffFile(t, got.Files, "SKILL.md")
	if skillMd.Status != diffModified || !skillMd.Editable {
		t.Errorf("SKILL.md = %q editable=%v, want modified+editable", skillMd.Status, skillMd.Editable)
	}
	if skillMd.BaseContent == "" || skillMd.TargetContent == "" || skillMd.BaseContent == skillMd.TargetContent {
		t.Error("modified file must carry both distinct sides' content")
	}
	if got.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1 (run.sh)", got.Unchanged)
	}
	// Local matches the baseline bytes, differs from the pending remote.
	if got.Local == nil || got.Local.VsBase != "same" || got.Local.VsRemote != "different" {
		t.Errorf("local standing wrong: %+v", got.Local)
	}
}

// TestThreeWayDiff_NoDeviceParamOmitsLocal: without device_id the local
// side is nil — base-vs-remote only.
func TestThreeWayDiff_NoDeviceParamOmitsLocal(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")
	changed := []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: deploy-helper\ndescription: upstream skill\n---\n\n# deploy-helper\n\nNEW body\n")},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
	stageAndCheck(t, srv, sc, cc, fetcher, "deploy-helper", changed, "commit2")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/deploy-helper/three-way-diff", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got threeWayDiffResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.HasRemoteUpdate {
		t.Fatal("has_remote_update = false, want true")
	}
	if got.Local != nil {
		t.Errorf("local side should be nil without device_id, got %+v", got.Local)
	}
}

func TestThreeWayDiff_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/x/three-way-diff", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
