package deploy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

// newStore opens a migrated in-memory-ish DB (temp file) and returns a
// jobs Store plus the raw handle, with one approved device seeded so the
// device_id FK is satisfied. deviceID is the seeded device.
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "deploy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO devices(id, name, status, created_at)
		VALUES ('dev1', 'dev', 'approved', 1)
	`); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return New(d), "dev1"
}

func mustCreate(t *testing.T, s *Store, deviceID string, now time.Time) Job {
	t.Helper()
	job, err := s.Create(context.Background(), CreateParams{
		DeviceID:    deviceID,
		Operation:   OpInstall,
		RequestJSON: `{"operation":"install","skill_name":"deploy-helper","version_id":"sv_1"}`,
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return job
}

// TestCreate_PendingWithExpiry: a created job is pending, carries the
// request, and gets a default-TTL expiry.
func TestCreate_PendingWithExpiry(t *testing.T) {
	s, dev := newStore(t)
	now := time.UnixMilli(1_000_000)
	job := mustCreate(t, s, dev, now)

	if job.Status != StatusPending {
		t.Errorf("status = %q, want pending", job.Status)
	}
	if job.ExpiresAt != now.Add(DefaultJobTTL) {
		t.Errorf("expires_at = %v, want now+%v", job.ExpiresAt, DefaultJobTTL)
	}
	got, err := s.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RequestJSON != job.RequestJSON {
		t.Errorf("round-trip request mismatch")
	}
}

func TestCreate_Validation(t *testing.T) {
	s, dev := newStore(t)
	now := time.UnixMilli(1)
	ctx := context.Background()

	if _, err := s.Create(ctx, CreateParams{Operation: OpInstall, RequestJSON: "{}"}, now); err != ErrEmptyDeviceID {
		t.Errorf("empty device: err = %v, want ErrEmptyDeviceID", err)
	}
	for _, op := range []Operation{OpRegisterRoot, OpRemoveRoot} {
		if _, err := s.Create(ctx, CreateParams{DeviceID: dev, Operation: op, RequestJSON: "{}"}, now); err != nil {
			t.Errorf("%s operation rejected: %v", op, err)
		}
	}
	if _, err := s.Create(ctx, CreateParams{DeviceID: dev, Operation: "remove", RequestJSON: "{}"}, now); err == nil {
		t.Error("invalid operation accepted")
	}
	if _, err := s.Create(ctx, CreateParams{DeviceID: dev, Operation: OpInstall}, now); err == nil {
		t.Error("empty request_json accepted")
	}
}

// TestClaimNext_SingleWinner is the core CAS guard: once a job is
// claimed, a second ClaimNext for the same device does NOT return it
// again. Two distinct pending jobs are each claimed exactly once, oldest
// first; a third claim on an empty queue returns ok=false.
func TestClaimNext_SingleWinner(t *testing.T) {
	s, dev := newStore(t)
	ctx := context.Background()
	t0 := time.UnixMilli(1_000_000)
	j1 := mustCreate(t, s, dev, t0)
	j2 := mustCreate(t, s, dev, t0.Add(time.Second))

	now := t0.Add(time.Minute)
	got1, ok, err := s.ClaimNext(ctx, dev, now)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if got1.ID != j1.ID {
		t.Errorf("first claim = %s, want oldest %s", got1.ID, j1.ID)
	}
	if got1.Status != StatusClaimed {
		t.Errorf("claimed job status = %q, want claimed", got1.Status)
	}

	got2, ok, err := s.ClaimNext(ctx, dev, now)
	if err != nil || !ok {
		t.Fatalf("second claim: ok=%v err=%v", ok, err)
	}
	if got2.ID != j2.ID {
		t.Errorf("second claim = %s, want %s; the already-claimed j1 must not return again", got2.ID, j2.ID)
	}

	_, ok, err = s.ClaimNext(ctx, dev, now)
	if err != nil {
		t.Fatalf("third claim err: %v", err)
	}
	if ok {
		t.Error("third claim returned a job from an empty queue")
	}
}

// TestClaimNext_ExpiresStalePending: a pending job past its expiry is
// lazily marked expired at claim time and never handed out.
func TestClaimNext_ExpiresStalePending(t *testing.T) {
	s, dev := newStore(t)
	ctx := context.Background()
	t0 := time.UnixMilli(1_000_000)
	job := mustCreate(t, s, dev, t0) // expires at t0+1h

	// Claim well after expiry.
	_, ok, err := s.ClaimNext(ctx, dev, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ok {
		t.Error("expired pending job was claimed")
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusExpired {
		t.Errorf("status = %q, want expired", got.Status)
	}
}

// TestClaimNext_OnlyOwnDevice: a job for device A is never handed to
// device B's claim.
func TestClaimNext_OnlyOwnDevice(t *testing.T) {
	s, dev := newStore(t)
	ctx := context.Background()
	// Seed a second device and a job for it.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO devices(id, name, status, created_at) VALUES ('dev2','d2','approved',1)
	`); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_000_000)
	if _, err := s.Create(ctx, CreateParams{DeviceID: "dev2", Operation: OpInstall, RequestJSON: "{}"}, now); err != nil {
		t.Fatal(err)
	}

	_, ok, err := s.ClaimNext(ctx, dev, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ok {
		t.Error("dev1 claimed dev2's job")
	}
}

