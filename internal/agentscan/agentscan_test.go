package agentscan

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScan_RealAdapterSet runs the full adapter set against a fake home
// directory laid out with one Claude Code skill, asserting the report
// is assembled with the right tool / skill mapping.
func TestScan_AssemblesReport(t *testing.T) {
	home := t.TempDir()
	// Claude Code user skill.
	skillDir := filepath.Join(home, ".claude", "skills", "deploy-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: deploy-helper\ndescription: deploys things\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := Scan(Options{AgentVersion: "test-1", HomeDir: home})

	if report.AgentVersion != "test-1" {
		t.Errorf("AgentVersion = %q", report.AgentVersion)
	}
	// The ~/.claude/skills directory is scanned by BOTH the Claude Code
	// adapter and the OpenCode adapter (OpenCode deliberately reads
	// skills authored for other tools), so the same skill legitimately
	// appears under two tool instances. Assert >= 1 and verify the
	// claude-code instance specifically below.
	if report.SkillCount() < 1 {
		t.Fatalf("SkillCount = %d, want >= 1; tools=%+v", report.SkillCount(), report.Tools)
	}

	// Find the claude-code tool instance.
	var found bool
	for _, ti := range report.Tools {
		if ti.ToolKey == "claude-code" {
			found = true
			if len(ti.Skills) != 1 {
				t.Fatalf("claude-code skills = %d, want 1", len(ti.Skills))
			}
			sk := ti.Skills[0]
			if sk.Name != "deploy-helper" {
				t.Errorf("skill name = %q", sk.Name)
			}
			if sk.Description != "deploys things" {
				t.Errorf("description = %q", sk.Description)
			}
			if sk.EffectiveState != "on" {
				t.Errorf("effective_state = %q, want on", sk.EffectiveState)
			}
			if sk.ContentSHA256 == "" {
				t.Error("ContentSHA256 empty")
			}
		}
	}
	if !found {
		t.Error("claude-code tool instance not in report")
	}
}

func TestScan_EmptyHomeYieldsEmptyReport(t *testing.T) {
	report := Scan(Options{AgentVersion: "x", HomeDir: t.TempDir()})
	if report.SkillCount() != 0 || report.RootCount() != 0 {
		t.Errorf("empty home should yield empty report, got %+v", report)
	}
}

func TestAll_SixAdapters(t *testing.T) {
	all := All()
	if len(all) != 6 {
		t.Fatalf("All() = %d adapters, want 6", len(all))
	}
	keys := map[string]bool{}
	for _, a := range all {
		keys[a.Key()] = true
	}
	for _, want := range []string{"claude-code", "codex", "opencode", "antigravity", "antigravity-cli", "pi"} {
		if !keys[want] {
			t.Errorf("adapter %q missing from All()", want)
		}
	}
}

func TestScan_FlattensWarnings(t *testing.T) {
	home := t.TempDir()
	// A skill dir with no SKILL.md -> adapter emits a no_skill_md warning.
	skillDir := filepath.Join(home, ".claude", "skills", "empty")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{AgentVersion: "x", HomeDir: home})
	var sawWarning bool
	for _, ti := range report.Tools {
		for _, sk := range ti.Skills {
			if sk.Name == "empty" && len(sk.Warnings) > 0 {
				sawWarning = true
			}
		}
	}
	if !sawWarning {
		t.Error("expected a warning on the SKILL.md-less skill")
	}
}
