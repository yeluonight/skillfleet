// Package session owns server-side sessions backed by the `sessions`
// table. Sessions are opaque tokens: the cookie carries a random 32
// bytes; the DB stores only sha256(token) so a database read does not
// yield a usable session.
//
// Phase 1 t5 lands Create + Lookup + Revoke. CSRF lives in t6.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

// CookieName is the name of the HTTP cookie carrying the session token.
const CookieName = "sf_session"

// tokenLen is the number of random bytes in the raw cookie value. 32
// bytes → 256 bits of entropy → 43 base64url chars after encoding.
const tokenLen = 32

// Errors surfaced by Lookup.
var (
	ErrNotFound = errors.New("session: not found")
	ErrExpired  = errors.New("session: expired")
	ErrRevoked  = errors.New("session: revoked")
)

// Session is the in-memory projection of a row in `sessions`.
type Session struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Create mints a new session for userID and inserts it into the DB.
// The raw token is returned to the caller exactly once (to ship via
// Set-Cookie); it is not stored anywhere on the server.
func Create(ctx context.Context, db *sql.DB, userID, ip, userAgent string, ttl time.Duration, now time.Time) (sess Session, token string, err error) {
	if ttl <= 0 {
		return Session{}, "", fmt.Errorf("session: ttl must be positive, got %s", ttl)
	}
	token, err = newToken()
	if err != nil {
		return Session{}, "", err
	}
	sess = Session{
		ID:         idgen.New("ses"),
		UserID:     userID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(ttl),
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, session_hash, ip, user_agent, created_at, last_seen_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sess.ID, sess.UserID, hashToken(token),
		nullableString(ip), nullableString(userAgent),
		sess.CreatedAt.UnixMilli(), sess.LastSeenAt.UnixMilli(), sess.ExpiresAt.UnixMilli(),
	); err != nil {
		return Session{}, "", fmt.Errorf("session: insert: %w", err)
	}
	return sess, token, nil
}

// Lookup returns the session matching token if it is live (not
// expired, not revoked). It also updates last_seen_at when ok.
func Lookup(ctx context.Context, db *sql.DB, token string, now time.Time) (Session, error) {
	if token == "" {
		return Session{}, ErrNotFound
	}
	h := hashToken(token)

	var (
		s          Session
		createdAt  int64
		lastSeenAt int64
		expiresAt  int64
		revokedAt  sql.NullInt64
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, user_id, created_at, last_seen_at, expires_at, revoked_at
		  FROM sessions
		 WHERE session_hash = ?
	`, h).Scan(&s.ID, &s.UserID, &createdAt, &lastSeenAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("session: lookup: %w", err)
	}
	if revokedAt.Valid {
		return Session{}, ErrRevoked
	}
	s.CreatedAt = time.UnixMilli(createdAt)
	s.LastSeenAt = time.UnixMilli(lastSeenAt)
	s.ExpiresAt = time.UnixMilli(expiresAt)
	if !now.Before(s.ExpiresAt) {
		return Session{}, ErrExpired
	}

	// Touch last_seen_at as a best-effort metadata update. The session has
	// already passed every authentication check above (found, not revoked,
	// not expired), so a failed touch must NOT fail the lookup: under WAL +
	// busy_timeout, concurrent requests in the same batch can still race the
	// write and surface SQLITE_BUSY, and turning that into a returned error
	// makes requireAuth emit a spurious 401. Record the new time on the
	// returned session optimistically; swallow a write error (the next
	// successful request re-touches).
	_, _ = db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now.UnixMilli(), s.ID)
	s.LastSeenAt = now
	return s, nil
}

// Revoke marks a session as revoked. Idempotent.
func Revoke(ctx context.Context, db *sql.DB, sessionID string, now time.Time) error {
	if _, err := db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		now.UnixMilli(), sessionID,
	); err != nil {
		return fmt.Errorf("session: revoke: %w", err)
	}
	return nil
}

// newToken returns a 32-byte cryptographic random token, base64url
// encoded without padding.
func newToken() (string, error) {
	var raw [tokenLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("session: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// hashToken returns the lowercase hex sha256 of token. The DB column
// stores this; the raw token is never persisted.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// nullableString converts "" to sql.NullString{Valid:false} so empty
// IP / UA strings land as SQL NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
