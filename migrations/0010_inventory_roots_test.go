package migrations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestInventoryRootsMigration covers phase-11-t3's 0010 migration: candidate
// roots are stored as an optional JSON blob on the latest inventory run.
func TestInventoryRootsMigration(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "inventory_roots.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('dev_roots', 'host', 'approved', 1)`)
	mustExec(t, d, `
		INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, agent_version, roots_json, created_at)
		VALUES ('inv_roots', 'dev_roots', 1, 0, 0, 'test', '[{"tool_key":"codex","scope":"user","path":"/home/me/.agents/skills"}]', 1)
	`)

	var rootsJSON string
	if err := d.QueryRowContext(ctx, `SELECT roots_json FROM inventory_runs WHERE id='inv_roots'`).Scan(&rootsJSON); err != nil {
		t.Fatal(err)
	}
	if rootsJSON == "" {
		t.Fatal("roots_json should round-trip")
	}

	var recorded int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=10 AND name='inventory_roots'`,
	).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations missing 10_inventory_roots row, count=%d", recorded)
	}
}

func TestInventoryRootsMigration_PreservesExistingRuns(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "inventory_roots_preserve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	all, err := LoadAll(Embedded())
	if err != nil {
		t.Fatal(err)
	}
	var addRoots Migration
	for _, m := range all {
		if m.Version == 10 {
			addRoots = m
			continue
		}
		if m.Version < 10 {
			if _, err := d.ExecContext(ctx, m.SQL); err != nil {
				t.Fatalf("apply %04d_%s: %v", m.Version, m.Name, err)
			}
		}
	}
	if addRoots.SQL == "" {
		t.Fatal("migration 0010 not found")
	}

	mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('dev_old', 'host', 'approved', 1)`)
	mustExec(t, d, `INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, created_at) VALUES ('inv_old', 'dev_old', 1, 0, 0, 1)`)

	if _, err := d.ExecContext(ctx, addRoots.SQL); err != nil {
		t.Fatalf("apply 0010: %v", err)
	}

	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM inventory_runs WHERE id='inv_old' AND roots_json IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("existing inventory run should survive with NULL roots_json, count=%d", n)
	}
}
