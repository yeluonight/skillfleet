package fingerprint

import (
	"time"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materialises a map of relative-path => contents into dir.
// Directories are created as needed; entries ending in "/" become
// empty directories. Returns dir for chaining.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, p)
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCompute_HappyPath(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":          "---\nname: x\n---\n",
		"scripts/deploy.py": "print('hi')\n",
		"config/data.json":  "{}",
	})

	fp, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp.Hash == "" || len(fp.Hash) != 64 {
		t.Errorf("Hash shape: %q", fp.Hash)
	}
	if fp.FileCount != 3 {
		t.Errorf("FileCount = %d, want 3", fp.FileCount)
	}
	if fp.TotalBytes == 0 {
		t.Error("TotalBytes should be non-zero")
	}
	// Files come back sorted lexicographically by forward-slash path.
	wantOrder := []string{"SKILL.md", "config/data.json", "scripts/deploy.py"}
	for i, want := range wantOrder {
		if fp.Files[i].Path != want {
			t.Errorf("Files[%d] = %s, want %s", i, fp.Files[i].Path, want)
		}
	}
}

func TestCompute_StableAcrossRuns(t *testing.T) {
	// Same content in two different roots yields the same hash.
	a := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":  "hi",
		"a/b.txt":   "world",
	})
	b := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":  "hi",
		"a/b.txt":   "world",
	})
	fpA, _ := Compute(a)
	fpB, _ := Compute(b)
	if fpA.Hash != fpB.Hash {
		t.Errorf("hash differs across identical trees:\n  %s\n  %s", fpA.Hash, fpB.Hash)
	}
}

func TestCompute_ChangeContentChangesHash(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md": "version-1",
	})
	fp1, _ := Compute(dir)

	// Modify content; same path.
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("version-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp2, _ := Compute(dir)

	if fp1.Hash == fp2.Hash {
		t.Error("hash should change when file content changes")
	}
}

func TestCompute_ChangePathChangesHash(t *testing.T) {
	dirA := writeTree(t, t.TempDir(), map[string]string{
		"foo.txt": "x",
	})
	dirB := writeTree(t, t.TempDir(), map[string]string{
		"bar.txt": "x",
	})
	fpA, _ := Compute(dirA)
	fpB, _ := Compute(dirB)
	if fpA.Hash == fpB.Hash {
		t.Error("hash should depend on filename")
	}
}

func TestCompute_ChangeExecBitChangesHash(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"run.sh": "echo hi",
	})
	fp1, _ := Compute(dir)
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	fp2, _ := Compute(dir)
	if fp1.Hash == fp2.Hash {
		t.Error("hash should react to exec bit flip")
	}
	if !fp2.Files[0].Exec {
		t.Error("Exec flag should be true after chmod +x")
	}
}

func TestCompute_SkipsHiddenFiles(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":         "x",
		".git/HEAD":        "ref: refs/heads/main",
		".DS_Store":        "binary",
		".hidden":          "hidden file",
		"subdir/.cache":    "should be skipped",
		"subdir/keep.txt":  "kept",
	})
	fp, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range fp.Files {
		if strings.HasPrefix(filepath.Base(e.Path), ".") || strings.Contains(e.Path, "/.") {
			t.Errorf("hidden file leaked into fingerprint: %s", e.Path)
		}
	}
	// Exactly the two non-hidden files made it through.
	if fp.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", fp.FileCount)
	}
}

func TestCompute_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(regular, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a symlink to another file outside dir (or to regular).
	if err := os.Symlink(regular, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	fp, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range fp.Files {
		if e.Path == "link.txt" {
			t.Error("symlink leaked into fingerprint")
		}
	}
}

func TestCompute_RejectsMissingRoot(t *testing.T) {
	_, err := Compute("/nonexistent/path/here")
	if !errors.Is(err, ErrRootMissing) {
		t.Errorf("err = %v, want ErrRootMissing", err)
	}
}

func TestCompute_RejectsNonDirRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "single.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Compute(file)
	if !errors.Is(err, ErrRootNotDir) {
		t.Errorf("err = %v, want ErrRootNotDir", err)
	}
}

func TestCompute_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	huge := make([]byte, MaxFileBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Compute(dir)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("err = %v, want ErrFileTooLarge", err)
	}
}

func TestCompute_EmptyDirYieldsZeroFileFingerprint(t *testing.T) {
	dir := t.TempDir()
	fp, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp.FileCount != 0 || fp.TotalBytes != 0 {
		t.Errorf("empty tree fp = %+v", fp)
	}
	// Hash of empty entries is sha256("") = e3b0c4...
	const emptySha = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if fp.Hash != emptySha {
		t.Errorf("empty hash = %s, want %s", fp.Hash, emptySha)
	}
}

func TestCompute_NestedDirsTraversed(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"a/b/c/deep.txt": "deep content",
		"a/sibling.txt":  "sibling",
		"top.txt":        "top",
	})
	fp, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp.FileCount != 3 {
		t.Errorf("FileCount = %d", fp.FileCount)
	}
	wantOrder := []string{"a/b/c/deep.txt", "a/sibling.txt", "top.txt"}
	for i, want := range wantOrder {
		if fp.Files[i].Path != want {
			t.Errorf("Files[%d] = %s, want %s", i, fp.Files[i].Path, want)
		}
	}
}

func TestCompute_AddingFileChangesHash(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"a.txt": "x",
	})
	fp1, _ := Compute(dir)

	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp2, _ := Compute(dir)
	if fp1.Hash == fp2.Hash {
		t.Error("hash should change after adding a file")
	}
	if fp2.FileCount != fp1.FileCount+1 {
		t.Errorf("FileCount delta = %d, want 1", fp2.FileCount-fp1.FileCount)
	}
}

func TestCompute_ModTimeIsMaxAndNotHashed(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md": "hi",
		"a/b.txt":  "world",
	})
	// Set distinct mtimes; ModTime must equal the newest.
	older := time.Unix(1_000_000, 0)
	newer := time.Unix(2_000_000, 0)
	if err := os.Chtimes(filepath.Join(dir, "SKILL.md"), older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "a", "b.txt"), newer, newer); err != nil {
		t.Fatal(err)
	}
	fp, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fp.ModTime.Equal(newer) {
		t.Errorf("ModTime = %v, want newest %v", fp.ModTime, newer)
	}

	// Bumping an mtime must NOT change the content hash (drift ignores mtime).
	hashBefore := fp.Hash
	if err := os.Chtimes(filepath.Join(dir, "SKILL.md"), newer, newer); err != nil {
		t.Fatal(err)
	}
	fp2, _ := Compute(dir)
	if fp2.Hash != hashBefore {
		t.Errorf("hash changed after mtime bump: %s != %s", fp2.Hash, hashBefore)
	}
}
