package source

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

// Fixed timestamp for deterministic tests. Keep it far from 1970 so
// zero-value time.Time never accidentally passes a check.
var fixedNow = time.UnixMilli(1_700_000_000_000) // ≈ 2023-11-14

func openTestDB(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "source_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatalf("migrations.Apply: %v", err)
	}
	return New(d), ctx
}

// fullSource returns a Source with every field populated so we can
// exercise the full round-trip (including nullable columns).
func fullSource() Source {
	return Source{
		Name:             "test-source",
		Type:             TypeGitHubRepo,
		URL:              "https://github.com/example/skills",
		Provider:         "github",
		Owner:            "example",
		Repo:             "skills",
		RefType:          RefBranch,
		RefName:          "main",
		Subdir:           "deploy-helper",
		LastCheckedAt:    fixedNow,
		LastRemoteCommit: "abc123def456",
		ConfigJSON:       `{"note":"test"}`,
	}
}

func TestCreate_Get_RoundTrip(t *testing.T) {
	store, ctx := openTestDB(t)
	input := fullSource()
	created, err := store.Create(ctx, input, fixedNow)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if !created.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v", created.CreatedAt, fixedNow)
	}
	if !created.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", created.UpdatedAt, fixedNow)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Name != input.Name {
		t.Errorf("Name = %q, want %q", got.Name, input.Name)
	}
	if got.Type != input.Type {
		t.Errorf("Type = %q, want %q", got.Type, input.Type)
	}
	if got.URL != input.URL {
		t.Errorf("URL = %q, want %q", got.URL, input.URL)
	}
	if got.Provider != input.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, input.Provider)
	}
	if got.Owner != input.Owner {
		t.Errorf("Owner = %q, want %q", got.Owner, input.Owner)
	}
	if got.Repo != input.Repo {
		t.Errorf("Repo = %q, want %q", got.Repo, input.Repo)
	}
	if got.RefType != input.RefType {
		t.Errorf("RefType = %q, want %q", got.RefType, input.RefType)
	}
	if got.RefName != input.RefName {
		t.Errorf("RefName = %q, want %q", got.RefName, input.RefName)
	}
	if got.Subdir != input.Subdir {
		t.Errorf("Subdir = %q, want %q", got.Subdir, input.Subdir)
	}
	if !got.LastCheckedAt.Equal(input.LastCheckedAt) {
		t.Errorf("LastCheckedAt = %v, want %v", got.LastCheckedAt, input.LastCheckedAt)
	}
	if got.LastRemoteCommit != input.LastRemoteCommit {
		t.Errorf("LastRemoteCommit = %q, want %q", got.LastRemoteCommit, input.LastRemoteCommit)
	}
	if got.ConfigJSON != input.ConfigJSON {
		t.Errorf("ConfigJSON = %q, want %q", got.ConfigJSON, input.ConfigJSON)
	}
	if !got.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, fixedNow)
	}
	if !got.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, fixedNow)
	}
}

func TestCreate_EmptyName(t *testing.T) {
	store, ctx := openTestDB(t)
	_, err := store.Create(ctx, Source{Name: "", Type: TypeGitRepo}, fixedNow)
	if !errors.Is(err, ErrEmptyName) {
		t.Errorf("err = %v, want ErrEmptyName", err)
	}
}

