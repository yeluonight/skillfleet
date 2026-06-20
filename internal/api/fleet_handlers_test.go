package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/inventory"
)

// fleetStatusOf fetches GET /api/skills/{name}/fleet-status and decodes it.
func fleetStatusOf(t *testing.T, srv *httptest.Server, sc, cc *http.Cookie, name string) fleetStatusResponse {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/"+name+"/fleet-status", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got fleetStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

// landRunAt stores an inventory run with an explicit timestamp so tests can
// order multiple runs per device (the handler reads only the newest run).
func landRunAt(t *testing.T, db *sql.DB, deviceID string, skills []inventory.Skill, at time.Time) {
	t.Helper()
	report := inventory.Report{
		AgentVersion: "0.7.0",
		Tools: []inventory.ToolInstance{{
			ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
			RootID: "claude_user", RootPath: "/home/me/.claude/skills",
			Skills: skills,
		}},
	}
	if _, err := inventory.Store(context.Background(), db, deviceID, report, at); err != nil {
		t.Fatal(err)
	}
}

// landRunWithRootsAt is landRunAt plus a Roots slice, so a test can seed a
// run whose candidate-root report carries registered roots (used by the
// fleet-status not_deployed synthesis pass).
func landRunWithRootsAt(t *testing.T, db *sql.DB, deviceID string, skills []inventory.Skill, roots []inventory.RootCandidate, at time.Time) {
	t.Helper()
	report := inventory.Report{
		AgentVersion: "0.7.0",
		Tools: []inventory.ToolInstance{{
			ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
			RootID: "claude_user", RootPath: "/home/me/.claude/skills",
			Skills: skills,
		}},
		Roots: roots,
	}
	if _, err := inventory.Store(context.Background(), db, deviceID, report, at); err != nil {
		t.Fatal(err)
	}
}

// TestSkillFleetStatus_CleanAndLocalModifiedAcrossDevices: one published
// skill deployed on two devices reporting distinct content SHAs — one
// matching a registry version (clean), one differing (local_modified).
func TestSkillFleetStatus_CleanAndLocalModifiedAcrossDevices(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	cleanSHA := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1")).ContentSHA256

	seedDevice(t, d, "devA")
	landRun(t, d, "devA", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: cleanSHA},
	})
	seedDevice(t, d, "devB")
	landRun(t, d, "devB", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-edited"},
	})

	got := fleetStatusOf(t, srv, sc, cc, "deploy")
	if got.SkillName != "deploy" {
		t.Errorf("skill_name = %q", got.SkillName)
	}
	if len(got.Deployments) != 2 {
		t.Fatalf("deployments = %d, want 2", len(got.Deployments))
	}
	states := map[string]string{}
	matched := map[string]string{}
	for _, row := range got.Deployments {
		states[row.DeviceName] = row.LocalState
		matched[row.DeviceName] = row.MatchedVersionID
		if row.RegistryVersionCount != 1 {
			t.Errorf("%s registry_version_count = %d, want 1", row.DeviceName, row.RegistryVersionCount)
		}
	}
	if states["devA"] != "clean" {
		t.Errorf("devA state = %q, want clean", states["devA"])
	}
	if matched["devA"] == "" {
		t.Errorf("devA clean row must carry matched_version_id")
	}
	if states["devB"] != "local_modified" {
		t.Errorf("devB state = %q, want local_modified", states["devB"])
	}
	if matched["devB"] != "" {
		t.Errorf("devB local_modified row must not carry matched_version_id, got %q", matched["devB"])
	}
}

// TestSkillFleetStatus_Untracked: a skill present on a device but never
// published to the registry classifies untracked.
func TestSkillFleetStatus_Untracked(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	seedDevice(t, d, "devA")
	landRun(t, d, "devA", []inventory.Skill{
		{Name: "scratch", SkillPath: "/s/scratch", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-x"},
	})

	got := fleetStatusOf(t, srv, sc, cc, "scratch")
	if len(got.Deployments) != 1 {
		t.Fatalf("deployments = %d, want 1", len(got.Deployments))
	}
	if got.Deployments[0].LocalState != "untracked" {
		t.Errorf("state = %q, want untracked", got.Deployments[0].LocalState)
	}
	if got.Deployments[0].RegistryVersionCount != 0 {
		t.Errorf("registry_version_count = %d, want 0", got.Deployments[0].RegistryVersionCount)
	}
}

// TestSkillFleetStatus_OnlyLatestRunCounts: only the newest run per device
// is read — a skill removed in the latest run disappears from the result.
func TestSkillFleetStatus_OnlyLatestRunCounts(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	sha := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1")).ContentSHA256
	seedDevice(t, d, "devA")
	landRunAt(t, d, "devA", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: sha},
	}, time.Now().Add(-time.Hour))
	landRunAt(t, d, "devA", []inventory.Skill{
		{Name: "other", SkillPath: "/s/other", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "z"},
	}, time.Now())

	got := fleetStatusOf(t, srv, sc, cc, "deploy")
	if len(got.Deployments) != 0 {
		t.Fatalf("deployments = %d, want 0 (skill gone in latest run)", len(got.Deployments))
	}
}

