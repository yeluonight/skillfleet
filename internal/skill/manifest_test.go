package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// writePkg creates a package directory under t.TempDir() from a map of
// package-relative path -> content, returning the root path.
func writePkg(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGenerate_FullPackage(t *testing.T) {
	root := writePkg(t, map[string]string{
		"SKILL.md":              "---\nname: deploy-helper\ndescription: deploys things\n---\n# Deploy\n",
		"README.md":             "# readme\n",
		"scripts/deploy.py":     "print('hi')\n",
		"config/defaults.json":  `{"a":1}`,
		"references/notes.md":   "notes\n",
	})

	m, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		t.Errorf("schema = %d, want %d", m.SchemaVersion, ManifestSchemaVersion)
	}
	if m.Name != "deploy-helper" {
		t.Errorf("name = %q, want deploy-helper (from frontmatter)", m.Name)
	}
	if m.Description != "deploys things" {
		t.Errorf("description = %q", m.Description)
	}
	if !m.HasSkillMD {
		t.Error("HasSkillMD = false, want true")
	}
	if m.FileCount != 5 {
		t.Errorf("file_count = %d, want 5", m.FileCount)
	}
	if m.ContentSHA256 == "" {
		t.Error("content_sha256 empty")
	}
	// Files sorted by path.
	wantFirst := "README.md"
	if m.Files[0].Path != wantFirst {
		t.Errorf("first file = %q, want %q", m.Files[0].Path, wantFirst)
	}
	// deploy.py is executable? we wrote 0644, so Exec=false; text.
	for _, f := range m.Files {
		if f.Binary {
			t.Errorf("file %q flagged binary, want text", f.Path)
		}
	}
}

func TestGenerate_NameFallsBackToDir(t *testing.T) {
	// No SKILL.md: name should be the root dir base name and a
	// missing_skill_md warning should be present.
	root := writePkg(t, map[string]string{
		"notes.txt": "hello\n",
	})
	m, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasSkillMD {
		t.Error("HasSkillMD = true, want false")
	}
	if m.Name != filepath.Base(root) {
		t.Errorf("name = %q, want dir base %q", m.Name, filepath.Base(root))
	}
	if !hasWarning(m, "missing_skill_md") {
		t.Errorf("warnings = %+v, want missing_skill_md", m.Warnings)
	}
}

func TestGenerate_BinaryDetection(t *testing.T) {
	root := t.TempDir()
	// A NUL byte → binary.
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "text.md"), []byte("# 中文标题\n正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, f := range m.Files {
		byPath[f.Path] = f
	}
	if !byPath["blob.bin"].Binary {
		t.Error("blob.bin should be binary")
	}
	if byPath["text.md"].Binary {
		t.Error("UTF-8 Chinese markdown should be text, not binary")
	}
}

func TestGenerate_ExecBit(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "run.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 1 || !m.Files[0].Exec {
		t.Errorf("exec bit not captured: %+v", m.Files)
	}
}

func TestManifest_MarshalRoundTripDeterministic(t *testing.T) {
	root := writePkg(t, map[string]string{
		"SKILL.md":      "---\nname: x\ndescription: d\n---\nbody\n",
		"a/b.txt":       "ab\n",
		"z.txt":         "z\n",
	})
	m1, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := m1.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Re-generate from the same tree → identical bytes.
	m2, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := m2.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Error("Marshal not deterministic across two Generate calls")
	}
	// Round-trip.
	back, err := Unmarshal(b1)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := back.Marshal()
	if string(rb) != string(b1) {
		t.Error("Unmarshal→Marshal not stable")
	}
}

func TestGenerate_RootMissing(t *testing.T) {
	_, err := Generate(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestLooksBinary(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"empty", nil, false},
		{"ascii", []byte("hello world"), false},
		{"utf8 chinese", []byte("你好，世界"), false},
		{"nul", []byte{'a', 0x00, 'b'}, true},
		{"invalid utf8", []byte{0xff, 0xfe, 0xfd}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksBinary(c.in); got != c.want {
				t.Errorf("looksBinary(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func hasWarning(m Manifest, code string) bool {
	for _, w := range m.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestGenerate_BadFrontmatterWarns(t *testing.T) {
	root := writePkg(t, map[string]string{
		"SKILL.md": "---\nname: [unclosed\n---\nbody\n",
	})
	m, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate should not fail on bad frontmatter: %v", err)
	}
	if !hasWarning(m, "skill_md_parse_failed") {
		t.Errorf("warnings = %+v, want skill_md_parse_failed", m.Warnings)
	}
}

func TestLooksBinary_SplitRuneAtWindow(t *testing.T) {
	sample := make([]byte, sniffLen)
	for i := range sample {
		sample[i] = 'a'
	}
	sample[sniffLen-1] = 0xE4 // lead byte of a 3-byte rune
	if looksBinary(sample) {
		t.Error("text split mid-rune at window flagged binary, want text")
	}
	if !looksBinary([]byte{0xE4, 0x20, 0x20}) {
		t.Error("invalid short sequence should be binary")
	}
}

func TestGenerate_LargeTextFileWithMultibyteTail(t *testing.T) {
	root := t.TempDir()
	body := make([]byte, 0, 700)
	for i := 0; i < 600; i++ {
		body = append(body, 'x')
	}
	body = append(body, []byte("结尾中文内容")...)
	if err := os.WriteFile(filepath.Join(root, "big.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[0].Binary {
		t.Error("large UTF-8 file flagged binary, want text")
	}
}
