package noncepurge

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "noncepurge.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// seedDevice inserts a minimal approved device row. agent_nonces has a
// FK on devices(id); only id/name/status/created_at are NOT NULL.
func seedDevice(t *testing.T, d *sql.DB, id string) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO devices(id, name, status, created_at) VALUES(?, ?, 'approved', ?)`,
		id, id, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
}

func insertNonceRow(t *testing.T, d *sql.DB, deviceID, nonce string, usedAt time.Time) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES(?,?,?)`,
		deviceID, nonce, usedAt.UnixMilli()); err != nil {
		t.Fatalf("insert nonce: %v", err)
	}
}

func TestSweep_DeletesExpiredKeepsRecent(t *testing.T) {
	d := newDB(t)
	seedDevice(t, d, "dev1")
	now := time.Now()
	insertNonceRow(t, d, "dev1", "old", now.Add(-10*time.Minute))   // 过期
	insertNonceRow(t, d, "dev1", "recent", now.Add(-1*time.Minute)) // 窗口内

	deleted := sweep(context.Background(), d, 5*time.Minute, nil)
	if deleted != 1 {
		t.Fatalf("deleted %d, want 1", deleted)
	}

	var remaining string
	_ = d.QueryRow(`SELECT nonce FROM agent_nonces WHERE device_id='dev1'`).Scan(&remaining)
	if remaining != "recent" {
		t.Fatalf("remaining nonce = %q, want recent", remaining)
	}
}

func TestRun_IntervalZeroNoOp(t *testing.T) {
	// interval<=0 → Run returns immediately, does not block
	done := make(chan struct{})
	go func() {
		Run(context.Background(), newDB(t), 0, 5*time.Minute, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with interval=0 did not return within 1s")
	}
}
