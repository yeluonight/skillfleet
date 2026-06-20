package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestRegistrySchema applies the embedded migrations and asserts the
// three phase-4-t4 registry tables, their CHECKs (version_kind,
// status, is_binary), the base_version self-reference behaviour, the
// draft->files cascade, and the (draft_id, path) uniqueness guard.
func TestRegistrySchema(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	t.Run("tables", func(t *testing.T) {
		for _, name := range []string{"skill_versions", "skill_drafts", "skill_draft_files"} {
			var got string
			if err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name,
			).Scan(&got); err != nil {
				t.Errorf("table %q missing: %v", name, err)
			}
		}
	})

	t.Run("indexes", func(t *testing.T) {
		for _, idx := range []string{
			"idx_skill_versions_name", "idx_skill_versions_source", "idx_skill_versions_content",
			"idx_skill_drafts_status", "idx_skill_drafts_base",
			"idx_skill_draft_files_draft", "idx_skill_draft_files_draft_path",
		} {
			var got string
			if err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
			).Scan(&got); err != nil {
				t.Errorf("index %q missing: %v", idx, err)
			}
		}
	})

	t.Run("version_kind_check", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO skill_versions(id, name, version_kind, content_sha256, manifest_json, package_path, created_at)
			VALUES ('sv_bad', 'x', 'teleport', 'abc', '{}', 'packages/abc.tgz', 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("version_kind CHECK not enforced: err = %v", err)
		}
	})

	t.Run("insert_version_ok", func(t *testing.T) {
		mustExec(t, d, `
			INSERT INTO skill_versions(id, name, version_kind, content_sha256, manifest_json, package_path, created_at)
			VALUES ('sv1', 'deploy-helper', 'manual', 'sha1', '{"name":"deploy-helper"}', 'packages/sha1.tgz', 10)
		`)
	})

	t.Run("base_version_self_ref_set_null", func(t *testing.T) {
		mustExec(t, d, `
			INSERT INTO skill_versions(id, name, version_kind, base_version_id, content_sha256, manifest_json, package_path, created_at)
			VALUES ('sv2', 'deploy-helper', 'draft_publish', 'sv1', 'sha2', '{}', 'packages/sha2.tgz', 20)
		`)
		// Deleting the base nulls the child's base_version_id (SET NULL).
		mustExec(t, d, `DELETE FROM skill_versions WHERE id='sv1'`)
		var base any
		if err := d.QueryRowContext(ctx,
			`SELECT base_version_id FROM skill_versions WHERE id='sv2'`,
		).Scan(&base); err != nil {
			t.Fatal(err)
		}
		if base != nil {
			t.Errorf("base_version_id = %v, want NULL after base delete", base)
		}
	})

	t.Run("draft_status_check", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO skill_drafts(id, name, status, created_at, updated_at)
			VALUES ('d_bad', 'x', 'half-baked', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("draft status CHECK not enforced: err = %v", err)
		}
	})

	t.Run("is_binary_check", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO skill_drafts(id, name, status, created_at, updated_at) VALUES ('d1', 'x', 'open', 1, 1)`)
		_, err := d.ExecContext(ctx, `
			INSERT INTO skill_draft_files(id, draft_id, path, is_binary, updated_at)
			VALUES ('f_bad', 'd1', 'SKILL.md', 5, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("is_binary CHECK not enforced: err = %v", err)
		}
	})

	t.Run("draft_file_unique_path", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO skill_drafts(id, name, status, created_at, updated_at) VALUES ('d2', 'x', 'open', 1, 1)`)
		mustExec(t, d, `INSERT INTO skill_draft_files(id, draft_id, path, content_text, updated_at) VALUES ('f1', 'd2', 'SKILL.md', 'a', 1)`)
		_, err := d.ExecContext(ctx,
			`INSERT INTO skill_draft_files(id, draft_id, path, content_text, updated_at) VALUES ('f2', 'd2', 'SKILL.md', 'b', 1)`)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Errorf("duplicate (draft, path) not rejected: err = %v", err)
		}
	})

	t.Run("cascade_draft_to_files", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO skill_drafts(id, name, status, created_at, updated_at) VALUES ('d3', 'x', 'open', 1, 1)`)
		mustExec(t, d, `INSERT INTO skill_draft_files(id, draft_id, path, content_text, updated_at) VALUES ('f3', 'd3', 'a.md', 'a', 1)`)
		mustExec(t, d, `DELETE FROM skill_drafts WHERE id='d3'`)
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM skill_draft_files WHERE draft_id='d3'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("draft cascade left %d file rows", n)
		}
	})

	t.Run("schema_migrations_recorded", func(t *testing.T) {
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version=6 AND name='registry'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("schema_migrations missing 6_registry row, count=%d", n)
		}
	})
}
