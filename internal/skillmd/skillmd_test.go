package skillmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_HappyPath(t *testing.T) {
	raw := []byte(`---
name: deploy-helper
description: Helps deploy applications safely
allowed-tools:
  - Read
  - Bash
---

# Deploy helper

Body text here.
`)
	res, err := Parse(raw, "deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "deploy-helper" {
		t.Errorf("Name = %q", res.Name)
	}
	if res.Description != "Helps deploy applications safely" {
		t.Errorf("Description = %q", res.Description)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", res.Warnings)
	}
	if res.Encoding != EncodingUTF8 {
		t.Errorf("Encoding = %s", res.Encoding)
	}
	tools, ok := res.Frontmatter["allowed-tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Errorf("allowed-tools = %v", res.Frontmatter["allowed-tools"])
	}
	if !strings.HasPrefix(res.Body, "# Deploy helper") {
		t.Errorf("Body = %q", res.Body)
	}
}

func TestParse_BOMStripped(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`---
name: x
description: y
---
body
`)...)
	res, err := Parse(raw, "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Encoding != EncodingUTF8BOM {
		t.Errorf("Encoding = %s, want utf-8-bom", res.Encoding)
	}
	if res.Name != "x" {
		t.Errorf("Name = %q", res.Name)
	}
}

func TestParse_NonUTF8WarnsAndStops(t *testing.T) {
	// invalid UTF-8 sequence
	raw := []byte{0xff, 0xfe, 0xfd, 0x00}
	res, err := Parse(raw, "x")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if res.Encoding != EncodingNonUTF8 {
		t.Errorf("Encoding = %s", res.Encoding)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "non_utf8" {
		t.Errorf("warnings = %+v", res.Warnings)
	}
	if res.Body != "" || res.Frontmatter != nil {
		t.Error("Body/Frontmatter should be empty on non-utf8")
	}
}

func TestParse_MissingFrontmatterWarn(t *testing.T) {
	raw := []byte("# Just markdown\n\nNo frontmatter here.\n")
	res, err := Parse(raw, "skill-a")
	if err != nil {
		t.Fatal(err)
	}
	if res.Frontmatter != nil {
		t.Errorf("expected nil Frontmatter, got %v", res.Frontmatter)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "missing_frontmatter" {
		t.Errorf("warnings = %+v", res.Warnings)
	}
	if !strings.HasPrefix(res.Body, "# Just markdown") {
		t.Errorf("Body = %q", res.Body)
	}
}

func TestParse_UnterminatedFrontmatterWarn(t *testing.T) {
	// Opener but no closing "---" → treated as no-frontmatter.
	raw := []byte("---\nname: x\nbody never closes\n")
	res, err := Parse(raw, "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Frontmatter != nil {
		t.Error("Frontmatter should be nil")
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "missing_frontmatter" {
		t.Errorf("warnings = %+v", res.Warnings)
	}
}

func TestParse_InvalidYAMLHardError(t *testing.T) {
	raw := []byte(`---
name: x
description: [unterminated
---
body
`)
	_, err := Parse(raw, "x")
	if !errors.Is(err, ErrFrontmatterBad) {
		t.Errorf("err = %v, want ErrFrontmatterBad", err)
	}
}

func TestParse_EmptyFrontmatterOK(t *testing.T) {
	raw := []byte("---\n---\nbody\n")
	res, err := Parse(raw, "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Frontmatter == nil {
		t.Error("empty frontmatter should still produce empty map")
	}
	// Missing description still warns.
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "missing_description" {
		t.Errorf("warnings = %+v", res.Warnings)
	}
}

func TestParse_MissingDescriptionWarn(t *testing.T) {
	raw := []byte("---\nname: x\n---\nbody\n")
	res, _ := Parse(raw, "x")
	codes := warningCodes(res.Warnings)
	if !contains(codes, "missing_description") {
		t.Errorf("warning codes = %v", codes)
	}
}

func TestParse_NameFolderMismatchWarn(t *testing.T) {
	raw := []byte("---\nname: actual-name\ndescription: d\n---\nbody\n")
	res, _ := Parse(raw, "different-folder")
	codes := warningCodes(res.Warnings)
	if !contains(codes, "name_folder_mismatch") {
		t.Errorf("warning codes = %v", codes)
	}
}

func TestParse_NameMatchesFolderNoWarn(t *testing.T) {
	raw := []byte("---\nname: same\ndescription: d\n---\nbody\n")
	res, _ := Parse(raw, "same")
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", res.Warnings)
	}
}

func TestParse_LeadingBlankLinesBeforeFrontmatter(t *testing.T) {
	raw := []byte("\n\n   \n---\nname: x\ndescription: d\n---\nbody\n")
	res, err := Parse(raw, "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Frontmatter == nil {
		t.Error("expected frontmatter to parse despite leading blanks")
	}
}

func TestParse_CRLFTolerated(t *testing.T) {
	raw := []byte("---\r\nname: x\r\ndescription: d\r\n---\r\nbody\r\n")
	res, err := Parse(raw, "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Description != "d" {
		t.Errorf("description = %q", res.Description)
	}
}

func TestParse_EmptyName(t *testing.T) {
	// Empty name in YAML — name vs folder mismatch should NOT fire
	// because we only compare when both are present.
	raw := []byte("---\nname: ''\ndescription: d\n---\nbody\n")
	res, _ := Parse(raw, "folder-x")
	codes := warningCodes(res.Warnings)
	if contains(codes, "name_folder_mismatch") {
		t.Errorf("should not warn when name is empty; got %v", codes)
	}
}

func TestParseFile_ReadsDisk(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: my-skill\ndescription: d\n---\nb"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "my-skill" || res.Description != "d" {
		t.Errorf("res = %+v", res)
	}
}

func TestParseFile_TooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	huge := make([]byte, MaxFileBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(path, huge, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseFile(path)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestParseFile_MissingFile(t *testing.T) {
	if _, err := ParseFile("/nonexistent/SKILL.md"); err == nil {
		t.Error("expected error on missing file")
	}
}

// helpers

func warningCodes(ws []Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Code
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
