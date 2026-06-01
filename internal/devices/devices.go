// Package devices owns CRUD-shaped operations on the devices and
// device_secrets tables. Enrolment, approval, and revocation all flow
// through here so the state-machine invariants live in one place.
//
// device_secrets stores sha256(secret) and that hash doubles as the
// HMAC key used to verify /agent/* requests (v1.0 §4.2). The plaintext
// is returned exactly once to the agent at enrolment so the Agent can
// reproduce the same hash on demand; the server never persists the
// plaintext. Comparison uses constant-time equality.
//
// Status vocabulary mirrors the DB CHECK in migrations/0004_devices.sql:
//
//	pending  -- enrolled with a valid token but not yet admin-approved
//	approved -- HMAC requests accepted
//	revoked  -- HMAC requests rejected; row kept for audit / re-enroll
package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRevoked  = "revoked"

	// SecretByteLen is the entropy bundled into each device_secret.
	// 32 bytes -> 43 base64url chars -> roughly the same shape as the
	// session token, so operators recognise the format.
	SecretByteLen = 32
)

// Errors surfaced by package operations.
var (
	ErrNotFound       = errors.New("devices: not found")
	ErrInvalidStatus  = errors.New("devices: status transition not allowed")
	ErrSecretMismatch = errors.New("devices: secret mismatch")
	ErrSecretNotSet   = errors.New("devices: secret row missing")
)

// Device is the in-memory projection of a row in devices.
type Device struct {
	ID           string
	Name         string
	Hostname     string
	OS           string
	Arch         string
	AgentVersion string
	Status       string
	CreatedAt    time.Time
	LastSeenAt   time.Time // zero if the agent has never reported
}

// EnrollInput carries the operator-controllable metadata supplied by
// the agent at enrolment time. Validation here is intentionally
// lenient — the agent self-reports these and they're informational,
// not security-sensitive. Empty strings become SQL NULLs.
type EnrollInput struct {
	Name         string
	Hostname     string
	OS           string
	Arch         string
	AgentVersion string
}

// EnrollResult is what gets returned to the agent. Secret is plaintext;
// the agent persists it in agent.json (0o600), the server forgets it.
type EnrollResult struct {
	Device Device
	Secret string // returned exactly once
}

// Enroll inserts a device row + matching device_secret row inside the
// caller-provided transaction. The caller is responsible for opening
// the tx and committing it; this keeps the token consume (in
// internal/enrollment) and the device insert atomic.
//
// Returns the freshly minted device + the plaintext secret. The
// secret never returns from the database after this call — only its
// sha256 is stored.
func Enroll(ctx context.Context, tx *sql.Tx, in EnrollInput, now time.Time) (EnrollResult, error) {
	if in.Name == "" {
		return EnrollResult{}, errors.New("devices: name must not be empty")
	}
	secret, err := generateSecret()
	if err != nil {
		return EnrollResult{}, err
	}
	dev := Device{
		ID:           idgen.New("dev"),
		Name:         in.Name,
		Hostname:     in.Hostname,
		OS:           in.OS,
		Arch:         in.Arch,
		AgentVersion: in.AgentVersion,
		Status:       StatusPending,
		CreatedAt:    now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices(id, name, hostname, os, arch, agent_version, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		dev.ID, dev.Name,
		nullable(dev.Hostname), nullable(dev.OS),
		nullable(dev.Arch), nullable(dev.AgentVersion),
		dev.Status, dev.CreatedAt.UnixMilli(),
	); err != nil {
		return EnrollResult{}, fmt.Errorf("devices: insert device: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_secrets(device_id, secret_hash, created_at)
		VALUES (?, ?, ?)
	`, dev.ID, hashSecret(secret), now.UnixMilli()); err != nil {
		return EnrollResult{}, fmt.Errorf("devices: insert secret: %w", err)
	}
	return EnrollResult{Device: dev, Secret: secret}, nil
}

// Get returns the device row matching id, without touching
// device_secrets. Used by HMAC middleware (status + last-seen) and
// the WebUI list / detail pages.
func Get(ctx context.Context, db DBExec, id string) (Device, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, name, hostname, os, arch, agent_version, status, created_at, last_seen_at
		  FROM devices WHERE id = ?
	`, id)
	return scanDevice(row)
}

// VerifySecret returns nil iff the plaintext matches the stored hash.
// Constant-time comparison.
func VerifySecret(ctx context.Context, db DBExec, deviceID, plaintext string) error {
	var stored string
	err := db.QueryRowContext(ctx,
		`SELECT secret_hash FROM device_secrets WHERE device_id = ?`,
		deviceID,
	).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSecretNotSet
	}
	if err != nil {
		return fmt.Errorf("devices: read secret: %w", err)
	}
	candidate := HMACKey(plaintext)
	if subtle.ConstantTimeCompare([]byte(stored), []byte(candidate)) != 1 {
		return ErrSecretMismatch
	}
	return nil
}

// HMACKey returns the value both sides use as the HMAC-SHA256 key when
// signing /agent/* requests. It is the lowercase hex sha256 of the
// device's plaintext secret — i.e. exactly what the server stores in
// device_secrets.secret_hash. The Agent derives the same value on the
// fly from agent.json's device_secret; the server reads it directly
// from the row.
//
// Using a stable derivation (sha256) rather than the plaintext as the
// HMAC key is what lets the server avoid storing the plaintext while
// still being able to verify signatures.
func HMACKey(plaintext string) string {
	return hashSecret(plaintext)
}

// LookupHMACKey reads the device's HMAC key (i.e. secret_hash) and
// device status in one round-trip. Returns ErrNotFound if no such
// device, ErrSecretNotSet if the row exists but has no secret yet.
// The HMAC middleware uses this to gate on status without needing
// two queries.
func LookupHMACKey(ctx context.Context, db DBExec, deviceID string) (key, status string, err error) {
	row := db.QueryRowContext(ctx, `
		SELECT d.status, s.secret_hash
		  FROM devices d
		  LEFT JOIN device_secrets s ON s.device_id = d.id
		 WHERE d.id = ?
	`, deviceID)
	var (
		st     string
		secret sql.NullString
	)
	if err := row.Scan(&st, &secret); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("devices: lookup hmac key: %w", err)
	}
	if !secret.Valid {
		return "", st, ErrSecretNotSet
	}
	return secret.String, st, nil
}

