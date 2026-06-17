package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestDeploymentSchema applies the embedded migrations and asserts the
// phase-8-t1 deployment_jobs table: its presence, the operation and
// status CHECK sets, the device_id FK with ON DELETE CASCADE, a clean
// insert of a pending install job, and the device_status index.
func TestDeploymentSchema(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "deploy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// A device row to satisfy the FK on every job insert below.
	seedDevice := func(t *testing.T, id string) {
		t.Helper()
		if _, err := d.ExecContext(ctx, `
			INSERT INTO devices(id, name, status, created_at)
			VALUES (?, 'dev', 'approved', 1)
		`, id); err != nil {
			t.Fatalf("seed device: %v", err)
		}
	}

	t.Run("table", func(t *testing.T) {
		var got string
		if err := d.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name='deployment_jobs'`,
		).Scan(&got); err != nil {
			t.Errorf("deployment_jobs missing: %v", err)
		}
	})

	t.Run("index", func(t *testing.T) {
		var got string
		if err := d.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_deployment_jobs_device_status'`,
		).Scan(&got); err != nil {
			t.Errorf("idx_deployment_jobs_device_status missing: %v", err)
		}
	})

	t.Run("clean_insert", func(t *testing.T) {
		seedDevice(t, "dev_ok")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_ok', 'dev_ok', 'install', 'pending', '{"operation":"install"}', 1, 1)
		`)
		if err != nil {
			t.Errorf("clean install insert rejected: %v", err)
		}
	})

	t.Run("operation_check", func(t *testing.T) {
		seedDevice(t, "dev_op")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_badop', 'dev_op', 'remove', 'pending', '{}', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("operation CHECK not enforced (remove should be rejected this phase): err = %v", err)
		}
	})

	t.Run("status_check", func(t *testing.T) {
		seedDevice(t, "dev_st")
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_badst', 'dev_st', 'install', 'queued', '{}', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("status CHECK not enforced: err = %v", err)
		}
	})

	t.Run("device_fk_cascade", func(t *testing.T) {
		seedDevice(t, "dev_cascade")
		if _, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_cascade', 'dev_cascade', 'install', 'pending', '{}', 1, 1)
		`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := d.ExecContext(ctx, `DELETE FROM devices WHERE id = 'dev_cascade'`); err != nil {
			t.Fatalf("delete device: %v", err)
		}
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT count(*) FROM deployment_jobs WHERE id = 'dj_cascade'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("job survived device delete; ON DELETE CASCADE not active (n=%d)", n)
		}
	})

	t.Run("orphan_device_rejected", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO deployment_jobs(id, device_id, operation, status, request_json, created_at, updated_at)
			VALUES ('dj_orphan', 'dev_nonexistent', 'install', 'pending', '{}', 1, 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Errorf("FK to devices not enforced: err = %v", err)
		}
	})
}