// TestComplete_TerminalTransition: a claimed job can be completed
// succeeded with a result; a second completion (now terminal) is
// rejected as not claimable.
func TestComplete_TerminalTransition(t *testing.T) {
	s, dev := newStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_000_000)
	job := mustCreate(t, s, dev, now)

	if _, ok, err := s.ClaimNext(ctx, dev, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := s.Complete(ctx, job.ID, StatusSucceeded, `{"rescan_content_sha256":"abc"}`, now.Add(2*time.Second)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.Status != StatusSucceeded || got.ResultJSON == "" {
		t.Errorf("after complete: status=%q result=%q", got.Status, got.ResultJSON)
	}
	// Second completion must fail — job is terminal, not claimed/running.
	if err := s.Complete(ctx, job.ID, StatusFailed, "{}", now.Add(3*time.Second)); err != ErrNotClaimable {
		t.Errorf("double complete: err = %v, want ErrNotClaimable", err)
	}
}

// TestComplete_RejectsPending: reporting a result on a job that was
// never claimed is a protocol violation.
func TestComplete_RejectsPending(t *testing.T) {
	s, dev := newStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1)
	job := mustCreate(t, s, dev, now)
	if err := s.Complete(ctx, job.ID, StatusSucceeded, "{}", now); err != ErrNotClaimable {
		t.Errorf("complete pending: err = %v, want ErrNotClaimable", err)
	}
}

func TestComplete_NotFound(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Complete(context.Background(), "dj_ghost", StatusSucceeded, "{}", time.UnixMilli(1)); err != ErrJobNotFound {
		t.Errorf("err = %v, want ErrJobNotFound", err)
	}
}

// TestList_FiltersBySkillAndDevice: List narrows by device and by the
// skill embedded in request_json, newest first.
func TestList_FiltersBySkillAndDevice(t *testing.T) {
	s, dev := newStore(t)
	ctx := context.Background()
	t0 := time.UnixMilli(1_000_000)
	// Two jobs for deploy-helper, one for other-skill.
	mustCreate(t, s, dev, t0)
	if _, err := s.Create(ctx, CreateParams{
		DeviceID: dev, Operation: OpInstall,
		RequestJSON: `{"operation":"install","skill_name":"other-skill","version_id":"sv_2"}`,
	}, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	all, err := s.List(ctx, ListFilter{DeviceID: dev})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("device list = %d, want 2", len(all))
	}
	// Newest first.
	if all[0].CreatedAt.Before(all[1].CreatedAt) {
		t.Error("list not newest-first")
	}

	only, err := s.List(ctx, ListFilter{DeviceID: dev, SkillName: "deploy-helper"})
	if err != nil {
		t.Fatalf("list skill: %v", err)
	}
	if len(only) != 1 {
		t.Fatalf("skill-filtered list = %d, want 1", len(only))
	}
}
