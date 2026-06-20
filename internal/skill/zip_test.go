package skill

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

// buildZip builds an in-memory zip from path->content.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportZip_FlatPackage(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"SKILL.md":       "---\nname: z\n---\n# Z\n",
		"scripts/run.sh": "#!/bin/sh\n",
	})
	files, err := ImportZip(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = string(f.Content)
	}
	if _, ok := byPath["SKILL.md"]; !ok {
		t.Errorf("SKILL.md missing: %+v", byPath)
	}
}

func TestImportZip_StripsCommonTopDir(t *testing.T) {
	// GitHub-style wrapper: everything under "deploy-helper/".
	raw := buildZip(t, map[string]string{
		"deploy-helper/SKILL.md":       "---\nname: deploy-helper\n---\nx\n",
		"deploy-helper/scripts/run.sh": "#!/bin/sh\n",
	})
	files, err := ImportZip(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "deploy-helper/SKILL.md" {
			t.Errorf("top dir not stripped: %s", f.Path)
		}
	}
	found := false
	for _, f := range files {
		if f.Path == "SKILL.md" {
			found = true
		}
	}
	if !found {
		t.Error("SKILL.md not at root after strip")
	}
}

func TestImportZip_MixedTopsNoStrip(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"a/SKILL.md": "x",
		"b/other.md": "y",
	})
	files, err := ImportZip(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	// Different top dirs → nothing stripped.
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	joined := paths[0] + "," + paths[1]
	if !contains(joined, "a/") && !contains(joined, "b/") {
		t.Errorf("expected original a/ b/ prefixes, got %v", paths)
	}
}

func TestImportZip_RejectsPathEscape(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"../escape.txt": "pwn",
	})
	_, err := ImportZip(bytes.NewReader(raw), int64(len(raw)))
	if err == nil {
		t.Fatal("accepted path-escaping zip entry")
	}
}

func TestImportZip_Empty(t *testing.T) {
	raw := buildZip(t, map[string]string{})
	_, err := ImportZip(bytes.NewReader(raw), int64(len(raw)))
	if !errors.Is(err, ErrZipEmpty) {
		t.Errorf("err = %v, want ErrZipEmpty", err)
	}
}

func TestImportZip_DirOnlyIsEmpty(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("just-a-dir/"); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	_, err := ImportZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if !errors.Is(err, ErrZipEmpty) {
		t.Errorf("err = %v, want ErrZipEmpty", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
