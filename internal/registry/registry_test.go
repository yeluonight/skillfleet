package registry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := New(d, filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

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

func TestPublishFromDir_WritesVersionAndArchive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	src := writePkg(t, map[string]string{
		"SKILL.md":       "---\nname: deploy-helper\ndescription: deploys\n---\n# Deploy\n",
		"scripts/run.sh": "#!/bin/sh\necho hi\n",
	})

	v, err := s.PublishFromDir(ctx, src, PublishParams{
		Name: "deploy-helper", Kind: KindManual, VersionLabel: "v1",
	}, time.UnixMilli(1_000))
	if err != nil {
		t.Fatal(err)
	}
	if v.ID == "" || v.ContentSHA256 == "" {
		t.Fatalf("version not populated: %+v", v)
	}
	if v.Manifest.Name != "deploy-helper" {
		t.Errorf("manifest name = %q", v.Manifest.Name)
	}

	// Archive exists on disk at the store-relative path.
	abs := s.ArchivePath(v)
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("archive missing: %v", err)
	}

	// Row is retrievable via Get.
	got, err := s.Get(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentSHA256 != v.ContentSHA256 || got.Kind != KindManual {
		t.Errorf("Get mismatch: %+v", got)
	}
}

func TestPublishFromDir_DedupsIdenticalContent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	files := map[string]string{"SKILL.md": "---\nname: x\n---\nbody\n"}
	src1 := writePkg(t, files)
	src2 := writePkg(t, files) // identical content, different dir

	v1, err := s.PublishFromDir(ctx, src1, PublishParams{Name: "x", Kind: KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.PublishFromDir(ctx, src2, PublishParams{Name: "x", Kind: KindDraftPublish}, time.UnixMilli(2))
	if err != nil {
		t.Fatal(err)
	}
	if v1.ID != v2.ID {
		t.Errorf("identical content produced two versions: %s vs %s", v1.ID, v2.ID)
	}
	// Only one row.
	vs, err := s.ListByName(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Errorf("ListByName = %d versions, want 1 (dedup)", len(vs))
	}
}

func TestPublishFromDir_DifferentContentNewVersion(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	src1 := writePkg(t, map[string]string{"SKILL.md": "---\nname: x\n---\nA\n"})
	src2 := writePkg(t, map[string]string{"SKILL.md": "---\nname: x\n---\nB\n"})

	if _, err := s.PublishFromDir(ctx, src1, PublishParams{Name: "x", Kind: KindManual}, time.UnixMilli(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishFromDir(ctx, src2, PublishParams{Name: "x", Kind: KindDraftPublish}, time.UnixMilli(2)); err != nil {
		t.Fatal(err)
	}
	vs, err := s.ListByName(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("ListByName = %d, want 2", len(vs))
	}
	// Newest first.
	if vs[0].CreatedAt.Before(vs[1].CreatedAt) {
		t.Error("ListByName not ordered newest-first")
	}
}

func TestPublishFromDir_Validation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	src := writePkg(t, map[string]string{"SKILL.md": "---\nname: x\n---\n"})

	if _, err := s.PublishFromDir(ctx, src, PublishParams{Name: "", Kind: KindManual}, time.UnixMilli(1)); err != ErrEmptyName {
		t.Errorf("empty name err = %v, want ErrEmptyName", err)
	}
	if _, err := s.PublishFromDir(ctx, src, PublishParams{Name: "x", Kind: "bogus"}, time.UnixMilli(1)); err == nil {
		t.Error("bad kind accepted")
	}
}

func TestPublishFromDir_BaseVersionLink(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := writePkg(t, map[string]string{"SKILL.md": "---\nname: x\n---\nbase\n"})
	v1, err := s.PublishFromDir(ctx, base, PublishParams{Name: "x", Kind: KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	child := writePkg(t, map[string]string{"SKILL.md": "---\nname: x\n---\nchild\n"})
	v2, err := s.PublishFromDir(ctx, child, PublishParams{
		Name: "x", Kind: KindDraftPublish, BaseVersionID: v1.ID,
	}, time.UnixMilli(2))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, v2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseVersionID != v1.ID {
		t.Errorf("base_version_id = %q, want %q", got.BaseVersionID, v1.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get(context.Background(), "sv_nope"); err != ErrVersionNotFnd {
		t.Errorf("Get unknown err = %v, want ErrVersionNotFnd", err)
	}
}

func TestArchiveSharedAcrossNames(t *testing.T) {
	// Identical content under two different names dedups the archive
	// file (sha-named) but creates two distinct version rows.
	s := newStore(t)
	ctx := context.Background()
	files := map[string]string{"SKILL.md": "---\nname: shared\n---\nsame\n"}
	srcA := writePkg(t, files)
	srcB := writePkg(t, files)

	va, err := s.PublishFromDir(ctx, srcA, PublishParams{Name: "alpha", Kind: KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	vb, err := s.PublishFromDir(ctx, srcB, PublishParams{Name: "beta", Kind: KindManual}, time.UnixMilli(2))
	if err != nil {
		t.Fatal(err)
	}
	if va.ID == vb.ID {
		t.Fatal("different names should be different versions")
	}
	// Same archive file backs both.
	if va.PackagePath != vb.PackagePath {
		t.Errorf("archives differ: %s vs %s (identical content should share)", va.PackagePath, vb.PackagePath)
	}
	if _, err := os.Stat(s.ArchivePath(va)); err != nil {
		t.Errorf("shared archive missing: %v", err)
	}
}

// compile-time: ensure *sql.Rows and *sql.Row both satisfy scanner.
var _ scanner = (*sql.Row)(nil)
var _ scanner = (*sql.Rows)(nil)

func TestLatestVersionBySource(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// No version for the source yet → found=false, no error.
	if _, found, err := s.LatestVersionBySource(ctx, "src_x", KindUpstream); err != nil || found {
		t.Fatalf("empty: found=%v err=%v, want false/nil", found, err)
	}

	// Publish two upstream versions for src_x (older then newer) plus a
	// manual version and an upstream version for a DIFFERENT source, to
	// prove the query filters on both source_id and kind.
	older := publishUpstream(t, s, "skill-a", "src_x", "old content", time.UnixMilli(1_000))
	newer := publishUpstream(t, s, "skill-a", "src_x", "new content", time.UnixMilli(2_000))
	_ = publishUpstream(t, s, "skill-a", "src_other", "other source", time.UnixMilli(3_000))
	if _, err := s.PublishFromDir(ctx, writePkg(t, map[string]string{
		"SKILL.md": "---\nname: skill-a\n---\nmanual\n",
	}), PublishParams{Name: "skill-a", Kind: KindManual, SourceID: "src_x"}, time.UnixMilli(4_000)); err != nil {
		t.Fatal(err)
	}

	got, found, err := s.LatestVersionBySource(ctx, "src_x", KindUpstream)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want true/nil", found, err)
	}
	if got.ID != newer.ID {
		t.Errorf("latest = %q, want newer %q (not older %q)", got.ID, newer.ID, older.ID)
	}
	if got.ContentSHA256 != newer.ContentSHA256 {
		t.Errorf("content sha = %q, want %q", got.ContentSHA256, newer.ContentSHA256)
	}
}

// publishUpstream is a registry_test helper: publishes a single-file
// upstream version with the given body so each call hashes distinctly.
func publishUpstream(t *testing.T, s *Store, name, sourceID, body string, now time.Time) Version {
	t.Helper()
	v, err := s.PublishFromFiles(context.Background(),
		[]InMemoryFile{{Path: "SKILL.md", Content: []byte("---\nname: " + name + "\ndescription: x\n---\n" + body + "\n")}},
		PublishParams{Name: name, Kind: KindUpstream, SourceID: sourceID}, now)
	if err != nil {
		t.Fatalf("publishUpstream: %v", err)
	}
	return v
}

func TestVersionSeqIncrementsPerSkill(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	pub := func(name, body string, ms int64) Version {
		src := writePkg(t, map[string]string{
			"SKILL.md": "---\nname: " + name + "\ndescription: d\n---\n# " + name + "\n" + body,
		})
		v, err := s.PublishFromDir(ctx, src, PublishParams{Name: name, Kind: KindManual}, time.UnixMilli(ms))
		if err != nil {
			t.Fatalf("publish %s: %v", name, err)
		}
		return v
	}

	a1 := pub("alpha", "one", 1_000)
	a2 := pub("alpha", "two", 2_000)
	b1 := pub("beta", "one", 3_000)
	if a1.VersionSeq != 1 || a2.VersionSeq != 2 {
		t.Errorf("alpha seqs = %d, %d; want 1, 2", a1.VersionSeq, a2.VersionSeq)
	}
	if b1.VersionSeq != 1 {
		t.Errorf("beta seq = %d; want 1 (per-skill counter)", b1.VersionSeq)
	}

	// current_version_id follows latest by default.
	cur, err := s.CurrentVersionID(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if cur != a2.ID {
		t.Errorf("current = %q; want latest %q", cur, a2.ID)
	}

	// Pin an explicit current; reads reflect it.
	if err := s.SetCurrentVersion(ctx, "alpha", a1.ID, time.UnixMilli(4_000)); err != nil {
		t.Fatalf("set current: %v", err)
	}
	cur, _ = s.CurrentVersionID(ctx, "alpha")
	if cur != a1.ID {
		t.Errorf("current after pin = %q; want %q", cur, a1.ID)
	}

	// Pinning a version from another skill is rejected.
	if err := s.SetCurrentVersion(ctx, "alpha", b1.ID, time.UnixMilli(5_000)); err == nil {
		t.Error("expected error pinning cross-skill version")
	}
}
