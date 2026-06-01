package enrollment

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
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "enr.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestCreateShape(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	tok, err := Create(context.Background(), d, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.ID, "tok_") {
		t.Errorf("ID shape: %s", tok.ID)
	}
	if !strings.HasPrefix(tok.Plaintext, TokenPrefix) {
		t.Errorf("plaintext missing prefix: %s", tok.Plaintext)
	}
	if len(tok.Plaintext) != len(TokenPrefix)+24 {
		t.Errorf("plaintext length = %d, want %d", len(tok.Plaintext), len(TokenPrefix)+24)
	}
	if tok.Status != StatusPending {
		t.Errorf("status = %q", tok.Status)
	}
	if !tok.ExpiresAt.After(tok.CreatedAt) {
		t.Errorf("expiry not after creation: created=%v expires=%v", tok.CreatedAt, tok.ExpiresAt)
	}
}

func TestCreatePersistsOnlyHash(t *testing.T) {
	d := newTestDB(t)
	tok, _ := Create(context.Background(), d, time.Hour, time.Now())

	var hash string
	if err := d.QueryRow(`SELECT token_hash FROM enrollment_tokens WHERE id=?`, tok.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == tok.Plaintext {
		t.Error("DB stored plaintext, not hash")
	}
	if hash != hashToken(tok.Plaintext) {
		t.Errorf("stored hash != sha256(plaintext)")
	}
}

func TestListNewestFirst(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	base := time.Now()
	_, _ = Create(ctx, d, time.Hour, base)
	_, _ = Create(ctx, d, time.Hour, base.Add(time.Second))
	_, _ = Create(ctx, d, time.Hour, base.Add(2*time.Second))

	tokens, err := List(ctx, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 {
		t.Fatalf("len = %d, want 3", len(tokens))
	}
	for i := 1; i < len(tokens); i++ {
		if tokens[i-1].CreatedAt.Before(tokens[i].CreatedAt) {
			t.Errorf("order wrong at i=%d", i)
		}
	}
	for _, tok := range tokens {
		if tok.Plaintext != "" {
			t.Error("List leaked plaintext")
		}
	}
}

func TestConsumeHappy(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	tok, _ := Create(ctx, d, time.Hour, now)

	id, err := Consume(ctx, d, tok.Plaintext, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if id != tok.ID {
		t.Errorf("id = %s, want %s", id, tok.ID)
	}
	var status string
	var usedAt sql.NullInt64
	_ = d.QueryRow(`SELECT status, used_at FROM enrollment_tokens WHERE id=?`, tok.ID).Scan(&status, &usedAt)
	if status != StatusUsed || !usedAt.Valid {
		t.Errorf("post-consume: status=%s used_at=%v", status, usedAt)
	}
}

func TestConsumeNormalisesWhitespace(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	tok, _ := Create(ctx, d, time.Hour, now)

	if _, err := Consume(ctx, d, "  "+tok.Plaintext+"\n", now); err != nil {
		t.Errorf("Consume on trimmed token: %v", err)
	}
}

func TestConsumeRejectsExpired(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	tok, _ := Create(ctx, d, time.Minute, now)

	_, err := Consume(ctx, d, tok.Plaintext, now.Add(2*time.Minute))
	if !errors.Is(err, ErrExpired) {
		t.Errorf("err = %v, want ErrExpired", err)
	}
}

func TestConsumeRejectsWrongToken(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	_, _ = Create(ctx, d, time.Hour, time.Now())

	_, err := Consume(ctx, d, "sfen_fakefakefakefakefakefa", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestConsumeRejectsEmptyToken(t *testing.T) {
	d := newTestDB(t)
	_, err := Consume(context.Background(), d, "   ", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound on empty", err)
	}
}

func TestConsumeRejectsAlreadyUsed(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	tok, _ := Create(ctx, d, time.Hour, now)

	if _, err := Consume(ctx, d, tok.Plaintext, now); err != nil {
		t.Fatal(err)
	}
	_, err := Consume(ctx, d, tok.Plaintext, now.Add(time.Second))
	if !errors.Is(err, ErrNotUsable) {
		t.Errorf("err = %v, want ErrNotUsable", err)
	}
}

func TestRevokePendingToUsedRejected(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	tok, _ := Create(ctx, d, time.Hour, now)

	if _, err := Consume(ctx, d, tok.Plaintext, now); err != nil {
		t.Fatal(err)
	}
	// Cannot revoke an already-used token.
	err := Revoke(ctx, d, tok.ID, now.Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), "cannot revoke") {
		t.Errorf("err = %v, want cannot-revoke", err)
	}
}

func TestRevokeIdempotent(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	tok, _ := Create(ctx, d, time.Hour, time.Now())

	if err := Revoke(ctx, d, tok.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Calling again on a revoked row is a no-op, not an error.
	if err := Revoke(ctx, d, tok.ID, time.Now()); err != nil {
		t.Errorf("second Revoke: %v", err)
	}
}

func TestRevokeUnknown(t *testing.T) {
	d := newTestDB(t)
	err := Revoke(context.Background(), d, "tok_doesnotexist", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRevokedCannotBeConsumed(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	tok, _ := Create(ctx, d, time.Hour, time.Now())
	if err := Revoke(ctx, d, tok.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := Consume(ctx, d, tok.Plaintext, time.Now())
	if !errors.Is(err, ErrNotUsable) {
		t.Errorf("err = %v, want ErrNotUsable", err)
	}
}

func TestConsumeWorksInTransaction(t *testing.T) {
	// Smoke for the inline interface — make sure *sql.Tx satisfies the
	// constraint so the t5 enrol flow can wrap consume + device insert
	// in a single tx.
	d := newTestDB(t)
	ctx := context.Background()
	tok, _ := Create(ctx, d, time.Hour, time.Now())

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Consume(ctx, tx, tok.Plaintext, time.Now())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if id != tok.ID {
		t.Errorf("id = %s, want %s", id, tok.ID)
	}
}
