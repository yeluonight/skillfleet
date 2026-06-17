// Package enrollment owns the lifecycle of one-shot device enrolment
// tokens (v1.0 §4.1 step 1-4).
//
// Token shape: "sfen_<24 base64url chars>". The "sfen_" prefix lets
// operators grep agent logs / server output and recognise what they
// found at a glance, matching the "SF-SETUP-..." setup-code banner.
//
// Storage: the plaintext lives in the response payload exactly once.
// The DB row only carries sha256(plaintext); a database leak does not
// yield a usable token. Hashed comparison is constant-time.
//
// Status state machine (in `enrollment_tokens.status`):
//
//	pending  -- minted, not yet consumed
//	used     -- consumed by /agent/enroll (phase 2 t5)
//	revoked  -- explicitly cancelled by an admin
//
// Expired-but-still-pending rows stay as `pending` in the DB; Consume
// rejects them based on `expires_at`. A janitor sweeping these rows
// is not required for correctness, only for table hygiene; deferring.
package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

// Plaintext token vocabulary.
const (
	TokenPrefix   = "sfen_"
	TokenByteLen  = 24
	StatusPending = "pending"
	StatusUsed    = "used"
	StatusRevoked = "revoked"
	DefaultTTL    = 10 * time.Minute // §4.1 "short-term valid"
)

// Errors returned by Consume.
var (
	ErrNotFound  = errors.New("enrollment: token not found")
	ErrExpired   = errors.New("enrollment: token expired")
	ErrNotUsable = errors.New("enrollment: token already used or revoked")
)

// Token is the in-memory projection of a row in enrollment_tokens.
// Plaintext is non-empty only at the moment of creation.
type Token struct {
	ID        string
	Plaintext string // returned exactly once from Create; "" everywhere else
	Status    string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time // zero unless Status == used
}

// Create mints a fresh enrolment token, persists only its hash, and
// returns the plaintext to the caller for one-time display.
func Create(ctx context.Context, db *sql.DB, ttl time.Duration, now time.Time) (Token, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	plaintext, err := generate()
	if err != nil {
		return Token{}, err
	}
	tok := Token{
		ID:        idgen.New("tok"),
		Plaintext: plaintext,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO enrollment_tokens (id, token_hash, status, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`,
		tok.ID, hashToken(plaintext), tok.Status,
		tok.CreatedAt.UnixMilli(), tok.ExpiresAt.UnixMilli(),
	); err != nil {
		return Token{}, fmt.Errorf("enrollment: insert: %w", err)
	}
	return tok, nil
}

// List returns recent enrolment tokens, newest first, capped at
// `limit` rows. Plaintext fields are always empty — the DB cannot
// reconstruct them. Used by the WebUI / API surface for the
// "outstanding tokens" view.
func List(ctx context.Context, db *sql.DB, limit int) ([]Token, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, status, created_at, expires_at, used_at
		  FROM enrollment_tokens
		 ORDER BY created_at DESC
		 LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("enrollment: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Token
	for rows.Next() {
		var (
			t         Token
			createdAt int64
			expiresAt int64
			usedAt    sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.Status, &createdAt, &expiresAt, &usedAt); err != nil {
			return nil, fmt.Errorf("enrollment: scan: %w", err)
		}
		t.CreatedAt = time.UnixMilli(createdAt)
		t.ExpiresAt = time.UnixMilli(expiresAt)
		if usedAt.Valid {
			t.UsedAt = time.UnixMilli(usedAt.Int64)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("enrollment: rows: %w", err)
	}
	return out, nil
}

// Revoke marks the pending token as revoked. Idempotent for tokens
// already in revoked state; tokens already used cannot be revoked
// (the device exists; revoke the device instead).
func Revoke(ctx context.Context, db *sql.DB, id string, now time.Time) error {
	res, err := db.ExecContext(ctx, `
		UPDATE enrollment_tokens
		   SET status = ?
		 WHERE id = ? AND status = ?
	`, StatusRevoked, id, StatusPending)
	if err != nil {
		return fmt.Errorf("enrollment: revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Disambiguate not-found vs wrong-status by re-reading the row.
		var status string
		err := db.QueryRowContext(ctx, `SELECT status FROM enrollment_tokens WHERE id = ?`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == StatusRevoked {
			return nil // idempotent
		}
		return fmt.Errorf("enrollment: cannot revoke token in status %q", status)
	}
	_ = now // reserved for future revoked_at column; not in schema yet
	return nil
}

// Consume claims the plaintext token: validates expiry + status, then
// flips the row to `used`. Returns the token ID on success so callers
// can record it in the resulting device's audit trail.
//
// MUST be wrapped in a transaction by the caller when the device
// insert needs to happen atomically with the token transition; t5
// owns that flow.
func Consume(ctx context.Context, dbtx interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}, plaintext string, now time.Time) (id string, err error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", ErrNotFound
	}
	h := hashToken(plaintext)

	var (
		status    string
		expiresAt int64
	)
	row := dbtx.QueryRowContext(ctx, `
		SELECT id, status, expires_at FROM enrollment_tokens WHERE token_hash = ?
	`, h)
	if err := row.Scan(&id, &status, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("enrollment: lookup: %w", err)
	}
	if status != StatusPending {
		return "", ErrNotUsable
	}
	if !now.Before(time.UnixMilli(expiresAt)) {
		return "", ErrExpired
	}
	if _, err := dbtx.ExecContext(ctx, `
		UPDATE enrollment_tokens
		   SET status = ?, used_at = ?
		 WHERE id = ? AND status = ?
	`, StatusUsed, now.UnixMilli(), id, StatusPending); err != nil {
		return "", fmt.Errorf("enrollment: mark used: %w", err)
	}
	return id, nil
}

// generate returns "sfen_<24 base64url chars>" backed by 18 random
// bytes (which encode to 24 base64url chars exactly).
func generate() (string, error) {
	var raw [18]byte // 18 bytes -> 24 base64url chars
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("enrollment: rand: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
