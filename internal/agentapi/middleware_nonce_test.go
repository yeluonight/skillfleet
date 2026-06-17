package agentapi

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

// newNonceTestDB builds a migrated SQLite for nonce tests, mirroring
// the file-path pattern in internal/registry/registry_test.go.
func newNonceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "nonce.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// seedDevice inserts a minimal approved device row. agent_nonces has a
// FK on devices(id); only id/name/status/created_at are NOT NULL
// (migrations/0004_devices.sql).
func seedDevice(t *testing.T, d *sql.DB, id string) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO devices(id, name, status, created_at) VALUES (?, ?, 'approved', ?)`,
		id, id, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
}

func TestInsertNonce_HappyPath(t *testing.T) {
	d := newNonceTestDB(t)
	seedDevice(t, d, "dev1")
	if err := insertNonce(context.Background(), d, "dev1", "n1", time.Now()); err != nil {
		t.Fatalf("insertNonce: %v", err)
	}
}

func TestInsertNonce_UNIQUEReturnsReplay(t *testing.T) {
	d := newNonceTestDB(t)
	seedDevice(t, d, "dev1")
	now := time.Now()
	if err := insertNonce(context.Background(), d, "dev1", "dup", now); err != nil {
		t.Fatal(err)
	}
	err := insertNonce(context.Background(), d, "dev1", "dup", now)
	if !errors.Is(err, errNonceReplay) {
		t.Fatalf("got %v, want errNonceReplay", err)
	}
}

func TestSleepCtx_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
