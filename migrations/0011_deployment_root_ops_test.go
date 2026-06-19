package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestDeploymentRootOpsMigration covers phase-11-t5's migration 0011: the
// deployment_jobs rebuild that widens operation CHECK for register/remove root
// jobs while preserving existing queue history.
func TestDeploymentRootOpsMigration(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "deploy_root_ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	seedDevice := func(t *testing.T, id string) {
		t.Helper()
		mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES (?, 'dev', 'approved', 1)`, id)
	}

	t.Run("root_ops_accepted", func(t *testing.T) {
		seedDevice(t, "dev_roots")
		for _, op := range []string{"register_root", "remove_root"} {
			_, err := d.ExecContext(ctx, `
				INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
				VALUES (?, 'dev_roots', ?, 'pending', '{}', 1, 1)
			`, "dj_"+op, op)
			if err != nil {
				t.Errorf("%s insert rejected after 0011: %v", op, err)
			}
		}
	})

	t.Run("existing_operations_still_valid", func(t *testing.T) {
		seedDevice(t, "dev_old11")
		for _, op := range []string{"install", "rollback", "state_change"} {
			_, err := d.ExecContext(ctx, `
				INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
				VALUES (?, 'dev_old11', ?, 'pending', '{}', 1, 1)
			`, "dj_old11_"+op, op)
			if err != nil {
				t.Errorf("%s insert rejected: %v", op, err)
			}
		}
	})

	t.Run("unknown_operation_still_rejected", func(t *testing.T) {
		seedDevice(t, "dev_bad11")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_bad11', 'dev_bad11', 'remove', 'pending', '{}', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("operation CHECK not enforced after 0011: err = %v", err)
		}
	})

	t.Run("index_recreated", func(t *testing.T) {
		var got string
		if err := d.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_deployment_jobs_device_status'`,
		).Scan(&got); err != nil {
			t.Errorf("idx_deployment_jobs_device_status not recreated by 0011: %v", err)
		}
	})
}

func TestDeploymentRootOpsMigration_PreservesData(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "deploy_root_ops_preserve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	all, err := LoadAll(Embedded())
	if err != nil {
		t.Fatal(err)
	}
	var rebuild Migration
	for _, m := range all {
		if m.Version == 11 {
			rebuild = m
			continue
		}
		if m.Version < 11 {
			if _, err := d.ExecContext(ctx, m.SQL); err != nil {
				t.Fatalf("apply %04d_%s: %v", m.Version, m.Name, err)
			}
		}
	}
	if rebuild.SQL == "" {
		t.Fatal("migration 0011 not found")
	}

	mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('dev_p11', 'dev', 'approved', 1)`)
	seed := []struct {
		id, op, status, req, plan, res string
		created, updated, expires      int64
	}{
		{"dj_p11_install", "install", "succeeded", `{"operation":"install"}`, `{"version_id":"sv_1"}`, `{"files_written":["SKILL.md"]}`, 100, 200, 0},
		{"dj_p11_state", "state_change", "failed", `{"operation":"state_change"}`, `{"target":{}}`, `{"error_code":"x"}`, 300, 400, 0},
		{"dj_p11_pending", "rollback", "pending", `{"operation":"rollback"}`, ``, ``, 500, 500, 999999},
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
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs
				(id, device_id, operation, status, request_json, plan_json, result_json, created_at, updated_at, expires_at)
			VALUES (?, 'dev_p11', ?, ?, ?, ?, ?, ?, ?, ?)
		`, s.id, s.op, s.status, s.req, plan, res, s.created, s.updated, exp)
		if err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	if _, err := d.ExecContext(ctx, rebuild.SQL); err != nil {
		t.Fatalf("apply 0011 rebuild: %v", err)
	}

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
}
