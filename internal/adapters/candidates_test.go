package adapters

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildCandidateRoots_ExpandsAndStampsExists(t *testing.T) {
	home := t.TempDir()
	// Create one of the two candidate dirs so Exists differs per row.
	existing := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	specs := []CandidateRootSpec{
		{Scope: ScopeUser, Tmpl: "~/.claude/skills"},
		{Scope: ScopeUser, Tmpl: "~/.agents/skills", Shared: true},
		{Scope: ScopeSystem, Tmpl: "/etc/codex/skills"},
	}
	sc := ScanContext{HomeDir: home}
	got := BuildCandidateRoots(sc, "claude-code", true, specs)

	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3", len(got))
	}

	// Row 0: ~/.claude/skills — expanded, exists, not shared.
	if got[0].Path != existing {
		t.Errorf("path[0] = %q, want %q", got[0].Path, existing)
	}
	if got[0].DisplayTmpl != "~/.claude/skills" {
		t.Errorf("displayTmpl[0] = %q", got[0].DisplayTmpl)
	}
	if !got[0].Exists {
		t.Error("row 0 should Exist")
	}
	if got[0].Shared {
		t.Error("row 0 should not be Shared")
	}
	if !got[0].ToolDetected {
		t.Error("toolDetected should propagate to every row")
	}
	if got[0].ToolKey != "claude-code" {
		t.Errorf("toolKey[0] = %q", got[0].ToolKey)
	}

	// Row 1: ~/.agents/skills — expanded, missing, shared.
	if got[1].Exists {
		t.Error("row 1 should not Exist")
	}
	if !got[1].Shared {
		t.Error("row 1 should be Shared")
	}

	// Row 2: absolute system path — not home-relative, left verbatim.
	if got[2].Path != "/etc/codex/skills" {
		t.Errorf("path[2] = %q, want /etc/codex/skills", got[2].Path)
	}
	if got[2].Scope != ScopeSystem {
		t.Errorf("scope[2] = %q, want system", got[2].Scope)
	}
}

func TestBuildCandidateRoots_SkipsUnexpandableTilde(t *testing.T) {
	// Empty home + a ~-relative spec cannot expand → that row is dropped
	// rather than emitting a broken path.
	specs := []CandidateRootSpec{
		{Scope: ScopeUser, Tmpl: "~/.claude/skills"},
		{Scope: ScopeSystem, Tmpl: "/etc/codex/skills"},
	}
	got := BuildCandidateRoots(ScanContext{HomeDir: ""}, "x", false, specs)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (tilde row dropped)", len(got))
	}
	if got[0].Path != "/etc/codex/skills" {
		t.Errorf("surviving path = %q", got[0].Path)
	}
}

func TestConfigDirExists(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !ConfigDirExists(home, "~/.claude") {
		t.Error("existing config dir should report true")
	}
	if ConfigDirExists(home, "~/.nonexistent") {
		t.Error("missing config dir should report false")
	}
	if ConfigDirExists("", "~/.claude") {
		t.Error("unexpandable path should report false")
	}
}

func TestBinaryOnPath(t *testing.T) {
	// A binary that is essentially always present on a unix test runner.
	// Use the test binary's own dir trick instead to stay portable: put
	// a fake executable on a temp PATH.
	dir := t.TempDir()
	bin := filepath.Join(dir, "sf-fake-tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if !BinaryOnPath("sf-fake-tool") {
		t.Error("binary on PATH should report true")
	}
	if BinaryOnPath("sf-definitely-not-a-real-tool") {
		t.Error("missing binary should report false")
	}
}
