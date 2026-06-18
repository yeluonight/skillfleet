package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestDeploymentStateChangeMigration covers phase-9-t1's migration 0009:
// the deployment_jobs rebuild that widens the operation CHECK set to
// admit 'state_change'. The critical property is DATA PRESERVATION — a
// CHECK change in SQLite requires a full table rebuild (create new / copy
// / drop / rename), and a bug there would silently lose existing jobs.
// So the test seeds install + rollback rows of varied status BEFORE the
// rebuild is observable and asserts they all survive, then proves the new
// operation inserts and the index came back.
func TestDeploymentStateChangeMigration(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "deploy_sc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	seedDevice := func(t *testing.T, id string) {
		t.Helper()
		if _, err := d.ExecContext(ctx, `
			INSERT INTO devices(id, name, status, created_at)
			VALUES (?, 'dev', 'approved', 1)
		`, id); err != nil {
			t.Fatalf("seed device: %v", err)
		}
	}

	// state_change is now a valid operation (the whole point of 0009).
	t.Run("state_change_accepted", func(t *testing.T) {
		seedDevice(t, "dev_sc")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_sc', 'dev_sc', 'state_change', 'pending', '{"operation":"state_change"}', 1, 1)
		`)
		if err != nil {
			t.Errorf("state_change insert rejected after 0009: %v", err)
		}
	})

	// install + rollback still pass; an unknown operation still fails —
	// 0009 widened the set, it did not replace it or open it up.
	t.Run("existing_operations_still_valid", func(t *testing.T) {
		seedDevice(t, "dev_old")
		for _, op := range []string{"install", "rollback"} {
			_, err := d.ExecContext(ctx, `
				INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
				VALUES (?, 'dev_old', ?, 'pending', '{}', 1, 1)
			`, "dj_"+op, op)
			if err != nil {
				t.Errorf("%s insert rejected: %v", op, err)
			}
		}
	})

	t.Run("unknown_operation_still_rejected", func(t *testing.T) {
		seedDevice(t, "dev_bad")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_bad', 'dev_bad', 'remove', 'pending', '{}', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("operation CHECK not enforced after 0009 (remove should still fail): err = %v", err)
		}
	})

	// The status CHECK and the device FK + ON DELETE CASCADE must survive
	// the rebuild unchanged.
	t.Run("status_check_survives", func(t *testing.T) {
		seedDevice(t, "dev_st9")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_st9', 'dev_st9', 'state_change', 'queued', '{}', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("status CHECK lost in rebuild: err = %v", err)
		}
	})

	t.Run("fk_cascade_survives", func(t *testing.T) {
		seedDevice(t, "dev_csc9")
		if _, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_csc9', 'dev_csc9', 'state_change', 'pending', '{}', 1, 1)
		`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := d.ExecContext(ctx, `DELETE FROM devices WHERE id = 'dev_csc9'`); err != nil {
			t.Fatalf("delete device: %v", err)
		}
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT count(*) FROM deployment_jobs WHERE id = 'dj_csc9'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("ON DELETE CASCADE lost in rebuild (n=%d)", n)
		}
	})

	t.Run("index_recreated", func(t *testing.T) {
		var got string
		if err := d.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_deployment_jobs_device_status'`,
		).Scan(&got); err != nil {
			t.Errorf("idx_deployment_jobs_device_status not recreated by 0009: %v", err)
		}
	})
}

