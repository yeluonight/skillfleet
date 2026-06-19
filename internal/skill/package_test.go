package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestPack_Deterministic(t *testing.T) {
	files := map[string]string{
		"SKILL.md":          "---\nname: x\n---\nbody\n",
		"scripts/run.sh":    "#!/bin/sh\necho hi\n",
		"a/b/c.txt":         "nested\n",
		"config/x.json":     `{"k":1}`,
	}
	r1 := writePkg(t, files)
	r2 := writePkg(t, files)

	var buf1, buf2 bytes.Buffer
	i1, err := Pack(r1, &buf1)
	if err != nil {
		t.Fatal(err)
	}
	i2, err := Pack(r2, &buf2)
	if err != nil {
		t.Fatal(err)
	}
	if i1.SHA256 != i2.SHA256 {
		t.Errorf("archive sha differs across identical content: %s vs %s", i1.SHA256, i2.SHA256)
	}
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Error("archive bytes differ for identical content")
	}
	if i1.Bytes != int64(buf1.Len()) {
		t.Errorf("reported bytes %d != actual %d", i1.Bytes, buf1.Len())
	}
}

func TestPack_RoundTrip(t *testing.T) {
	root := writePkg(t, map[string]string{
		"SKILL.md":       "---\nname: deploy\ndescription: d\n---\n# h\n",
		"scripts/run.sh": "#!/bin/sh\n",
		"中文/说明.md":       "你好\n",
	})
	// Make run.sh executable so we can assert the exec bit survives.
	if err := os.Chmod(filepath.Join(root, "scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := Pack(root, &buf); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "out")
	got, err := Unpack(bytes.NewReader(buf.Bytes()), dest)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"SKILL.md", "scripts/run.sh", "中文/说明.md"}
	if len(got) != len(want) {
		t.Fatalf("extracted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extracted[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Content + exec bit preserved.
	b, err := os.ReadFile(filepath.Join(dest, "中文/说明.md"))
	if err != nil || string(b) != "你好\n" {
		t.Errorf("unicode file content = %q, err %v", b, err)
	}
	info, err := os.Stat(filepath.Join(dest, "scripts/run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Error("exec bit not preserved on round-trip")
	}

	// The unpacked tree must fingerprint to the same content hash as
	// the original (proves Pack/Unpack is content-preserving).
	m1, _ := Generate(root)
	m2, _ := Generate(dest)
	if m1.ContentSHA256 != m2.ContentSHA256 {
		t.Errorf("content hash changed across round-trip: %s vs %s", m1.ContentSHA256, m2.ContentSHA256)
	}
}

func TestUnpack_RejectsPathEscape(t *testing.T) {
	// Hand-build a malicious tar.gz with a "../escape" entry.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("pwn"))
	tw.Close()
	gz.Close()

	_, err := Unpack(bytes.NewReader(buf.Bytes()), filepath.Join(t.TempDir(), "dest"))
	if err == nil {
		t.Fatal("Unpack accepted a path-escaping entry")
	}
}

func TestUnpack_RejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0o777}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	_, err := Unpack(bytes.NewReader(buf.Bytes()), filepath.Join(t.TempDir(), "dest"))
	if !errors.Is(err, ErrBadEntry) {
		t.Errorf("Unpack symlink err = %v, want ErrBadEntry", err)
	}
}

func TestPack_EmptyPackage(t *testing.T) {
	// A directory with only hidden/skipped files packs to a valid
	// (empty-entry) archive that round-trips to nothing.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	info, err := Pack(root, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if info.SHA256 == "" {
		t.Error("empty package should still have an archive sha")
	}
	dest := filepath.Join(t.TempDir(), "out")
	got, err := Unpack(bytes.NewReader(buf.Bytes()), dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("extracted %v, want empty", got)
	}
}
