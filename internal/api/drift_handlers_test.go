package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/inventory"
	"github.com/yeluonight/skillfleet/internal/registry"
)

// seedDevice inserts an approved device row directly so the drift tests
// don't need the full enroll dance (that path is covered in
// device_handlers_test). The drift endpoint only needs the device to exist.
func seedDevice(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO devices(id, name, status, created_at) VALUES (?, ?, 'approved', ?)`,
		id, id, time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
}

// oneSkillFile builds a single SKILL.md whose body varies by `body`, so
// two calls with different bodies publish versions with different
// content_sha256 (and the same body is idempotent).
func oneSkillFile(name, body string) []registry.InMemoryFile {
	return []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: " + name + "\ndescription: x\n---\n# " + body + "\n")},
	}
}

// landRun stores an inventory run for a device with the given skills, so
// ComputeDeviceDrift has discovered_skills to classify.
func landRun(t *testing.T, db *sql.DB, deviceID string, skills []inventory.Skill) {
	t.Helper()
	report := inventory.Report{
		AgentVersion: "0.7.0",
		Tools: []inventory.ToolInstance{{
			ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
			RootID: "claude_user", RootPath: "/home/me/.claude/skills",
			Skills: skills,
		}},
	}
	if _, err := inventory.Store(context.Background(), db, deviceID, report, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceDrift_ThreeStates(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	cleanSHA := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1")).ContentSHA256
	publishVersion(t, reg, "lint", oneSkillFile("lint", "lint v1")) // tracked name, device sha will differ
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: cleanSHA},
		{Name: "lint", SkillPath: "/s/lint", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-edited"},
		{Name: "scratch", SkillPath: "/s/scratch", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-x"},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/dev1/drift", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var got deviceDriftResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Summary.Clean != 1 || got.Summary.LocalModified != 1 || got.Summary.Untracked != 1 {
		t.Fatalf("summary = %+v, want clean=1 modified=1 untracked=1", got.Summary)
	}

	byName := map[string]driftSkillView{}
	for _, s := range got.Skills {
		byName[s.Name] = s
	}
	if s := byName["deploy"]; s.LocalState != "clean" || s.MatchedVersionID == "" {
		t.Errorf("deploy: want clean + matched id, got %q/%q", s.LocalState, s.MatchedVersionID)
	}
	if s := byName["lint"]; s.LocalState != "local_modified" {
		t.Errorf("lint: want local_modified, got %q", s.LocalState)
	}
	if s := byName["scratch"]; s.LocalState != "untracked" {
		t.Errorf("scratch: want untracked, got %q", s.LocalState)
	}
}

// TestDeviceDrift_ShaMatchEqualsClean is the HTTP-layer view of the core
// guard: a device whose reported content_sha256 equals a registry
// version's sha classifies clean — never local_modified — even though the
// registry holds an older version of the same skill too.
func TestDeviceDrift_ShaMatchEqualsClean(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v0")) // older version
	runningSHA := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1")).ContentSHA256 // the one the device runs
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: runningSHA},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/dev1/drift", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var got deviceDriftResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(got.Skills))
	}
	if got.Skills[0].LocalState != "clean" {
		t.Errorf("sha match must be clean, got %q", got.Skills[0].LocalState)
	}
	if got.Summary.LocalModified != 0 {
		t.Errorf("a matching sha must not be counted local_modified, summary=%+v", got.Summary)
	}
}

func TestDeviceDrift_DeviceNotFound(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/ghost/drift", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeviceDrift_NoInventoryIsEmpty(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedDevice(t, d, "dev1")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/dev1/drift", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got deviceDriftResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 0 {
		t.Errorf("device never scanned: want empty skills, got %d", len(got.Skills))
	}
}

func TestDeviceDrift_RequiresAuth(t *testing.T) {
	srv, _, _ := newTestServerWithRegistry(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/dev1/drift", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