func TestCreate_BadType(t *testing.T) {
	store, ctx := openTestDB(t)
	_, err := store.Create(ctx, Source{Name: "x", Type: "not-a-type"}, fixedNow)
	if !errors.Is(err, ErrBadType) {
		t.Errorf("err = %v, want ErrBadType", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	store, ctx := openTestDB(t)
	_, err := store.Get(ctx, "src_nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdate_Then_Get(t *testing.T) {
	store, ctx := openTestDB(t)
	created, err := store.Create(ctx, fullSource(), fixedNow)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	later := fixedNow.Add(1 * time.Hour)
	created.Name = "renamed"
	created.URL = "https://github.com/other/repo"
	created.Provider = "gitlab"
	created.Owner = "other"
	created.Repo = "repo"
	created.RefType = RefTag
	created.RefName = "v2.0"
	created.Subdir = "other-skill"
	created.LastCheckedAt = later
	created.LastRemoteCommit = "def789"
	created.ConfigJSON = `{"updated":true}`

	if err := store.Update(ctx, created, later); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want %q", got.Name, "renamed")
	}
	if got.URL != "https://github.com/other/repo" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Provider != "gitlab" {
		t.Errorf("Provider = %q", got.Provider)
	}
	if got.Owner != "other" {
		t.Errorf("Owner = %q", got.Owner)
	}
	if got.Repo != "repo" {
		t.Errorf("Repo = %q", got.Repo)
	}
	if got.RefType != RefTag {
		t.Errorf("RefType = %q", got.RefType)
	}
	if got.RefName != "v2.0" {
		t.Errorf("RefName = %q", got.RefName)
	}
	if got.Subdir != "other-skill" {
		t.Errorf("Subdir = %q", got.Subdir)
	}
	if !got.LastCheckedAt.Equal(later) {
		t.Errorf("LastCheckedAt = %v, want %v", got.LastCheckedAt, later)
	}
	if got.LastRemoteCommit != "def789" {
		t.Errorf("LastRemoteCommit = %q", got.LastRemoteCommit)
	}
	if got.ConfigJSON != `{"updated":true}` {
		t.Errorf("ConfigJSON = %q", got.ConfigJSON)
	}
	if !got.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, later)
	}
	// CreatedAt must not change.
	if !got.CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, fixedNow)
	}
}

func TestUpdateCheckCursor_OnlyChangesCursor(t *testing.T) {
	store, ctx := openTestDB(t)
	created, err := store.Create(ctx, fullSource(), fixedNow)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	later := fixedNow.Add(2 * time.Hour)
	newCommit := "cursor-commit-xyz"
	if err := store.UpdateCheckCursor(ctx, created.ID, newCommit, later); err != nil {
		t.Fatalf("UpdateCheckCursor: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after UpdateCheckCursor: %v", err)
	}

	// Cursor fields must change.
	if !got.LastCheckedAt.Equal(later) {
		t.Errorf("LastCheckedAt = %v, want %v", got.LastCheckedAt, later)
	}
	if got.LastRemoteCommit != newCommit {
		t.Errorf("LastRemoteCommit = %q, want %q", got.LastRemoteCommit, newCommit)
	}
	if !got.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, later)
	}

	// Everything else must be unchanged.
	if got.Name != created.Name {
		t.Errorf("Name changed: %q -> %q", created.Name, got.Name)
	}
	if got.URL != created.URL {
		t.Errorf("URL changed: %q -> %q", created.URL, got.URL)
	}
	if got.Provider != created.Provider {
		t.Errorf("Provider changed")
	}
	if got.Owner != created.Owner {
		t.Errorf("Owner changed")
	}
	if got.Repo != created.Repo {
		t.Errorf("Repo changed")
	}
	if got.RefType != created.RefType {
		t.Errorf("RefType changed")
	}
	if got.RefName != created.RefName {
		t.Errorf("RefName changed")
	}
	if got.Subdir != created.Subdir {
		t.Errorf("Subdir changed")
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt changed")
	}
	if got.ConfigJSON != created.ConfigJSON {
		t.Errorf("ConfigJSON changed")
	}
}

func TestDelete_Then_NotFound(t *testing.T) {
	store, ctx := openTestDB(t)
	created, err := store.Create(ctx, fullSource(), fixedNow)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(ctx, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}

	// Double-delete is also ErrNotFound.
	err = store.Delete(ctx, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Double Delete: err = %v, want ErrNotFound", err)
	}
}

func TestListAll_Ordering(t *testing.T) {
	store, ctx := openTestDB(t)

	nows := []time.Time{
		fixedNow,
		fixedNow.Add(1 * time.Second),
		fixedNow.Add(2 * time.Second),
	}

	names := []string{"a", "b", "c"}
	var ids []string
	for i, name := range names {
		s := fullSource()
		s.Name = name
		created, err := store.Create(ctx, s, nows[i])
		if err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
		ids = append(ids, created.ID)
	}

	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAll len = %d, want 3", len(all))
	}
	for i, wantName := range names {
		if all[i].Name != wantName {
			t.Errorf("list[%d].Name = %q, want %q", i, all[i].Name, wantName)
		}
		if all[i].ID != ids[i] {
			t.Errorf("list[%d].ID = %q, want %q", i, all[i].ID, ids[i])
		}
	}
}

