package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestInventorySchema applies the embedded migrations against a fresh
// database and asserts the four phase-3-t8 tables, their key columns,
// the scope / effective_state / has_skill_md CHECKs, the cascading
// deletes, and the project uniqueness guard all landed correctly.
func TestInventorySchema(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// A device to anchor the foreign keys.
	mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('dev1', 'host', 'approved', 1)`)

	t.Run("tables", func(t *testing.T) {
		for _, name := range []string{"projects", "tool_instances", "inventory_runs", "discovered_skills"} {
			var got string
			err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name,
			).Scan(&got)
			if err != nil {
				t.Errorf("table %q missing: %v", name, err)
			}
		}
	})

	t.Run("indexes", func(t *testing.T) {
		want := []string{
			"idx_projects_device",
			"idx_projects_device_path",
			"idx_inventory_runs_device",
			"idx_tool_instances_device",
			"idx_tool_instances_run",
			"idx_discovered_skills_device",
			"idx_discovered_skills_run",
			"idx_discovered_skills_tool",
		}
		for _, idx := range want {
			var got string
			err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
			).Scan(&got)
			if err != nil {
				t.Errorf("index %q missing: %v", idx, err)
			}
		}
	})

	t.Run("project_unique_device_path", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO projects(id, device_id, name, path, created_at) VALUES ('p1', 'dev1', 'proj', '/repo', 1)`)
		_, err := d.ExecContext(ctx,
			`INSERT INTO projects(id, device_id, name, path, created_at) VALUES ('p2', 'dev1', 'dup', '/repo', 2)`)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Errorf("duplicate (device, path) not rejected: err = %v", err)
		}
	})

	t.Run("scope_check", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, created_at) VALUES ('r_s', 'dev1', 1, 0, 0, 1)`)
		_, err := d.ExecContext(ctx, `
			INSERT INTO tool_instances(id, device_id, run_id, tool_key, display_name, scope, root_id, root_path, last_scanned_at)
			VALUES ('ti_bad', 'dev1', 'r_s', 'claude-code', 'Claude Code', 'galaxy', 'r', '/p', 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("scope CHECK not enforced: err = %v", err)
		}
	})

	t.Run("effective_state_check", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, created_at) VALUES ('r_es', 'dev1', 1, 0, 0, 1)`)
		mustExec(t, d, `
			INSERT INTO tool_instances(id, device_id, run_id, tool_key, display_name, scope, root_id, root_path, last_scanned_at)
			VALUES ('ti_es', 'dev1', 'r_es', 'claude-code', 'Claude Code', 'user', 'r', '/p', 1)
		`)
		_, err := d.ExecContext(ctx, `
			INSERT INTO discovered_skills(id, device_id, run_id, tool_instance_id, tool_key, scope, name, skill_path, has_skill_md, effective_state, created_at)
			VALUES ('ds_bad', 'dev1', 'r_es', 'ti_es', 'claude-code', 'user', 'x', '/p/x', 1, 'maybe', 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("effective_state CHECK not enforced: err = %v", err)
		}
	})

	t.Run("has_skill_md_check", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, created_at) VALUES ('r_h', 'dev1', 1, 0, 0, 1)`)
		mustExec(t, d, `
			INSERT INTO tool_instances(id, device_id, run_id, tool_key, display_name, scope, root_id, root_path, last_scanned_at)
			VALUES ('ti_h', 'dev1', 'r_h', 'claude-code', 'Claude Code', 'user', 'r', '/p', 1)
		`)
		_, err := d.ExecContext(ctx, `
			INSERT INTO discovered_skills(id, device_id, run_id, tool_instance_id, tool_key, scope, name, skill_path, has_skill_md, effective_state, created_at)
			VALUES ('ds_h', 'dev1', 'r_h', 'ti_h', 'claude-code', 'user', 'x', '/p/x', 7, 'on', 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("has_skill_md CHECK not enforced: err = %v", err)
		}
	})

	t.Run("cascade_run_to_skills", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, created_at) VALUES ('r_c', 'dev1', 1, 1, 1, 1)`)
		mustExec(t, d, `
			INSERT INTO tool_instances(id, device_id, run_id, tool_key, display_name, scope, root_id, root_path, last_scanned_at)
			VALUES ('ti_c', 'dev1', 'r_c', 'codex', 'Codex', 'user', 'r', '/p', 1)
		`)
		mustExec(t, d, `
			INSERT INTO discovered_skills(id, device_id, run_id, tool_instance_id, tool_key, scope, name, skill_path, has_skill_md, effective_state, created_at)
			VALUES ('ds_c', 'dev1', 'r_c', 'ti_c', 'codex', 'user', 'x', '/p/x', 1, 'on', 1)
		`)
		mustExec(t, d, `DELETE FROM inventory_runs WHERE id='r_c'`)
		for _, q := range []string{
			`SELECT COUNT(*) FROM tool_instances WHERE run_id='r_c'`,
			`SELECT COUNT(*) FROM discovered_skills WHERE run_id='r_c'`,
		} {
			var n int
			if err := d.QueryRowContext(ctx, q).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("run cascade left rows: %q -> %d", q, n)
			}
		}
	})

	t.Run("cascade_device_to_inventory", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('dev2', 'host', 'approved', 1)`)
		mustExec(t, d, `INSERT INTO projects(id, device_id, name, path, created_at) VALUES ('p_d2', 'dev2', 'proj', '/r2', 1)`)
		mustExec(t, d, `INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, created_at) VALUES ('r_d2', 'dev2', 1, 0, 0, 1)`)
		mustExec(t, d, `DELETE FROM devices WHERE id='dev2'`)
		for _, q := range []string{
			`SELECT COUNT(*) FROM projects WHERE device_id='dev2'`,
			`SELECT COUNT(*) FROM inventory_runs WHERE device_id='dev2'`,
		} {
			var n int
			if err := d.QueryRowContext(ctx, q).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("device cascade left rows: %q -> %d", q, n)
			}
		}
	})

	t.Run("schema_migrations_recorded", func(t *testing.T) {
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version=5 AND name='inventory'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("schema_migrations missing 5_inventory row, count=%d", n)
		}
	})
}
