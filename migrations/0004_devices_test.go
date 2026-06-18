package migrations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestDevicesSchema applies the embedded migrations against a fresh
// database and asserts the three phase-2-t3 tables, their key
// columns, the status CHECK, the cascading deletes, and the replay
// guard all landed correctly.
func TestDevicesSchema(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	t.Run("tables", func(t *testing.T) {
		for _, name := range []string{"devices", "device_secrets", "agent_nonces"} {
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
			"idx_devices_status",
			"idx_devices_last_seen_at",
			"idx_agent_nonces_used_at",
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

	t.Run("status_check", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO devices(id, name, status, created_at)
			VALUES ('d_x', 'bad', 'banana', 1)
		`)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Errorf("status CHECK not enforced: err = %v", err)
		}
	})

	t.Run("strict_mode", func(t *testing.T) {
		_, err := d.ExecContext(ctx, `
			INSERT INTO devices(id, name, status, created_at)
			VALUES ('d_strict', 'bad', 'pending', 'not-a-number')
		`)
		if err == nil || !strings.Contains(err.Error(), "cannot store TEXT") {
			t.Errorf("STRICT not enforced on created_at: err = %v", err)
		}
	})

	t.Run("cascade_device_secret", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('d_c1', 'host', 'pending', 1)`)
		mustExec(t, d, `INSERT INTO device_secrets(device_id, secret_hash, created_at) VALUES ('d_c1', 'h', 1)`)
		mustExec(t, d, `DELETE FROM devices WHERE id='d_c1'`)
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM device_secrets WHERE device_id='d_c1'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("device_secrets row not cascaded, count=%d", n)
		}
	})

	t.Run("cascade_agent_nonces", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('d_c2', 'host', 'approved', 1)`)
		mustExec(t, d, `INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES ('d_c2', 'n1', 1)`)
		mustExec(t, d, `INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES ('d_c2', 'n2', 1)`)
		mustExec(t, d, `DELETE FROM devices WHERE id='d_c2'`)
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM agent_nonces WHERE device_id='d_c2'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("agent_nonces rows not cascaded, count=%d", n)
		}
	})

	t.Run("nonce_replay_rejected", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('d_r1', 'host', 'approved', 1)`)
		mustExec(t, d, `INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES ('d_r1', 'same', 1)`)
		// Replay = same (device_id, nonce) -> PK violation.
		_, err := d.ExecContext(ctx,
			`INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES ('d_r1', 'same', 2)`,
		)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Errorf("nonce replay not rejected: err = %v", err)
		}
		// Same nonce for a *different* device must still succeed —
		// device_id is part of the PK by design.
		mustExec(t, d, `INSERT INTO devices(id, name, status, created_at) VALUES ('d_r2', 'host', 'approved', 1)`)
		mustExec(t, d, `INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES ('d_r2', 'same', 1)`)
	})

	t.Run("schema_migrations_recorded", func(t *testing.T) {
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version=4 AND name='devices'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("schema_migrations missing 4_devices row, count=%d", n)
		}
	})
}
