package agentstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeluonight/skillfleet/internal/agentinstall"
	"github.com/yeluonight/skillfleet/internal/deploy"
)

// TestStateChange_Claude_EndToEnd drives the public entry point: a target
// resolving to a claude allowed root writes the override to the root's
// sibling settings.json.
func TestStateChange_Claude_EndToEnd(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	w := NewWriter([]agentinstall.AllowedRoot{
		{ID: "claude_user", Tool: "claude-code", Scope: "user", Path: skillsRoot},
	}, home)

	res, err := w.StateChange(context.Background(), deploy.Request{
		Operation:    deploy.OpStateChange,
		SkillName:    "deploy",
		Target:       deploy.Target{ToolKey: "claude-code", Scope: "user", RootID: "claude_user"},
		DesiredState: "off",
	})
	if err != nil {
		t.Fatalf("StateChange: %v (%s)", err, res.ErrorMessage)
	}
	if res.ResolvedRootPath != skillsRoot {
		t.Errorf("ResolvedRootPath = %q, want %q", res.ResolvedRootPath, skillsRoot)
	}
	// settings.json sibling of the skills dir.
	got := readClaudeOverrides(t, filepath.Join(home, ".claude", "settings.json"))
	if got["deploy"] != "off" {
		t.Errorf("override = %q, want off", got["deploy"])
	}
}

// TestStateChange_Codex_EndToEnd checks the codex path: the config key is
// the skill's SKILL.md absolute path under the resolved root, written to
// the per-user ~/.codex/config.toml.
func TestStateChange_Codex_EndToEnd(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	w := NewWriter([]agentinstall.AllowedRoot{
		{ID: "codex_user", Tool: "codex", Scope: "user", Path: skillsRoot},
	}, home)

	if _, err := w.StateChange(context.Background(), deploy.Request{
		SkillName:    "deploy",
		Target:       deploy.Target{ToolKey: "codex", Scope: "user"},
		DesiredState: "off",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := readCodexConfig(t, filepath.Join(home, ".codex", "config.toml"))
	wantKey := filepath.Join(skillsRoot, "deploy", "SKILL.md")
	if cfg[wantKey] != false {
		t.Errorf("codex entry for %s = %v, want disabled", wantKey, cfg[wantKey])
	}
}

// TestStateChange_RefusesUnresolvableTarget proves a target matching no
// allowed root is refused before any file is touched.
func TestStateChange_RefusesUnresolvableTarget(t *testing.T) {
	home := t.TempDir()
	w := NewWriter([]agentinstall.AllowedRoot{
		{ID: "claude_user", Tool: "claude-code", Scope: "user", Path: filepath.Join(home, ".claude", "skills")},
	}, home)

	res, err := w.StateChange(context.Background(), deploy.Request{
		SkillName:    "deploy",
		Target:       deploy.Target{ToolKey: "claude-code", Scope: "project"}, // no project root
		DesiredState: "off",
	})
	if err == nil {
		t.Fatal("want refusal for unresolvable target, got nil")
	}
	if !errors.Is(err, agentinstall.ErrRootNotAllowed) {
		t.Errorf("want ErrRootNotAllowed, got %v", err)
	}
	if res.ErrorCode != "root_not_allowed" {
		t.Errorf("ErrorCode = %q, want root_not_allowed", res.ErrorCode)
	}
	// No settings.json should have been created.
	if _, statErr := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(statErr) {
		t.Error("refused target still wrote a config file")
	}
}

// TestStateChange_UnsupportedToolFailsSafe proves a tool with no writer
// (e.g. one that slipped past the planner) is refused with a clear code,
// not silently ignored.
func TestStateChange_UnsupportedToolFailsSafe(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "ag", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	w := NewWriter([]agentinstall.AllowedRoot{
		{ID: "ag", Tool: "antigravity", Scope: "user", Path: root},
	}, home)
	res, err := w.StateChange(context.Background(), deploy.Request{
		SkillName:    "x",
		Target:       deploy.Target{ToolKey: "antigravity", Scope: "user"},
		DesiredState: "off",
	})
	if err == nil || !errors.Is(err, ErrUnknownTool) {
		t.Errorf("want ErrUnknownTool, got %v", err)
	}
	if res.ErrorCode != "unsupported_tool" {
		t.Errorf("ErrorCode = %q, want unsupported_tool", res.ErrorCode)
	}
}
