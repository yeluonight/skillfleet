package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestSourcesSchema applies the embedded migrations and asserts the
// phase-6-t1 skill_sources table: its presence, the source_type and
// ref_type CHECK sets, a clean insert of a github_repo binding, and
// that the migration is recorded in schema_migrations.
func TestSourcesSchema(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "sources.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	t.Run("table", func(t *testing.T) {
		var got string
		if err := d.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name='skill_sources'`,
		).Scan(&got); err != nil {
			t.Errorf("skill_sources missing: %v", err)
		}
	})

	t.Run("index", func(t *testing.T) {
		var got string
		if err := d.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_skill_sources_type'`,
		).Scan(&got); err != nil {
			t.Errorf("idx_skill_sources_type missing: %v", err)
		}
	})

	t.Run("source_type_check", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO skill_sources(id, name, source_type, created_at, updated_at)
			VALUES ('src_bad', 'x', 'svn_checkout', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("source_type CHECK not enforced: err = %v", err)
		}
	})

	t.Run("ref_type_check", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO skill_sources(id, name, source_type, ref_type, created_at, updated_at)
			VALUES ('src_bad2', 'x', 'git_repo', 'snapshot', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("ref_type CHECK not enforced: err = %v", err)
		}
	})

	t.Run("ref_type_null_ok", func(t *testing.T) {
		// ref_type is optional (a freshly-bound source may not have one yet).
		mustExec(t, d, `
			INSERT INTO skill_sources(id, name, source_type, created_at, updated_at)
			VALUES ('src_nullref', 'x', 'webui_created', 1, 1)
		`)
	})

	t.Run("insert_github_repo_ok", func(t *testing.T) {
		mustExec(t, d, `
			INSERT INTO skill_sources(
				id, name, source_type, source_url, provider, owner, repo,
				ref_type, ref_name, subdir, last_remote_commit, created_at, updated_at)
			VALUES (
				'src1', 'deploy-helper upstream', 'github_repo',
				'https://github.com/acme/skills', 'github', 'acme', 'skills',
				'branch', 'main', 'deploy-helper', 'abc123', 100, 100)
		`)
		var typ, url, subdir string
		if err := d.QueryRowContext(ctx,
			`SELECT source_type, source_url, subdir FROM skill_sources WHERE id='src1'`,
		).Scan(&typ, &url, &subdir); err != nil {
			t.Fatal(err)
		}
		if typ != "github_repo" || url != "https://github.com/acme/skills" || subdir != "deploy-helper" {
			t.Errorf("round-trip mismatch: type=%q url=%q subdir=%q", typ, url, subdir)
		}
	})

	t.Run("schema_migrations_recorded", func(t *testing.T) {
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version=7 AND name='sources'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("schema_migrations missing 7_sources row, count=%d", n)
		}
	})
}
