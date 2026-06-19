// Package setup owns the one-time admin bootstrap flow:
//
//  1. On every server boot, EnsureCode inspects users + setup_state.
//     If no admin exists yet, it (re-)generates a setup code, persists
//     its sha256, and returns the plaintext for the caller to print
//     to stderr (the only place it is ever displayed).
//
//  2. POST /api/setup eventually calls Consume to validate the code,
//     create the first admin user, and mark the setup row consumed.
//
// Both operations run inside transactions so a concurrent boot or
// duplicate POST cannot create two admins.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/auth"
	"github.com/yeluonight/skillfleet/internal/idgen"
)

// CodePrefix is the human-recognisable banner on every setup code.
// `SF-SETUP-` lets operators grep the boot log and know what they
// found. The two trailing groups are 4 chars each from a 28-char
// Crockford-style alphabet (no I/L/O/U to avoid confusion).
const (
	CodePrefix    = "SF-SETUP-"
	codeAlphabet  = "ABCDEFGHJKMNPQRSTVWXYZ23456789" // Crockford, dropped vowels U, ambiguous I/L/O
	codeGroupSize = 4
	codeGroups    = 2
)

// Errors returned from Consume.
var (
	ErrAlreadyConsumed = errors.New("setup: already consumed")
	ErrCodeMismatch    = errors.New("setup: code mismatch")
	ErrNoPending       = errors.New("setup: no pending setup")
)

// Status reports whether the bootstrap is still required.
type Status struct {
	Required bool // true iff users table is empty
}

// CurrentStatus reports whether the server still needs bootstrapping.
// It does NOT touch the setup_state row.
func CurrentStatus(ctx context.Context, db *sql.DB) (Status, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return Status{}, fmt.Errorf("setup: count users: %w", err)
	}
	return Status{Required: n == 0}, nil
}

// EnsureCode (re-)generates the bootstrap code when, and only when,
// the users table is empty. The plaintext code is returned to the
// caller for stderr display; the database stores only its sha256.
//
// If users already exist, ok=false and code="" — the caller should
// skip stderr printing.
//
// Calling EnsureCode twice on the same empty database rotates the
// code: this matches the "whatever the most recent boot log says is
// authoritative" contract documented in migrations/0003_setup_state.sql.
func EnsureCode(ctx context.Context, db *sql.DB, now time.Time) (code string, ok bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("setup: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var users int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return "", false, fmt.Errorf("setup: count users in tx: %w", err)
	}
	if users > 0 {
		return "", false, nil
	}

	code, err = generateCode()
	if err != nil {
		return "", false, err
	}
	hash := hashCode(code)
	nowMs := now.UnixMilli()

	// Upsert into the singleton row. The CHECK constraint on the
	// migration guarantees id=1 is the only legal value.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO setup_state (id, code_hash, code_created_at, consumed_at, consumed_by_user_id)
		VALUES (1, ?, ?, NULL, NULL)
		ON CONFLICT(id) DO UPDATE SET
			code_hash = excluded.code_hash,
			code_created_at = excluded.code_created_at,
			consumed_at = NULL,
			consumed_by_user_id = NULL
	`, hash, nowMs); err != nil {
		return "", false, fmt.Errorf("setup: persist code hash: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("setup: commit: %w", err)
	}
	return code, true, nil
}

// Consume validates the operator-supplied code, creates the first
// admin user, and marks the setup row consumed — all atomically.
//
// Returns the newly created user id on success.
func Consume(ctx context.Context, db *sql.DB, code, username, password string, now time.Time) (userID string, err error) {
	if err := validateUsername(username); err != nil {
		return "", err
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}

	pwHash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("setup: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&existing); err != nil {
		return "", fmt.Errorf("setup: count users in tx: %w", err)
	}
	if existing > 0 {
		return "", ErrAlreadyConsumed
	}

	var storedHash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT code_hash FROM setup_state WHERE id = 1`).Scan(&storedHash)
	if errors.Is(err, sql.ErrNoRows) || !storedHash.Valid {
		return "", ErrNoPending
	}
	if err != nil {
		return "", fmt.Errorf("setup: read code hash: %w", err)
	}

	if !constantTimeEqual(storedHash.String, hashCode(code)) {
		return "", ErrCodeMismatch
	}

	userID = idgen.New("usr")
	nowMs := now.UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, created_at)
		VALUES (?, ?, ?, ?)
	`, userID, username, pwHash, nowMs); err != nil {
		return "", fmt.Errorf("setup: insert user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE setup_state
		   SET code_hash = NULL, code_created_at = NULL,
		       consumed_at = ?, consumed_by_user_id = ?
		 WHERE id = 1
	`, nowMs, userID); err != nil {
		return "", fmt.Errorf("setup: mark consumed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("setup: commit: %w", err)
	}
	return userID, nil
}

// generateCode returns a fresh setup code in the form
// "SF-SETUP-AAAA-BBBB". Cryptographic randomness is read from the
// kernel CSPRNG via crypto/rand.
func generateCode() (string, error) {
	const total = codeGroups * codeGroupSize
	out := make([]byte, total)
	if _, err := rand.Read(out); err != nil {
		return "", fmt.Errorf("setup: rand: %w", err)
	}
	for i := 0; i < total; i++ {
		out[i] = codeAlphabet[int(out[i])%len(codeAlphabet)]
	}
	var b strings.Builder
	b.Grow(len(CodePrefix) + total + codeGroups - 1)
	b.WriteString(CodePrefix)
	for g := 0; g < codeGroups; g++ {
		if g > 0 {
			b.WriteByte('-')
		}
		b.Write(out[g*codeGroupSize : (g+1)*codeGroupSize])
	}
	return b.String(), nil
}

func hashCode(code string) string {
	// Hash the canonical form so "sf-setup-aaaa-bbbb" and the
	// upper-case variant produce the same digest.
	sum := sha256.Sum256([]byte(canonicalize(code)))
	return hex.EncodeToString(sum[:])
}

// canonicalize normalises operator input before hashing/comparison:
// trim whitespace, drop dashes, upper-case. This lets the operator
// retype with or without dashes and in either case.
func canonicalize(code string) string {
	code = strings.TrimSpace(code)
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return strings.ToUpper(code)
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func validateUsername(u string) error {
	u = strings.TrimSpace(u)
	if len(u) < 3 || len(u) > 64 {
		return errors.New("setup: username must be 3-64 chars")
	}
	for _, c := range u {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return fmt.Errorf("setup: username contains invalid char %q", c)
		}
	}
	return nil
}

func validatePassword(p string) error {
	// Length-only check at the platform layer. Strength rules belong
	// in WebUI (per §13 hint), not the server: we don't want to
	// reject perfectly fine passphrases just because they lack a
	// digit.
	if len(p) < 12 {
		return errors.New("setup: password must be at least 12 chars")
	}
	if len(p) > 256 {
		return errors.New("setup: password too long (max 256)")
	}
	return nil
}