// TestDeploymentStateChangeMigration_PreservesData is the data-loss guard:
// it can't apply 0009 "after" seeding through the public runner (Apply is
// all-or-nothing from a fresh db), so instead it builds the pre-0009
// schema by hand, seeds rows, then runs the 0009 rebuild SQL directly and
// asserts every seeded row survived with its columns intact. This is the
// closest faithful model of "an existing deployment upgrades and keeps its
// job history".
func TestDeploymentStateChangeMigration_PreservesData(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "deploy_preserve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// Build the schema only up to 0008 (the pre-0009 world): apply each
	// migration by hand, stopping before 0009.
	all, err := LoadAll(Embedded())
	if err != nil {
		t.Fatal(err)
	}
	var rebuild Migration
	for _, m := range all {
		if m.Version == 9 {
			rebuild = m
			continue
		}
		if m.Version < 9 {
			if _, err := d.ExecContext(ctx, m.SQL); err != nil {
				t.Fatalf("apply %04d_%s: %v", m.Version, m.Name, err)
			}
		}
	}
	if rebuild.SQL == "" {
		t.Fatal("migration 0009 not found")
	}

	// Seed a device and three jobs of different operation/status, with
	// non-trivial plan_json / result_json / expires_at to prove every
	// column copies through the rebuild.
	if _, err := d.ExecContext(ctx, `
		INSERT INTO devices(id, name, status, created_at) VALUES ('dev_p', 'dev', 'approved', 1)
	`); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	seed := []struct {
		id, op, status, req, plan, res string
		created, updated, expires      int64
	}{
		{"dj_p1", "install", "succeeded", `{"operation":"install"}`, `{"version_id":"sv_1"}`, `{"files_written":["SKILL.md"]}`, 100, 200, 0},
		{"dj_p2", "rollback", "failed", `{"operation":"rollback"}`, `{"target":{}}`, `{"error_code":"x"}`, 300, 400, 0},
		{"dj_p3", "install", "pending", `{"operation":"install"}`, ``, ``, 500, 500, 999999},
	}
	for _, s := range seed {
		var plan, res, exp any
		if s.plan != "" {
			plan = s.plan
		}
		if s.res != "" {
			res = s.res
		}
		if s.expires != 0 {
			exp = s.expires
		}
		if _, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs
				(id, device_id, operation, status, request_json, plan_json, result_json, created_at, updated_at, expires_at)
			VALUES (?, 'dev_p', ?, ?, ?, ?, ?, ?, ?, ?)
		`, s.id, s.op, s.status, s.req, plan, res, s.created, s.updated, exp); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	// Run the 0009 rebuild.
	if _, err := d.ExecContext(ctx, rebuild.SQL); err != nil {
		t.Fatalf("apply 0009 rebuild: %v", err)
	}

	// Every seeded row survives, byte-for-byte on the copied columns.
	for _, s := range seed {
		var op, status, req string
		var plan, res *string
		var created, updated int64
		var expires *int64
		err := d.QueryRowContext(ctx, `
			SELECT operation, status, request_json, plan_json, result_json, created_at, updated_at, expires_at
			FROM deployment_jobs WHERE id = ?
		`, s.id).Scan(&op, &status, &req, &plan, &res, &created, &updated, &expires)
		if err != nil {
			t.Errorf("%s lost in rebuild: %v", s.id, err)
			continue
		}
		if op != s.op || status != s.status || req != s.req || created != s.created || updated != s.updated {
			t.Errorf("%s scalar columns changed: op=%s status=%s req=%s created=%d updated=%d",
				s.id, op, status, req, created, updated)
		}
		gotPlan := ""
		if plan != nil {
			gotPlan = *plan
		}
		if gotPlan != s.plan {
			t.Errorf("%s plan_json: got %q want %q", s.id, gotPlan, s.plan)
		}
		gotRes := ""
		if res != nil {
			gotRes = *res
		}
		if gotRes != s.res {
			t.Errorf("%s result_json: got %q want %q", s.id, gotRes, s.res)
		}
		gotExp := int64(0)
		if expires != nil {
			gotExp = *expires
		}
		if gotExp != s.expires {
			t.Errorf("%s expires_at: got %d want %d", s.id, gotExp, s.expires)
		}
	}

	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM deployment_jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(seed) {
		t.Errorf("row count after rebuild: got %d want %d", n, len(seed))
	}
}
