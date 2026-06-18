package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

// landRunForDevice stores an inventory run with the given skills for an
// arbitrary device id (drift_handlers_test.go's landRun is hardwired to one
// device). It lets the dashboard test seed untracked content on a specific
// device.
func landRunForDevice(t *testing.T, db *sql.DB, deviceID string, skills []inventory.Skill) {
	t.Helper()
	report := inventory.Report{
		AgentVersion: "0.13.0",
		Tools: []inventory.ToolInstance{{
			ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
			RootID: "claude_user", RootPath: "/home/me/.claude/skills",
			Skills: skills,
		}},
	}
	if _, err := inventory.Store(context.Background(), db, deviceID, report, time.Now()); err != nil {
		t.Fatalf("store inventory for %s: %v", deviceID, err)
	}
}

// TestDashboard_Aggregates seeds two devices (one online, one pending), two
// registry skills, and a device inventory carrying one untracked skill, then
// asserts the six headline metrics and the action-item list.
func TestDashboard_Aggregates(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Two managed skills in the registry.
	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "deploy v1"))
	publishVersion(t, reg, "lint", oneSkillFile("lint", "lint v1"))

	// One approved device, seen just now → online. One pending → not online,
	// but counted as a pending action item.
	now := time.Now()
	onlineID := enrollDeviceViaDBAt(t, d, "online-box", devices.StatusApproved, now)
	if err := devices.TouchLastSeen(context.Background(), d, onlineID, now); err != nil {
		t.Fatal(err)
	}
	enrollDeviceViaDBAt(t, d, "pending-box", devices.StatusPending, now)

	// The online device reports one untracked skill (registry has no version
	// named "scratch"), contributing to untracked + high_risk.
	landRunForDevice(t, d, onlineID, []inventory.Skill{
		{Name: "scratch", SkillPath: "/s/scratch", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-x"},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/dashboard", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got dashboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	m := got.Metrics
	if m.OnlineDevices != 1 {
		t.Errorf("online_devices = %d, want 1", m.OnlineDevices)
	}
	if m.PendingDevices != 1 {
		t.Errorf("pending_devices = %d, want 1", m.PendingDevices)
	}
	if m.ManagedSkills != 2 {
		t.Errorf("managed_skills = %d, want 2", m.ManagedSkills)
	}
	if m.UntrackedSkills != 1 {
		t.Errorf("untracked_skills = %d, want 1", m.UntrackedSkills)
	}
	if m.UpstreamUpdates != 0 {
		t.Errorf("upstream_updates = %d, want 0", m.UpstreamUpdates)
	}
	if m.FailedDeployments != 0 {
		t.Errorf("failed_deployments = %d, want 0", m.FailedDeployments)
	}
	// high_risk = untracked(1) + conflicts(0) + failed(0).
	if m.HighRiskItems != 1 {
		t.Errorf("high_risk_items = %d, want 1", m.HighRiskItems)
	}

	// Action items: approve_devices (1 pending) and track_untracked (1) must
	// be present and carry the right counts; zero-count actions are omitted.
	byKey := map[string]int{}
	for _, a := range got.ActionItems {
		byKey[a.Key] = a.Count
		if a.Count == 0 {
			t.Errorf("action %q has count 0; zero actions must be omitted", a.Key)
		}
		if a.Label == "" {
			t.Errorf("action %q missing label", a.Key)
		}
	}
	if byKey["approve_devices"] != 1 {
		t.Errorf("approve_devices count = %d, want 1", byKey["approve_devices"])
	}
	if byKey["track_untracked"] != 1 {
		t.Errorf("track_untracked count = %d, want 1", byKey["track_untracked"])
	}
	if _, ok := byKey["review_upstream"]; ok {
		t.Error("review_upstream present but upstream_updates is 0; should be omitted")
	}
}

// TestDashboard_Empty: a fresh server (no devices, no skills) returns all-zero
// metrics and an empty (non-null) action list.
func TestDashboard_Empty(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/dashboard", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got dashboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Metrics != (dashboardMetrics{}) {
		t.Errorf("metrics = %+v, want all zero", got.Metrics)
	}
	if got.ActionItems == nil {
		t.Error("action_items = null, want []")
	}
	if len(got.ActionItems) != 0 {
		t.Errorf("action_items = %d, want 0", len(got.ActionItems))
	}
}

// TestDashboard_RequiresAuth: auth-gated like every other /api read.
func TestDashboard_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)
	resp, err := http.Get(srv.URL + "/api/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