// TouchLastSeen updates last_seen_at to now. Best-effort: a write
// failure must not abort the underlying agent request.
func TouchLastSeen(ctx context.Context, db DBExec, deviceID string, now time.Time) error {
	_, err := db.ExecContext(ctx,
		`UPDATE devices SET last_seen_at = ? WHERE id = ?`,
		now.UnixMilli(), deviceID,
	)
	return err
}

// List returns all devices ordered by creation time, newest first,
// capped at limit. Used by the WebUI Devices page (t9).
func List(ctx context.Context, db DBExec, limit int) ([]Device, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, hostname, os, arch, agent_version, status, created_at, last_seen_at
		  FROM devices ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("devices: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Device
	for rows.Next() {
		dev, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dev)
	}
	return out, rows.Err()
}

// SetStatus moves a device through the {pending, approved, revoked}
// state machine. Only these transitions are accepted:
//
//	pending  -> approved
//	pending  -> revoked
//	approved -> revoked
//
// Any other transition (including a no-op move to the current state)
// returns ErrInvalidStatus. Callers that want idempotent approve /
// revoke should branch on the current status first.
func SetStatus(ctx context.Context, db DBExec, id, want string) error {
	dev, err := Get(ctx, db, id)
	if err != nil {
		return err
	}
	if !allowed(dev.Status, want) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidStatus, dev.Status, want)
	}
	_, err = db.ExecContext(ctx,
		`UPDATE devices SET status = ? WHERE id = ? AND status = ?`,
		want, id, dev.Status,
	)
	return err
}

// --- helpers ---

// DBExec is the subset of database/sql we use. *sql.DB and *sql.Tx
// both satisfy this so handlers can hand in whichever they have.
type DBExec interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func allowed(from, to string) bool {
	switch {
	case from == StatusPending && to == StatusApproved:
		return true
	case from == StatusPending && to == StatusRevoked:
		return true
	case from == StatusApproved && to == StatusRevoked:
		return true
	}
	return false
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDevice(s rowScanner) (Device, error) {
	var (
		d Device
		// SQL NULLs.
		hostname, os, arch, av sql.NullString
		createdAt              int64
		lastSeenAt             sql.NullInt64
	)
	if err := s.Scan(&d.ID, &d.Name, &hostname, &os, &arch, &av, &d.Status, &createdAt, &lastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Device{}, ErrNotFound
		}
		return Device{}, fmt.Errorf("devices: scan: %w", err)
	}
	d.Hostname = hostname.String
	d.OS = os.String
	d.Arch = arch.String
	d.AgentVersion = av.String
	d.CreatedAt = time.UnixMilli(createdAt)
	if lastSeenAt.Valid {
		d.LastSeenAt = time.UnixMilli(lastSeenAt.Int64)
	}
	return d, nil
}

func generateSecret() (string, error) {
	var raw [SecretByteLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("devices: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashSecret(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
