package session

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

func newTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "sess.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// Seed a user since sessions.user_id has FK -> users.
	uid := "usr_test"
	if _, err := d.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, created_at) VALUES (?, 'alice', 'h', 1)`,
		uid,
	); err != nil {
		t.Fatal(err)
	}
	return d, uid
}

func TestCreateLookupHappy(t *testing.T) {
	d, uid := newTestDB(t)
	now := time.Now()
	ctx := context.Background()

	sess, tok, err := Create(ctx, d, uid, "127.0.0.1", "test-ua", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sess.ID, "ses_") {
		t.Errorf("session id shape: %s", sess.ID)
	}
	if len(tok) < 32 {
		t.Errorf("token too short: %d", len(tok))
	}

	// Lookup should succeed and bump last_seen_at.
	later := now.Add(time.Minute)
	got, err := Lookup(ctx, d, tok, later)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sess.ID || got.UserID != uid {
		t.Errorf("Lookup returned %+v, want id=%s uid=%s", got, sess.ID, uid)
	}
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("LastSeenAt not bumped: %v vs %v", got.LastSeenAt, later)
	}
}

func TestLookupRejectsExpired(t *testing.T) {
	d, uid := newTestDB(t)
	now := time.Now()
	_, tok, err := Create(context.Background(), d, uid, "", "", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Lookup(context.Background(), d, tok, now.Add(2*time.Minute))
	if !errors.Is(err, ErrExpired) {
		t.Errorf("err = %v, want ErrExpired", err)
	}
}

func TestLookupRejectsRevoked(t *testing.T) {
	d, uid := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	sess, tok, _ := Create(ctx, d, uid, "", "", time.Hour, now)
	if err := Revoke(ctx, d, sess.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(ctx, d, tok, now.Add(time.Minute)); !errors.Is(err, ErrRevoked) {
		t.Errorf("err = %v, want ErrRevoked", err)
	}
}

func TestLookupRejectsUnknown(t *testing.T) {
	d, _ := newTestDB(t)
	if _, err := Lookup(context.Background(), d, "not-a-real-token", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLookupRejectsEmpty(t *testing.T) {
	d, _ := newTestDB(t)
	if _, err := Lookup(context.Background(), d, "", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRevokeIdempotent(t *testing.T) {
	d, uid := newTestDB(t)
	ctx := context.Background()
	sess, _, _ := Create(ctx, d, uid, "", "", time.Hour, time.Now())
	if err := Revoke(ctx, d, sess.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Revoke(ctx, d, sess.ID, time.Now()); err != nil {
		t.Errorf("second Revoke errored: %v", err)
	}
}

func TestTokenHashStable(t *testing.T) {
	a := hashToken("abc")
	b := hashToken("abc")
	if a != b {
		t.Fatal("hashToken not deterministic")
	}
	if a == hashToken("abd") {
		t.Error("hashToken collision")
	}
	if len(a) != 64 {
		t.Errorf("hashToken len = %d, want 64 hex chars", len(a))
	}
}
