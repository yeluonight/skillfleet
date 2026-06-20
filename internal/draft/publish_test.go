package draft

import (
	"context"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/registry"
)

func TestValidate_CleanDraftHasNoErrors(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "ok-skill"}, time.UnixMilli(1))
	// Seeded SKILL.md has name but no description → one warning, no errors.
	issues, err := ds.Validate(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if HasErrors(issues) {
		t.Errorf("clean draft has errors: %+v", issues)
	}
}

func TestValidate_NameMismatchIsError(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "real-name"}, time.UnixMilli(1))
	// Overwrite SKILL.md with a mismatched frontmatter name.
	if _, err := ds.PutFile(ctx, d.ID, "SKILL.md",
		[]byte("---\nname: wrong-name\n---\n# x\n"), time.UnixMilli(2)); err != nil {
		t.Fatal(err)
	}
	issues, err := ds.Validate(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !HasErrors(issues) {
		t.Fatalf("expected name_mismatch error, got %+v", issues)
	}
	if !hasCode(issues, "name_mismatch") {
		t.Errorf("issues = %+v, want name_mismatch", issues)
	}
}

func TestValidate_MissingSkillMD(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "x"}, time.UnixMilli(1))
	if err := ds.DeleteFile(ctx, d.ID, "SKILL.md", time.UnixMilli(2)); err != nil {
		t.Fatal(err)
	}
	issues, _ := ds.Validate(ctx, d.ID)
	if !hasCode(issues, "missing_skill_md") {
		t.Errorf("issues = %+v, want missing_skill_md", issues)
	}
}

func TestPublish_HappyPath(t *testing.T) {
	ds, reg := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "publish-me"}, time.UnixMilli(1))
	// Add a description so there are zero warnings, and a second file.
	ds.PutFile(ctx, d.ID, "SKILL.md", []byte("---\nname: publish-me\ndescription: yes\n---\n# Go\n"), time.UnixMilli(2))
	ds.PutFile(ctx, d.ID, "scripts/run.sh", []byte("#!/bin/sh\n"), time.UnixMilli(3))

	res, err := ds.Publish(ctx, d.ID, time.UnixMilli(5000))
	if err != nil {
		t.Fatalf("publish: %v (%+v)", err, res.Issues)
	}
	if res.Version.ID == "" || res.Version.Kind != "draft_publish" {
		t.Errorf("version = %+v", res.Version)
	}
	if res.Version.Manifest.FileCount != 2 {
		t.Errorf("file count = %d, want 2", res.Version.Manifest.FileCount)
	}
	// Version is in the registry under the skill name.
	vs, _ := reg.ListByName(ctx, "publish-me")
	if len(vs) != 1 {
		t.Errorf("registry has %d versions, want 1", len(vs))
	}
	// Draft is now published; further edits rejected.
	if _, err := ds.PutFile(ctx, d.ID, "x.md", []byte("y"), time.UnixMilli(6000)); err != ErrNotOpen {
		t.Errorf("edit after publish err = %v, want ErrNotOpen", err)
	}
}

func TestPublish_BlockedByValidationError(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "right"}, time.UnixMilli(1))
	ds.PutFile(ctx, d.ID, "SKILL.md", []byte("---\nname: wrong\n---\nx\n"), time.UnixMilli(2))

	_, err := ds.Publish(ctx, d.ID, time.UnixMilli(3))
	if err != ErrValidationFailed {
		t.Errorf("err = %v, want ErrValidationFailed", err)
	}
	// Draft remains open (publish was blocked).
	loaded, _ := ds.Load(ctx, d.ID)
	if loaded.Status != StatusOpen {
		t.Errorf("status = %q, want open after blocked publish", loaded.Status)
	}
}

func TestPublish_ForkRetainsBase(t *testing.T) {
	ds, reg := newStores(t)
	ctx := context.Background()
	base, err := reg.PublishFromFiles(ctx, []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: chain\ndescription: d\n---\nA\n")},
	}, registry.PublishParams{Name: "chain", Kind: registry.KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ds.Create(ctx, CreateParams{BaseVersionID: base.ID}, time.UnixMilli(2))
	ds.PutFile(ctx, d.ID, "SKILL.md", []byte("---\nname: chain\ndescription: d\n---\nB\n"), time.UnixMilli(3))

	res, err := ds.Publish(ctx, d.ID, time.UnixMilli(4))
	if err != nil {
		t.Fatal(err)
	}
	if res.Version.BaseVersionID != base.ID {
		t.Errorf("base = %q, want %q", res.Version.BaseVersionID, base.ID)
	}
}

func TestPublish_NotOpen(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "once"}, time.UnixMilli(1))
	ds.PutFile(ctx, d.ID, "SKILL.md", []byte("---\nname: once\ndescription: d\n---\nx\n"), time.UnixMilli(2))
	if _, err := ds.Publish(ctx, d.ID, time.UnixMilli(3)); err != nil {
		t.Fatal(err)
	}
	// Second publish on a published draft → ErrNotOpen.
	if _, err := ds.Publish(ctx, d.ID, time.UnixMilli(4)); err != ErrNotOpen {
		t.Errorf("re-publish err = %v, want ErrNotOpen", err)
	}
}

func hasCode(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
