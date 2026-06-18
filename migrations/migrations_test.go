package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yeluonight/skillfleet/internal/db"
)

func TestLoadAllSorting(t *testing.T) {
	fs := fstest.MapFS{
		"0003_third.sql":  {Data: []byte("CREATE TABLE c (id INTEGER PRIMARY KEY);")},
		"0001_first.sql":  {Data: []byte("CREATE TABLE a (id INTEGER PRIMARY KEY);")},
		"0002_second.sql": {Data: []byte("CREATE TABLE b (id INTEGER PRIMARY KEY);")},
		"README.txt":      {Data: []byte("ignored")},
	}
	got, err := LoadAll(fs)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, m := range got {
		if m.Version != i+1 {
			t.Errorf("got[%d].Version = %d, want %d", i, m.Version, i+1)
		}
	}
}

func TestLoadAllRejectsGap(t *testing.T) {
	fs := fstest.MapFS{
		"0001_first.sql": {Data: []byte("--")},
		"0003_third.sql": {Data: []byte("--")},
	}
	_, err := LoadAll(fs)
	if err == nil || !strings.Contains(err.Error(), "gap at version 2") {
		t.Errorf("err = %v, want gap-at-version-2 error", err)
	}
}

func TestLoadAllRejectsDuplicate(t *testing.T) {
	fs := fstest.MapFS{
		"0001_first.sql":   {Data: []byte("--")},
		"0001_another.sql": {Data: []byte("--")},
	}
	_, err := LoadAll(fs)
	if err == nil || !strings.Contains(err.Error(), "duplicate version 1") {
		t.Errorf("err = %v, want duplicate-version-1 error", err)
	}
}

func TestLoadAllRejectsBadFilename(t *testing.T) {
	fs := fstest.MapFS{
		"junk.sql": {Data: []byte("--")},
	}
	_, err := LoadAll(fs)
	if err == nil || !strings.Contains(err.Error(), "filename must match") {
		t.Errorf("err = %v, want filename-format error", err)
	}
}

func TestApplyAppliesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	ctx := context.Background()

	d, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	fs := fstest.MapFS{
		"0001_init.sql":      {Data: []byte("CREATE TABLE marker (id INTEGER PRIMARY KEY) STRICT;")},
		"0002_add_value.sql": {Data: []byte("ALTER TABLE marker ADD COLUMN note TEXT;")},
	}

	// First run: applies both.
	res, err := Apply(ctx, d, fs)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if res.AppliedCount != 2 || res.EndVersion != 2 || res.StartVersion != 0 {
		t.Errorf("first run result = %+v", res)
	}

	// Second run: no-op.
	res2, err := Apply(ctx, d, fs)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if res2.AppliedCount != 0 || res2.EndVersion != 2 || res2.StartVersion != 2 {
		t.Errorf("second run result = %+v", res2)
	}

	// schema_migrations has the rows we expect.
	rows, err := d.Query(`SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	var names []string
	for rows.Next() {
		var v int
		var n string
		if err := rows.Scan(&v, &n); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
		names = append(names, n)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Errorf("versions = %v", versions)
	}
	if names[0] != "init" || names[1] != "add_value" {
		t.Errorf("names = %v", names)
	}
}

func TestApplyDetectsTamperedFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "tamper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	original := fstest.MapFS{
		"0001_init.sql": {Data: []byte("CREATE TABLE t (id INTEGER PRIMARY KEY) STRICT;")},
	}
	if _, err := Apply(ctx, d, original); err != nil {
		t.Fatal(err)
	}

	tampered := fstest.MapFS{
		"0001_init.sql": {Data: []byte("CREATE TABLE t (id INTEGER PRIMARY KEY, extra TEXT) STRICT;")},
	}
	_, err = Apply(ctx, d, tampered)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("err = %v, want checksum-mismatch error", err)
	}
}

func TestApplyRollsBackBadSQL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "bad.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	fs := fstest.MapFS{
		"0001_init.sql": {Data: []byte("CREATE TABLE good (id INTEGER PRIMARY KEY) STRICT;")},
		"0002_bad.sql":  {Data: []byte("THIS IS NOT VALID SQL;")},
	}
	res, err := Apply(ctx, d, fs)
	if err == nil {
		t.Fatal("expected error from bad SQL")
	}
	// 0001 should have applied; 0002 rolled back.
	if res.EndVersion != 1 {
		t.Errorf("EndVersion = %d, want 1 (only first migration succeeded)", res.EndVersion)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("schema_migrations has row for failed migration, count=%d", count)
	}
}

// TestEmbeddedLoadable ensures the //go:embed directive packs the real
// migrations directory at build time. The test is brittle on purpose: if
// the file layout drifts, the embed fails and this test catches it.
func TestEmbeddedLoadable(t *testing.T) {
	ms, err := LoadAll(Embedded())
	if err != nil {
		t.Fatalf("LoadAll(Embedded): %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("Embedded() returned no migrations")
	}
	if ms[0].Version != 1 || ms[0].Name != "init" {
		t.Errorf("first embedded migration = %d_%s, want 1_init", ms[0].Version, ms[0].Name)
	}
}
