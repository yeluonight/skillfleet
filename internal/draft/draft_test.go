package draft

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/migrations"
)

func newStores(t *testing.T) (*Store, *registry.Store) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "draft.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	storeDir := filepath.Join(t.TempDir(), "store")
	reg, err := registry.New(d, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := New(d, reg, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	return ds, reg
}

func TestCreate_BlankSeedsSkillMD(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()

	d, err := ds.Create(ctx, CreateParams{Name: "fresh-skill", Title: "My Draft"}, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusOpen || d.Name != "fresh-skill" || d.Title != "My Draft" {
		t.Errorf("draft = %+v", d)
	}
	if len(d.Files) != 1 || d.Files[0].Path != skill.SkillMDName {
		t.Fatalf("seed files = %+v, want one SKILL.md", d.Files)
	}
	if !strings.Contains(string(d.Files[0].Content), "name: fresh-skill") {
		t.Errorf("SKILL.md stub = %q", d.Files[0].Content)
	}
	if d.Files[0].IsBinary {
		t.Error("SKILL.md should be text")
	}
}

func TestCreate_BlankRequiresName(t *testing.T) {
	ds, _ := newStores(t)
	if _, err := ds.Create(context.Background(), CreateParams{}, time.UnixMilli(1)); err != ErrEmptyName {
		t.Errorf("err = %v, want ErrEmptyName", err)
	}
}

func TestCreate_ForkFromVersion(t *testing.T) {
	ds, reg := newStores(t)
	ctx := context.Background()

	// Publish a base version with multiple files (incl. binary).
	base, err := reg.PublishFromFiles(ctx, []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: forked\n---\n# Forked\n")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
		{Path: "img.bin", Content: []byte{0x00, 0x01, 0x02}},
	}, registry.PublishParams{Name: "forked", Kind: registry.KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}

	d, err := ds.Create(ctx, CreateParams{BaseVersionID: base.ID}, time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "forked" {
		t.Errorf("name = %q, want inherited 'forked'", d.Name)
	}
	if d.BaseVersionID != base.ID {
		t.Errorf("base = %q, want %q", d.BaseVersionID, base.ID)
	}
	if len(d.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(d.Files))
	}
	// Binary file recorded as binary.
	byPath := map[string]File{}
	for _, f := range d.Files {
		byPath[f.Path] = f
	}
	if !byPath["img.bin"].IsBinary {
		t.Error("img.bin should be binary")
	}
	if byPath["SKILL.md"].IsBinary {
		t.Error("SKILL.md should be text")
	}
}

func TestCreate_ForkUnknownBase(t *testing.T) {
	ds, _ := newStores(t)
	_, err := ds.Create(context.Background(), CreateParams{BaseVersionID: "sv_ghost"}, time.UnixMilli(1))
	if err == nil {
		t.Fatal("expected error for unknown base version")
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	created, err := ds.Create(ctx, CreateParams{Name: "rt", CreatedBy: "usr_1"}, time.UnixMilli(5000))
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := ds.Load(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != created.ID || loaded.Name != "rt" || loaded.CreatedBy != "usr_1" {
		t.Errorf("loaded = %+v", loaded)
	}
	if len(loaded.Files) != 1 || loaded.Files[0].Path != skill.SkillMDName {
		t.Errorf("loaded files = %+v", loaded.Files)
	}
	if !strings.Contains(string(loaded.Files[0].Content), "name: rt") {
		t.Errorf("loaded content = %q", loaded.Files[0].Content)
	}
}

func TestLoad_NotFound(t *testing.T) {
	ds, _ := newStores(t)
	if _, err := ds.Load(context.Background(), "dft_nope"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreate_ForkBinaryBlobOnDisk(t *testing.T) {
	ds, reg := newStores(t)
	ctx := context.Background()
	base, err := reg.PublishFromFiles(ctx, []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: b\n---\nx\n")},
		{Path: "data.bin", Content: []byte{0x00, 0xff, 0x00, 0xff}},
	}, registry.PublishParams{Name: "b", Kind: registry.KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ds.Create(ctx, CreateParams{BaseVersionID: base.ID}, time.UnixMilli(2))
	if err != nil {
		t.Fatal(err)
	}
	// The binary file's content is not inlined on Load.
	loaded, err := ds.Load(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range loaded.Files {
		if f.Path == "data.bin" {
			if !f.IsBinary {
				t.Error("data.bin not marked binary")
			}
			if f.Content != nil {
				t.Error("binary content should not be inlined on Load")
			}
		}
	}
}