func TestSkillFleetStatus_NoDeployments_EmptyArray(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	got := fleetStatusOf(t, srv, sc, cc, "ghost")
	if got.Deployments == nil {
		t.Fatal("deployments must be a non-nil empty array")
	}
	if len(got.Deployments) != 0 {
		t.Fatalf("deployments = %d, want 0", len(got.Deployments))
	}
}

func TestSkillFleetStatus_RequiresAuth(t *testing.T) {
	srv, _, _ := newTestServerWithRegistry(t)
	resp, err := http.Get(srv.URL + "/api/skills/deploy/fleet-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestSkillFleetStatus_SynthesizesNotDeployedForRegisteredRoot: a device
// has the skill installed under its claude root (a real discovered row ⇒
// clean) AND a second REGISTERED root (codex) the skill was never installed
// to. The handler must synthesize a not_deployed row for the codex root so
// the detail page can show every deployable path + offer a deploy.
func TestSkillFleetStatus_SynthesizesNotDeployedForRegisteredRoot(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	cleanSHA := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1")).ContentSHA256

	seedDevice(t, d, "devA")
	landRunWithRootsAt(t, d, "devA", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: cleanSHA},
	}, []inventory.RootCandidate{
		// Same path as the ToolInstance above ⇒ already covered, must NOT
		// synthesize a duplicate claude row.
		{ToolKey: "claude-code", Scope: "user", Path: "/home/me/.claude/skills", Registered: true, RootID: "claude_user", Exists: true},
		// Registered root with no discovered deploy row ⇒ synthesize not_deployed.
		{ToolKey: "codex", Scope: "user", Path: "/home/me/.codex/skills", Registered: true, RootID: "codex_user_codex", Exists: true},
	}, time.Now())

	got := fleetStatusOf(t, srv, sc, cc, "deploy")
	if len(got.Deployments) != 2 {
		t.Fatalf("deployments = %d, want 2 (clean + not_deployed): %+v", len(got.Deployments), got.Deployments)
	}
	byPath := map[string]fleetStatusRow{}
	for _, row := range got.Deployments {
		byPath[row.RootPath] = row
		if row.RegistryVersionCount != 1 {
			t.Errorf("%s registry_version_count = %d, want 1", row.RootPath, row.RegistryVersionCount)
		}
	}
	clean := byPath["/home/me/.claude/skills"]
	if clean.LocalState != "clean" {
		t.Errorf("claude row state = %q, want clean", clean.LocalState)
	}
	if clean.ToolKey != "claude-code" {
		t.Errorf("claude row tool = %q, want claude-code", clean.ToolKey)
	}
	nd := byPath["/home/me/.codex/skills"]
	if nd.LocalState != "not_deployed" {
		t.Errorf("codex row state = %q, want not_deployed", nd.LocalState)
	}
	if nd.ToolKey != "codex" {
		t.Errorf("codex row tool = %q, want codex", nd.ToolKey)
	}
	if nd.EffectiveState != "unknown" {
		t.Errorf("codex row effective_state = %q, want unknown", nd.EffectiveState)
	}
	if nd.MatchedVersionID != "" {
		t.Errorf("not_deployed row must not carry matched_version_id, got %q", nd.MatchedVersionID)
	}
}

// TestSkillFleetStatus_NotDeployedOmitsUnregisteredRoots: a candidate root
// that is NOT registered must NOT get a synthesized row — the operator has
// not adopted that path, so there is nothing to deploy to.
func TestSkillFleetStatus_NotDeployedOmitsUnregisteredRoots(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	cleanSHA := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1")).ContentSHA256

	seedDevice(t, d, "devA")
	landRunWithRootsAt(t, d, "devA", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: cleanSHA},
	}, []inventory.RootCandidate{
		{ToolKey: "claude-code", Scope: "user", Path: "/home/me/.claude/skills", Registered: true, RootID: "claude_user", Exists: true},
		// Candidate but NOT registered ⇒ no synthesis.
		{ToolKey: "codex", Scope: "user", Path: "/home/me/.codex/skills", Registered: false, Exists: true},
	}, time.Now())

	got := fleetStatusOf(t, srv, sc, cc, "deploy")
	if len(got.Deployments) != 1 {
		t.Fatalf("deployments = %d, want 1 (clean only; unregistered root must not synthesize): %+v", len(got.Deployments), got.Deployments)
	}
	if got.Deployments[0].LocalState != "clean" {
		t.Errorf("state = %q, want clean", got.Deployments[0].LocalState)
	}
}
