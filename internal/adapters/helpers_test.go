package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/skillmd"
)

func TestExpandHome(t *testing.T) {
	home := "/home/alice"
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"/abs/path", "/abs/path", false},
		{"relative/path", "relative/path", false},
		{"~", home, false},
		{"~/.claude/skills", home + "/.claude/skills", false},
		{"~bob/x", "", true}, // ~user form unsupported
	}
	for _, c := range cases {
		got, err := ExpandHome(c.in, home)
		if c.wantErr {
			if err == nil {
				t.Errorf("ExpandHome(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ExpandHome(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandHome_EmptyHomeWithTilde(t *testing.T) {
	if _, err := ExpandHome("~/x", ""); err == nil {
		t.Error("expected error expanding ~ without home dir")
	}
}

func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	if !DirExists(dir) {
		t.Error("DirExists should be true for a real dir")
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if DirExists(file) {
		t.Error("DirExists should be false for a regular file")
	}
	if DirExists(filepath.Join(dir, "nope")) {
		t.Error("DirExists should be false for a missing path")
	}
}

// makeSkillRoot lays out a standard root: each child dir optionally
// gets a SKILL.md with the given body.
func makeSkillRoot(t *testing.T, skills map[string]string) SkillRoot {
	t.Helper()
	dir := t.TempDir()
	for name, body := range skills {
		sd := filepath.Join(dir, name)
		if err := os.MkdirAll(sd, 0o755); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(sd, SkillFileName), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return SkillRoot{ID: "test_root", Tool: "test", Scope: ScopeUser, Path: dir}
}

func TestScanStandardRoot_HappyPath(t *testing.T) {
	root := makeSkillRoot(t, map[string]string{
		"deploy-helper": "---\nname: deploy-helper\ndescription: d\n---\nbody\n",
		"lint-runner":   "---\nname: lint-runner\ndescription: e\n---\nbody\n",
	})
	got, err := ScanStandardRoot(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d skills, want 2", len(got))
	}
	byName := map[string]DiscoveredSkill{}
	for _, ds := range got {
		byName[ds.Name] = ds
	}
	dh, ok := byName["deploy-helper"]
	if !ok {
		t.Fatal("deploy-helper not found")
	}
	if !dh.HasSkillMD {
		t.Error("HasSkillMD should be true")
	}
	if dh.SkillMD.Description != "d" {
		t.Errorf("description = %q", dh.SkillMD.Description)
	}
	if dh.ContentSHA256 == "" {
		t.Error("ContentSHA256 should be populated")
	}
	if dh.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", dh.FileCount)
	}
	if dh.RootID != "test_root" {
		t.Errorf("RootID = %q", dh.RootID)
	}
	if dh.EffectiveState != StateUnknown {
		t.Errorf("nil callback should leave StateUnknown, got %s", dh.EffectiveState)
	}
}

func TestScanStandardRoot_SkillWithoutSkillMD(t *testing.T) {
	root := makeSkillRoot(t, map[string]string{
		"empty-skill": "", // dir exists, no SKILL.md
	})
	got, err := ScanStandardRoot(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d, want 1", len(got))
	}
	ds := got[0]
	if ds.HasSkillMD {
		t.Error("HasSkillMD should be false")
	}
	if !hasWarning(ds.Warnings, "no_skill_md") {
		t.Errorf("expected no_skill_md warning, got %+v", ds.Warnings)
	}
	// Still fingerprinted (empty tree -> sha256 of empty entries).
	if ds.ContentSHA256 == "" {
		t.Error("empty skill should still have a fingerprint")
	}
}

func TestScanStandardRoot_SkipsHiddenDirs(t *testing.T) {
	root := makeSkillRoot(t, map[string]string{
		"real-skill": "---\nname: real-skill\ndescription: d\n---\n",
	})
	// Add a hidden dir + a regular file alongside.
	if err := os.MkdirAll(filepath.Join(root.Path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root.Path, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ScanStandardRoot(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "real-skill" {
		t.Errorf("expected only real-skill, got %+v", names(got))
	}
}

func TestScanStandardRoot_NativeStateCallback(t *testing.T) {
	root := makeSkillRoot(t, map[string]string{
		"x": "---\nname: x\ndescription: d\ndisabled: true\n---\n",
	})
	cb := func(name, path string, md skillmd.Result) (EffectiveState, string) {
		if v, ok := md.Frontmatter["disabled"].(bool); ok && v {
			return StateOff, "disabled"
		}
		return StateOn, "available"
	}
	got, err := ScanStandardRoot(root, cb)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].EffectiveState != StateOff {
		t.Errorf("EffectiveState = %s, want off", got[0].EffectiveState)
	}
	if got[0].NativeState != "disabled" {
		t.Errorf("NativeState = %q", got[0].NativeState)
	}
}

func TestScanStandardRoot_MissingRootErrors(t *testing.T) {
	root := SkillRoot{ID: "x", Path: "/nonexistent/root/here"}
	if _, err := ScanStandardRoot(root, nil); err == nil {
		t.Error("expected error scanning a missing root")
	}
}

func TestScanStandardRoot_BadSkillMDDegradesToWarning(t *testing.T) {
	root := makeSkillRoot(t, map[string]string{
		"broken": "---\ndescription: [unterminated\n---\nbody\n",
	})
	got, err := ScanStandardRoot(root, nil)
	if err != nil {
		t.Fatalf("a broken SKILL.md must not abort the scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d, want 1", len(got))
	}
	if got[0].HasSkillMD {
		t.Error("HasSkillMD should be false on parse failure")
	}
	if !hasWarning(got[0].Warnings, "skill_md_unreadable") {
		t.Errorf("expected skill_md_unreadable warning, got %+v", got[0].Warnings)
	}
}

// helpers

func hasWarning(ws []Warning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}

func names(ds []DiscoveredSkill) string {
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Name)
	}
	return b.String()
}
