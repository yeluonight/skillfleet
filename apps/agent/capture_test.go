package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateCapturePath_AcceptsHomeChild: a directory under home resolves.
func TestValidateCapturePath_AcceptsHomeChild(t *testing.T) {
	home := t.TempDir()
	skill := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := validateCapturePath(skill, home)
	if err != nil {
		t.Fatalf("validateCapturePath: %v", err)
	}
	// EvalSymlinks may canonicalise (e.g. /var -> /private/var on macOS);
	// compare the resolved forms.
	want, _ := filepath.EvalSymlinks(skill)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestValidateCapturePath_RejectsOutsideHome: a path outside home is refused,
// so a forged downlink cannot make the agent read arbitrary directories.
func TestValidateCapturePath_RejectsOutsideHome(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir() // sibling temp dir, not under home
	if _, err := validateCapturePath(outside, home); err == nil {
		t.Fatal("expected rejection for path outside home, got nil")
	}
}

// TestValidateCapturePath_RejectsFile: a regular file (not a dir) is refused.
func TestValidateCapturePath_RejectsFile(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCapturePath(file, home); err == nil {
		t.Fatal("expected rejection for non-directory, got nil")
	}
}

// TestReadSkillFiles: reads the skill tree, base64-encodes content, and
// applies the scanner's skip rules (hidden files excluded).
func TestReadSkillFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "run.sh"), []byte("echo hi"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hidden file the scanner skips; it must not be uploaded.
	if err := os.WriteFile(filepath.Join(dir, ".secret"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := readSkillFiles(dir)
	if err != nil {
		t.Fatalf("readSkillFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		raw, err := base64.StdEncoding.DecodeString(f.ContentBase64)
		if err != nil {
			t.Fatalf("decode %s: %v", f.Path, err)
		}
		got[f.Path] = string(raw)
	}
	if got["SKILL.md"] != "# demo" {
		t.Errorf("SKILL.md = %q", got["SKILL.md"])
	}
	if got["scripts/run.sh"] != "echo hi" {
		t.Errorf("scripts/run.sh = %q", got["scripts/run.sh"])
	}
	if _, ok := got[".secret"]; ok {
		t.Error(".secret should have been skipped")
	}
	if len(got) != 2 {
		t.Errorf("file count = %d, want 2", len(got))
	}
}
