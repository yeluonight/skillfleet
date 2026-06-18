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
	"github.com/yeluonight/skillfleet/internal/ratelimit"
)

// storeSampleInventory writes a two-tool inventory for deviceID using
// the real inventory.Store so the read path is tested against
// authentic rows.
func storeSampleInventory(t *testing.T, d *sql.DB, deviceID string) {
	t.Helper()
	storeReport(t, d, deviceID, inventory.Report{
		AgentVersion: "0.3.0",
		Tools: []inventory.ToolInstance{
			{
				ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
				RootID: "claude_user", RootPath: "/h/.claude/skills",
				Skills: []inventory.Skill{
					{Name: "deploy", SkillPath: "/h/.claude/skills/deploy", HasSkillMD: true,
						Description: "deploys", EffectiveState: "on", NativeState: "available",
						ContentSHA256: "abc", FileCount: 2, TotalBytes: 50},
					{Name: "lint", SkillPath: "/h/.claude/skills/lint", HasSkillMD: true,
						EffectiveState: "off", NativeState: "disabled",
						Warnings: []inventory.Warning{{Code: "missing_description", Message: "no desc"}}},
				},
			},
			{
				ToolKey: "opencode", DisplayName: "OpenCode", Scope: "user",
				RootID: "opencode_user_claude", RootPath: "/h/.claude/skills",
				Skills: []inventory.Skill{
					{Name: "deploy", SkillPath: "/h/.claude/skills/deploy", HasSkillMD: true,
						EffectiveState: "on", NativeState: "allow"},
				},
			},
		},
		Roots: []inventory.RootCandidate{
			{ToolKey: "claude-code", Scope: "user", Path: "/h/.claude/skills", Exists: true, Registered: true, RootID: "claude_user", ToolDetected: true},
			{ToolKey: "codex", Scope: "user", Path: "/h/.agents/skills", Exists: false, Shared: true},
		},
	})
}

func storeReport(t *testing.T, d *sql.DB, deviceID string, rep inventory.Report) {
	t.Helper()
	if _, err := inventory.Store(context.Background(), d, deviceID, rep, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceInventory_ReturnsMatrix(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "scan-box", devices.StatusApproved)
	storeSampleInventory(t, d, id)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/"+id+"/inventory", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var got struct {
		Run *struct {
			RunID      string `json:"run_id"`
			SkillCount int    `json:"skill_count"`
			RootCount  int    `json:"root_count"`
			Roots      []struct {
				ToolKey    string `json:"tool_key"`
				Path       string `json:"path"`
				Registered bool   `json:"registered"`
				RootID     string `json:"root_id"`
				Shared     bool   `json:"shared"`
			} `json:"roots"`
			Skills []struct {
				ToolKey        string `json:"tool_key"`
				Scope          string `json:"scope"`
				Name           string `json:"name"`
				EffectiveState string `json:"effective_state"`
				NativeState    string `json:"native_state"`
				Warnings       []struct {
					Code string `json:"code"`
				} `json:"warnings"`
			} `json:"skills"`
		} `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Run == nil {
		t.Fatal("run should not be nil for a scanned device")
	}
	if got.Run.SkillCount != 3 || got.Run.RootCount != 2 {
		t.Errorf("counts = %d / %d, want 3 / 2", got.Run.SkillCount, got.Run.RootCount)
	}
	if len(got.Run.Roots) != 2 || got.Run.Roots[0].RootID != "claude_user" || !got.Run.Roots[1].Shared {
		t.Errorf("roots = %+v", got.Run.Roots)
	}
	if len(got.Run.Skills) != 3 {
		t.Fatalf("skills = %d, want 3", len(got.Run.Skills))
	}
	// Ordered by tool_key, scope, name: claude-code/deploy, claude-code/lint, opencode/deploy.
	if got.Run.Skills[0].ToolKey != "claude-code" || got.Run.Skills[0].Name != "deploy" {
		t.Errorf("first row = %+v", got.Run.Skills[0])
	}
	if got.Run.Skills[1].Name != "lint" || got.Run.Skills[1].EffectiveState != "off" {
		t.Errorf("lint row = %+v", got.Run.Skills[1])
	}
	if len(got.Run.Skills[1].Warnings) != 1 || got.Run.Skills[1].Warnings[0].Code != "missing_description" {
		t.Errorf("lint warnings = %+v", got.Run.Skills[1].Warnings)
	}
	// The same skill under opencode is on/allow (matrix payoff).
	if got.Run.Skills[2].ToolKey != "opencode" || got.Run.Skills[2].NativeState != "allow" {
		t.Errorf("opencode row = %+v", got.Run.Skills[2])
	}
}

func TestDeviceInventory_NeverScannedReturnsNullRun(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "fresh-box", devices.StatusApproved)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/"+id+"/inventory", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["run"] != nil {
		t.Errorf("run = %v, want nil for never-scanned device", got["run"])
	}
}

func TestDeviceInventory_UnknownDevice404(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/dev_nope/inventory", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeviceInventory_RequiresAuth(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Get(srv.URL + "/api/devices/dev_x/inventory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
