package devices

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "dev.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func enrollOne(t *testing.T, d *sql.DB, name string) EnrollResult {
	t.Helper()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Enroll(context.Background(), tx, EnrollInput{Name: name, Hostname: "h", OS: "linux", Arch: "amd64", AgentVersion: "0.1"}, time.Now())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEnrollShape(t *testing.T) {
	d := newTestDB(t)
	res := enrollOne(t, d, "alpha")
	if !strings.HasPrefix(res.Device.ID, "dev_") {
		t.Errorf("device id shape: %s", res.Device.ID)
	}
	if res.Device.Status != StatusPending {
		t.Errorf("status = %s", res.Device.Status)
	}
	if res.Secret == "" || len(res.Secret) < 32 {
		t.Errorf("secret looks wrong: %q", res.Secret)
	}
}

func TestEnrollPersistsHashOnly(t *testing.T) {
	d := newTestDB(t)
	res := enrollOne(t, d, "alpha")

	var stored string
	if err := d.QueryRow(`SELECT secret_hash FROM device_secrets WHERE device_id=?`, res.Device.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == res.Secret {
		t.Error("plaintext leaked to device_secrets.secret_hash")
	}
	if stored != hashSecret(res.Secret) {
		t.Error("stored hash != sha256(plaintext)")
	}
}

func TestEnrollRequiresName(t *testing.T) {
	d := newTestDB(t)
	tx, _ := d.Begin()
	defer tx.Rollback()
	if _, err := Enroll(context.Background(), tx, EnrollInput{}, time.Now()); err == nil {
		t.Error("expected error on empty name")
	}
}

func TestVerifySecretHappyAndMismatch(t *testing.T) {
	d := newTestDB(t)
	res := enrollOne(t, d, "alpha")

	if err := VerifySecret(context.Background(), d, res.Device.ID, res.Secret); err != nil {
		t.Errorf("verify matching secret: %v", err)
	}
	if err := VerifySecret(context.Background(), d, res.Device.ID, "wrong"); !errors.Is(err, ErrSecretMismatch) {
		t.Errorf("err = %v, want ErrSecretMismatch", err)
	}
}

func TestVerifySecretMissingDevice(t *testing.T) {
	d := newTestDB(t)
	if err := VerifySecret(context.Background(), d, "dev_unknown", "x"); !errors.Is(err, ErrSecretNotSet) {
		t.Errorf("err = %v, want ErrSecretNotSet", err)
	}
}

func TestGetMissing(t *testing.T) {
	d := newTestDB(t)
	if _, err := Get(context.Background(), d, "dev_unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListNewestFirst(t *testing.T) {
	d := newTestDB(t)
	enrollOne(t, d, "a")
	time.Sleep(2 * time.Millisecond)
	enrollOne(t, d, "b")
	time.Sleep(2 * time.Millisecond)
	enrollOne(t, d, "c")

	list, err := List(context.Background(), d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Name != "c" || list[2].Name != "a" {
		t.Errorf("order wrong: %v", []string{list[0].Name, list[1].Name, list[2].Name})
	}
}

func TestSetStatusTransitions(t *testing.T) {
	ctx := context.Background()
	d := newTestDB(t)
	r1 := enrollOne(t, d, "a")
	r2 := enrollOne(t, d, "b")
	r3 := enrollOne(t, d, "c")

	// pending -> approved
	if err := SetStatus(ctx, d, r1.Device.ID, StatusApproved); err != nil {
		t.Errorf("pending->approved: %v", err)
	}
	// pending -> revoked
	if err := SetStatus(ctx, d, r2.Device.ID, StatusRevoked); err != nil {
		t.Errorf("pending->revoked: %v", err)
	}
	// approved -> revoked (transitively: first approve r3)
	if err := SetStatus(ctx, d, r3.Device.ID, StatusApproved); err != nil {
		t.Fatal(err)
	}
	if err := SetStatus(ctx, d, r3.Device.ID, StatusRevoked); err != nil {
		t.Errorf("approved->revoked: %v", err)
	}
}

func TestSetStatusRejectsIllegalTransition(t *testing.T) {
	ctx := context.Background()
	d := newTestDB(t)
	r := enrollOne(t, d, "x")

	// pending -> pending (no-op) is illegal
	if err := SetStatus(ctx, d, r.Device.ID, StatusPending); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("err = %v, want ErrInvalidStatus on no-op", err)
	}
	// approve then attempt approve again
	_ = SetStatus(ctx, d, r.Device.ID, StatusApproved)
	if err := SetStatus(ctx, d, r.Device.ID, StatusApproved); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("double approve: err = %v", err)
	}
	// revoke then attempt revive
	_ = SetStatus(ctx, d, r.Device.ID, StatusRevoked)
	if err := SetStatus(ctx, d, r.Device.ID, StatusApproved); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("revoked->approved: err = %v", err)
	}
}

func TestTouchLastSeen(t *testing.T) {
	ctx := context.Background()
	d := newTestDB(t)
	r := enrollOne(t, d, "a")

	now := time.Now()
	if err := TouchLastSeen(ctx, d, r.Device.ID, now); err != nil {
		t.Fatal(err)
	}
	got, err := Get(ctx, d, r.Device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastSeenAt.Equal(time.UnixMilli(now.UnixMilli())) {
		t.Errorf("LastSeenAt = %v, want %v", got.LastSeenAt, now)
	}
}

// insertInventoryRun writes a run row directly so List's LEFT JOIN has
// something to read. created is the run's created_at (ms-truncated).
func insertInventoryRun(t *testing.T, d *sql.DB, deviceID string, skillCount, rootCount int, created time.Time) string {
	t.Helper()
	id := "run_" + deviceID + "_" + created.Format("150405.000")
	_, err := d.Exec(`
		INSERT INTO inventory_runs (id, device_id, started_at, skill_count, root_count, agent_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, deviceID, created.UnixMilli(), skillCount, rootCount, "0.1", created.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestList_IncludesSkillAndRootCounts(t *testing.T) {
	d := newTestDB(t)
	res := enrollOne(t, d, "alpha")
	now := time.Now()
	// Two runs: the later one (more counts) must win.
	insertInventoryRun(t, d, res.Device.ID, 1, 1, now.Add(-time.Hour))
	insertInventoryRun(t, d, res.Device.ID, 3, 2, now)

	list, err := List(context.Background(), d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].SkillCount != 3 || list[0].RootCount != 2 {
		t.Errorf("counts = %d/%d, want 3/2 (latest run)", list[0].SkillCount, list[0].RootCount)
	}
}

func TestList_DeviceWithoutRun_ZeroCounts(t *testing.T) {
	d := newTestDB(t)
	enrollOne(t, d, "alpha")

	list, err := List(context.Background(), d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].SkillCount != 0 || list[0].RootCount != 0 {
		t.Errorf("counts = %d/%d, want 0/0 for no-run device", list[0].SkillCount, list[0].RootCount)
	}
}
