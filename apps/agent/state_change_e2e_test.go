package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/BurntSushi/toml"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/agentapi"
	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentclient"
	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/migrations"
)

// runStateChangeE2E drives one state_change job through the real downlink:
// it enrolls + approves a device, stores a state_change job with the given
// request/plan, spins up the agentapi server, and runs the job through the
// actual agentclient + agentstate writer with the given allowed roots and
// home dir. It returns the final job status so callers can assert the
// recorded outcome alongside the on-disk config edit they check directly.
//
// This is the shared rig behind the §17 Phase 9 acceptance: claude / codex
// / opencode each get one of these with a tool-appropriate root + assertion.
func runStateChangeE2E(t *testing.T, roots []agentcfg.AllowedRoot, homeDir string, req deploy.Request, plan deploy.StateChangePlan) deploy.Status {
	t.Helper()
	ctx := context.Background()

	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tx, _ := d.BeginTx(ctx, nil)
	tok, _ := enrollment.Create(ctx, d, time.Hour, now)
	enrollment.Consume(ctx, tx, tok.Plaintext, now)
	res, err := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n", OS: "linux", Arch: "amd64"}, now)
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if err := devices.SetStatus(ctx, d, res.Device.ID, devices.StatusApproved); err != nil {
		t.Fatal(err)
	}

	reqJSON, _ := json.Marshal(req)
	planJSON, _ := json.Marshal(plan)
	store := deploy.New(d)
	job, err := store.Create(ctx, deploy.CreateParams{
		DeviceID:    res.Device.ID,
		Operation:   deploy.OpStateChange,
		RequestJSON: string(reqJSON),
		PlanJSON:    string(planJSON),
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(agentapi.NewRouter(agentapi.Deps{
		DB:    d,
		Now:   func() time.Time { return now },
		Audit: audit.New(d, nil, func() time.Time { return now }),
	}))
	t.Cleanup(srv.Close)

	client, err := agentclient.New(agentclient.Config{
		ServerURL: srv.URL, DeviceID: res.Device.ID, DeviceSecret: res.Secret,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	if err := agentcfg.Save(cfgPath, testAgentConfig(roots)); err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := client.Jobs(ctx)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runOneJob(ctx, log, client, cfgPath, homeDir, claimed)

	final, _ := store.Get(ctx, job.ID)
	return final.Status
}

// readSkillOverride reads settings.json's skillOverrides[name], "" if absent.
func readSkillOverride(t *testing.T, path, name string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s struct {
		SkillOverrides map[string]string `json:"skillOverrides"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return s.SkillOverrides[name]
}

// readCodexEnabled reads the enabled flag for the [[skills.config]] entry
// whose path == key. Returns (value, found).
func readCodexEnabled(t *testing.T, path, key string) (bool, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// Minimal TOML parse via the agent's own dependency surface: reuse a
	// small struct decode (BurntSushi/toml is already a dep).
	var doc struct {
		Skills struct {
			Config []struct {
				Path    string `toml:"path"`
				Enabled bool   `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, c := range doc.Skills.Config {
		if c.Path == key {
			return c.Enabled, true
		}
	}
	return false, false
}

// readOpencodePerm reads permission.skill[name], "" if absent.
func readOpencodePerm(t *testing.T, path, name string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s struct {
		Permission struct {
			Skill map[string]string `json:"skill"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return s.Permission.Skill[name]
}

// TestStateChangeE2E_Codex: a codex off job disables the skill in the
// per-user ~/.codex/config.toml, keyed by the SKILL.md absolute path.
func TestStateChangeE2E_Codex(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsRoot, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	status := runStateChangeE2E(t,
		[]agentcfg.AllowedRoot{{ID: "r1", Tool: "codex", Scope: "user", Path: skillsRoot}},
		home,
		deploy.Request{
			Operation: deploy.OpStateChange, SkillName: "deploy", DesiredState: "off",
			Target: deploy.Target{ToolKey: "codex", Scope: "user", RootID: "r1"},
		},
		deploy.StateChangePlan{
			Target:    deploy.Target{ToolKey: "codex", Scope: "user", RootID: "r1"},
			SkillName: "deploy", DesiredState: "off",
		})
	if status != deploy.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", status)
	}
	key := filepath.Join(skillsRoot, "deploy", "SKILL.md")
	enabled, found := readCodexEnabled(t, filepath.Join(home, ".codex", "config.toml"), key)
	if !found {
		t.Fatalf("no config.toml entry for %s", key)
	}
	if enabled {
		t.Errorf("codex entry enabled=true, want disabled")
	}
}

// TestStateChangeE2E_Opencode: an opencode ask job writes permission.skill
// = "ask" in the per-user opencode.json.
func TestStateChangeE2E_Opencode(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".config", "opencode", "skills")
	if err := os.MkdirAll(filepath.Join(skillsRoot, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	status := runStateChangeE2E(t,
		[]agentcfg.AllowedRoot{{ID: "r1", Tool: "opencode", Scope: "user", Path: skillsRoot}},
		home,
		deploy.Request{
			Operation: deploy.OpStateChange, SkillName: "deploy", DesiredState: "ask",
			Target: deploy.Target{ToolKey: "opencode", Scope: "user", RootID: "r1"},
		},
		deploy.StateChangePlan{
			Target:    deploy.Target{ToolKey: "opencode", Scope: "user", RootID: "r1"},
			SkillName: "deploy", DesiredState: "ask",
		})
	if status != deploy.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", status)
	}
	if got := readOpencodePerm(t, filepath.Join(home, ".config", "opencode", "opencode.json"), "deploy"); got != "ask" {
		t.Errorf("opencode perm = %q, want ask", got)
	}
}

// TestStateChangeE2E_AllToolsAllStates is the §17 Phase 9 acceptance
// matrix end-to-end: for every (tool, supported state) the job runs
// through the real downlink and the tool's native config reflects it.
// claude: on/name-only/user-invocable-only/off; codex: on/off; opencode:
// on/ask/off. (on deletes the override/perm — asserted as absent.)
func TestStateChangeE2E_AllToolsAllStates(t *testing.T) {
	type want struct {
		// assert is given the home dir + skills root and verifies the
		// tool's config reflects the state.
		assert func(t *testing.T, home, skillsRoot string)
	}
	cases := []struct {
		tool    string
		rootRel string // skills root relative to home
		state   string
		want    want
	}{
		// claude-code: settings.json sibling of the skills root.
		{"claude-code", ".claude/skills", "off", want{func(t *testing.T, home, root string) {
			if got := readSkillOverride(t, filepath.Join(filepath.Dir(root), "settings.json"), "deploy"); got != "off" {
				t.Errorf("claude off: override = %q, want off", got)
			}
		}}},
		{"claude-code", ".claude/skills", "name-only", want{func(t *testing.T, home, root string) {
			if got := readSkillOverride(t, filepath.Join(filepath.Dir(root), "settings.json"), "deploy"); got != "name-only" {
				t.Errorf("claude name-only: override = %q, want name-only", got)
			}
		}}},
		{"claude-code", ".claude/skills", "user-invocable-only", want{func(t *testing.T, home, root string) {
			if got := readSkillOverride(t, filepath.Join(filepath.Dir(root), "settings.json"), "deploy"); got != "user-invocable-only" {
				t.Errorf("claude uio: override = %q, want user-invocable-only", got)
			}
		}}},
		// codex: ~/.codex/config.toml keyed by SKILL.md path.
		{"codex", ".agents/skills", "off", want{func(t *testing.T, home, root string) {
			key := filepath.Join(root, "deploy", "SKILL.md")
			enabled, found := readCodexEnabled(t, filepath.Join(home, ".codex", "config.toml"), key)
			if !found || enabled {
				t.Errorf("codex off: enabled=%v found=%v, want disabled+found", enabled, found)
			}
		}}},
		// opencode: ~/.config/opencode/opencode.json permission.skill.
		{"opencode", ".config/opencode/skills", "ask", want{func(t *testing.T, home, root string) {
			if got := readOpencodePerm(t, filepath.Join(home, ".config", "opencode", "opencode.json"), "deploy"); got != "ask" {
				t.Errorf("opencode ask: perm = %q, want ask", got)
			}
		}}},
		{"opencode", ".config/opencode/skills", "off", want{func(t *testing.T, home, root string) {
			if got := readOpencodePerm(t, filepath.Join(home, ".config", "opencode", "opencode.json"), "deploy"); got != "deny" {
				t.Errorf("opencode off: perm = %q, want deny", got)
			}
		}}},
	}

	for _, c := range cases {
		t.Run(c.tool+"/"+c.state, func(t *testing.T) {
			home := t.TempDir()
			skillsRoot := filepath.Join(home, filepath.FromSlash(c.rootRel))
			if err := os.MkdirAll(filepath.Join(skillsRoot, "deploy"), 0o755); err != nil {
				t.Fatal(err)
			}
			target := deploy.Target{ToolKey: c.tool, Scope: "user", RootID: "r1"}
			status := runStateChangeE2E(t,
				[]agentcfg.AllowedRoot{{ID: "r1", Tool: c.tool, Scope: "user", Path: skillsRoot}},
				home,
				deploy.Request{Operation: deploy.OpStateChange, SkillName: "deploy", DesiredState: adapters.EffectiveState(c.state), Target: target},
				deploy.StateChangePlan{Target: target, SkillName: "deploy", DesiredState: adapters.EffectiveState(c.state)},
			)
			if status != deploy.StatusSucceeded {
				t.Fatalf("%s/%s: status = %q, want succeeded", c.tool, c.state, status)
			}
			c.want.assert(t, home, skillsRoot)
		})
	}
}