func TestCreate_NullableFieldsRoundTrip(t *testing.T) {
	// A source with all nullable fields at their zero values should
	// round-trip: stored as NULL, read back as "" / zero time.
	store, ctx := openTestDB(t)
	input := Source{
		Name: "bare-minimum",
		Type: TypeWebUICreated,
		// All nullable fields left at zero.
	}
	created, err := store.Create(ctx, input, fixedNow)
	if err != nil {
		t.Fatalf("Create bare: %v", err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get bare: %v", err)
	}
	if got.URL != "" {
		t.Errorf("URL = %q, want empty", got.URL)
	}
	if got.Provider != "" {
		t.Errorf("Provider = %q, want empty", got.Provider)
	}
	if got.Owner != "" {
		t.Errorf("Owner = %q, want empty", got.Owner)
	}
	if got.Repo != "" {
		t.Errorf("Repo = %q, want empty", got.Repo)
	}
	if got.RefType != "" {
		t.Errorf("RefType = %q, want empty", got.RefType)
	}
	if got.RefName != "" {
		t.Errorf("RefName = %q, want empty", got.RefName)
	}
	if got.Subdir != "" {
		t.Errorf("Subdir = %q, want empty", got.Subdir)
	}
	if !got.LastCheckedAt.IsZero() {
		t.Errorf("LastCheckedAt = %v, want zero", got.LastCheckedAt)
	}
	if got.LastRemoteCommit != "" {
		t.Errorf("LastRemoteCommit = %q, want empty", got.LastRemoteCommit)
	}
	if got.ConfigJSON != "" {
		t.Errorf("ConfigJSON = %q, want empty", got.ConfigJSON)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	store, ctx := openTestDB(t)
	err := store.Update(ctx, Source{ID: "src_nope", Name: "x"}, fixedNow)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateCheckCursor_NotFound(t *testing.T) {
	store, ctx := openTestDB(t)
	err := store.UpdateCheckCursor(ctx, "src_nope", "abc", fixedNow)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateCheckCursor_ZeroTime(t *testing.T) {
	// A zero checkedAt is a contract violation: it must be rejected, not
	// silently written as a negative epoch to the NOT NULL updated_at.
	store, ctx := openTestDB(t)
	created, err := store.Create(ctx, fullSource(), fixedNow)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.UpdateCheckCursor(ctx, created.ID, "abc", time.Time{}); !errors.Is(err, ErrZeroCheckTime) {
		t.Errorf("err = %v, want ErrZeroCheckTime", err)
	}
	// The row's updated_at must be untouched (still fixedNow).
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want unchanged %v", got.UpdatedAt, fixedNow)
	}
}

func TestIDGeneration(t *testing.T) {
	store, ctx := openTestDB(t)
	input := Source{Name: "auto-id", Type: TypeLocalImport}
	created, err := store.Create(ctx, input, fixedNow)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created.ID) < 4 {
		t.Fatalf("ID too short: %q", created.ID)
	}
	if created.ID[:4] != "src_" {
		t.Errorf("ID prefix = %q, want \"src_\"", created.ID[:4])
	}
}

func TestGetBySkillName(t *testing.T) {
	store, ctx := openTestDB(t)

	// Unbound name → ErrNotFound.
	if _, err := store.GetBySkillName(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbound GetBySkillName = %v, want ErrNotFound", err)
	}

	// Create a binding and read it back by skill name.
	src := fullSource()
	src.Name = "deploy-helper"
	if _, err := store.Create(ctx, src, fixedNow); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetBySkillName(ctx, "deploy-helper")
	if err != nil {
		t.Fatalf("GetBySkillName: %v", err)
	}
	if got.Name != "deploy-helper" || got.URL != src.URL {
		t.Errorf("got = %+v", got)
	}

	// If two rows ever share a name, the newest (by created_at) wins.
	newer := fullSource()
	newer.Name = "deploy-helper"
	newer.URL = "https://github.com/example/newer"
	if _, err := store.Create(ctx, newer, fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("Create newer: %v", err)
	}
	got2, err := store.GetBySkillName(ctx, "deploy-helper")
	if err != nil {
		t.Fatalf("GetBySkillName after second: %v", err)
	}
	if got2.URL != newer.URL {
		t.Errorf("GetBySkillName returned URL %q, want newest %q", got2.URL, newer.URL)
	}
}
