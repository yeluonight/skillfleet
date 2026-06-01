// Middleware that enforces the v1.0 §4.2 HMAC signing scheme on every
// /agent/* route except enrolment. The order of checks is chosen so
// cheap rejections happen first (header presence, time window) and
// the secret-dependent comparison runs last; every failure surfaces
// the same 401 envelope plus an `error` code so the agent can log
// which step tripped without revealing it to opportunistic probes.

package agentapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
)

// MaxAgentBodyBytes caps the request body the middleware will buffer
// for hashing. Heartbeat payloads sit well under 1 KiB; cap is
// generous for future inventory snapshots without enabling abuse.
const MaxAgentBodyBytes = 1 << 20 // 1 MiB

// AuthContext carries the identity attached to a signed request. The
// per-route handler retrieves it via FromContext to access device id
// and metadata without re-querying.
type AuthContext struct {
	DeviceID string
	Status   string // always "approved" — middleware rejects others
}

type ctxKey int

const ctxKeyAuth ctxKey = iota

// FromContext returns the AuthContext attached by Authenticate. Inside
// a handler mounted behind Authenticate the second return is always
// true.
func FromContext(ctx context.Context) (AuthContext, bool) {
	a, ok := ctx.Value(ctxKeyAuth).(AuthContext)
	return a, ok
}

// Authenticate wraps next with HMAC validation. Use it on every
// /agent/* route except /agent/enroll.
//
// Steps, in order:
//
//  1. Parse the five X-SF-* headers; ErrMissingHeader / ErrBadTimestamp
//     short-circuit with bad_signature so probes can't differentiate.
//  2. Reject if |now - request_ts| > MaxClockSkew. Tight window means
//     stale replays die immediately even before nonce lookup.
//  3. Look up device (status + HMAC key) in one query. Unknown device
//     and missing-secret collapse to the same response; non-approved
//     status surfaces as device_not_approved so the operator can
//     diagnose.
//  4. Buffer the body (capped) and recompute its sha256. Mismatch
//     means the agent sent inconsistent headers/body; reject.
//  5. Try to INSERT the nonce as (device_id, nonce, used_at). UNIQUE
//     PK violation = replay. Done before signature verify so a
//     reused signature can't be exploited even if it would otherwise
//     match.
//  6. Verify the HMAC signature. Constant-time comparison.
//  7. Best-effort touch last_seen_at; failures are logged, not raised.
//  8. Attach AuthContext, call next with body restored via NopCloser.
func (d Deps) Authenticate(next http.Handler) http.Handler {
	skew := d.MaxClockSkew
	if skew == 0 {
		skew = DefaultMaxClockSkew
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: parse + presence check.
		hdr, err := sfhmac.Parse(r)
		if err != nil {
			d.writeAuthError(w, http.StatusUnauthorized, "bad_signature", "missing or malformed signature headers")
			return
		}

		// Step 2: time window. Use the wall clock from d.Now to keep
		// tests deterministic.
		now := d.Now()
		if delta := now.Sub(hdr.Timestamp); delta > skew || delta < -skew {
			d.writeAuthError(w, http.StatusUnauthorized, "timestamp_out_of_window", "request timestamp outside accepted window")
			return
		}

		// Step 3: device lookup + status gate.
		key, status, err := devices.LookupHMACKey(r.Context(), d.DB, hdr.DeviceID)
		switch {
		case errors.Is(err, devices.ErrNotFound), errors.Is(err, devices.ErrSecretNotSet):
			d.writeAuthError(w, http.StatusUnauthorized, "bad_signature", "device not authorised")
			return
		case err != nil:
			d.logErr("agentapi: lookup hmac key", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		if status != devices.StatusApproved {
			d.writeAuthError(w, http.StatusForbidden, "device_not_approved", "device awaiting approval or revoked")
			return
		}

		// Step 4: buffer body + recompute hash.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxAgentBodyBytes))
		if err != nil {
			d.writeAuthError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
			return
		}
		if sfhmac.BodySHA256(body) != hdr.BodyHash {
			d.writeAuthError(w, http.StatusUnauthorized, "body_mismatch", "X-SF-Body-SHA256 does not match body")
			return
		}

		// Step 5: nonce uniqueness. INSERT before signature verify so
		// even a leaked-signature replay can't slip through during
		// the window where signature is still valid.
		if err := insertNonce(r.Context(), d.DB, hdr.DeviceID, hdr.Nonce, now); err != nil {
			if errors.Is(err, errNonceReplay) {
				d.writeAuthError(w, http.StatusUnauthorized, "nonce_replay", "nonce already used")
				return
			}
			d.logErr("agentapi: insert nonce", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}

		// Step 6: signature verify. The HMAC key stored in
		// device_secrets.secret_hash equals sha256(plaintext); the
		// agent computes the same value via devices.HMACKey().
		if err := sfhmac.Verify(key, r.Method, r.URL.Path, hdr.Timestamp, hdr.Nonce, hdr.BodyHash, hdr.Signature); err != nil {
			d.writeAuthError(w, http.StatusUnauthorized, "bad_signature", "signature mismatch")
			return
		}

		// Step 7: best-effort last_seen update — never fatal.
		if err := devices.TouchLastSeen(r.Context(), d.DB, hdr.DeviceID, now); err != nil {
			d.logErr("agentapi: touch last_seen", err)
		}

		// Step 8: rebuild the request with the buffered body so the
		// per-route handler can re-parse it without losing bytes.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))

		ctx := context.WithValue(r.Context(), ctxKeyAuth, AuthContext{
			DeviceID: hdr.DeviceID,
			Status:   status,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// errNonceReplay is the sentinel returned by insertNonce when the PK
// conflict fires. The underlying driver reports it as "UNIQUE
// constraint failed", which is implementation-detail; this sentinel
// is the API the middleware programs against.
var errNonceReplay = errors.New("agentapi: nonce already used")

// insertNonce attempts to claim (device_id, nonce) in agent_nonces.
// The composite primary key (device_id, nonce) declared in migration
// 0004 turns any duplicate into a constraint error.
func insertNonce(ctx context.Context, db *sql.DB, deviceID, nonce string, now time.Time) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO agent_nonces(device_id, nonce, used_at) VALUES (?, ?, ?)`,
		deviceID, nonce, now.UnixMilli(),
	)
	if err == nil {
		return nil
	}
	// modernc.org/sqlite reports UNIQUE failures via the SQL error
	// string. Checking for substrings keeps this file free of a
	// direct driver import.
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: agent_nonces") {
		return errNonceReplay
	}
	return err
}

// writeAuthError emits the JSON error envelope and logs the failed
// step at debug level. Logging the precise reason on the server (but
// not on the wire) is the operator's diagnostic path.
func (d Deps) writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	if d.Logger != nil {
		d.Logger.Debug("agentapi: auth rejected",
			slog.String("error", code),
			slog.Int("status", status),
		)
	}
	writeError(w, status, code, msg)
	// Audit hook: only record device_not_approved so the table doesn't
	// fill with probe traffic. Other rejection reasons stay in logs.
	if d.Audit != nil && code == "device_not_approved" {
		d.Audit.Write(context.Background(), audit.Record{
			Actor:  audit.Actor{Type: "system"},
			Action: "device.unauthorized",
		})
	}
}
