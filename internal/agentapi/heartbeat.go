// /agent/heartbeat: liveness ping from the agent. The middleware has
// already validated the signature, time window, nonce uniqueness, and
// device approval status — by the time this handler runs the only
// remaining decisions are:
//
//   - decode the (small, optional) JSON payload
//   - propagate any agent_version change so the WebUI shows the truth
//   - return a 200 with a hint envelope
//
// last_seen_at is updated by the middleware; the handler must not
// re-touch it.

package agentapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const maxHeartbeatBody = 4 * 1024

type heartbeatRequest struct {
	AgentVersion string `json:"agent_version,omitempty"`
}

type heartbeatResponse struct {
	Status string `json:"status"`
}

func (d Deps) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	ac, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing auth context")
		return
	}

	// Empty body is acceptable — the middleware has already verified
	// the matching body hash. Anything else with a non-JSON body would
	// be a client bug, so we still gate on Content-Type when present.
	var req heartbeatRequest
	if r.ContentLength > 0 {
		if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHeartbeatBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, sql.ErrNoRows) {
			// io.EOF on a zero-length body is fine; anything else means
			// the payload was malformed.
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
	}

	if v := strings.TrimSpace(req.AgentVersion); v != "" {
		// Only update when the value differs to keep WAL churn quiet.
		if err := updateAgentVersionIfChanged(r.Context(), d.DB, ac.DeviceID, v); err != nil {
			d.logErr("heartbeat: update agent_version", err)
		}
	}

	writeJSON(w, http.StatusOK, heartbeatResponse{Status: "ok"})
}

// updateAgentVersionIfChanged writes only when the device's recorded
// agent_version diverges from the supplied one. SQLite's UPDATE will
// still take a write lock even on a no-op, so the read-first pattern
// is preferable for a high-rate route.
func updateAgentVersionIfChanged(ctx context.Context, db *sql.DB, deviceID, v string) error {
	var current sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT agent_version FROM devices WHERE id = ?`, deviceID,
	).Scan(&current); err != nil {
		return err
	}
	if current.Valid && current.String == v {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`UPDATE devices SET agent_version = ? WHERE id = ?`, v, deviceID,
	)
	return err
}
