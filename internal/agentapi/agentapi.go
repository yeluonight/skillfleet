// Package agentapi exposes the /agent/* HTTP surface used by
// skillfleet-agent processes (v1.0 §14.2).
//
// Routes:
//
//	POST /agent/enroll      one-shot enrolment, gated by an
//	                        enrollment_token (no HMAC yet — the
//	                        agent has no secret).
//
// All other /agent/* routes (phase 2 t8+) sit behind Authenticate(),
// the middleware defined in middleware.go that enforces the v1.0
// §4.2 signing scheme + nonce-uniqueness + device-approved gate.
package agentapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
)

// DefaultMaxClockSkew is the symmetric window the HMAC middleware
// accepts between request timestamp and server now (v2.0 §5.7).
const DefaultMaxClockSkew = 5 * time.Minute

// Deps holds the references every handler needs.
type Deps struct {
	DB     *sql.DB
	Logger *slog.Logger
	Now    func() time.Time
	Audit  *audit.Logger
	// MaxClockSkew, when non-zero, overrides DefaultMaxClockSkew. Tests
	// set this to short values to exercise the time-window rejection.
	MaxClockSkew time.Duration
	// Packages serves version archives to GET /agent/packages/{id}. When
	// nil that route returns 503 (package serving not configured). The
	// server wires an adapter over the registry store.
	Packages PackageSource
}

// NewRouter returns an http.Handler with the phase-2 /agent/* routes
// wired. Mount under the same mux as the WebUI / /api tree; the
// patterns include the /agent prefix.
//
// /agent/enroll is intentionally NOT behind Authenticate — the agent
// has no secret yet at enrolment time. Every other /agent/* pattern
// registered in this file MUST go through d.Authenticate().
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.MaxClockSkew == 0 {
		d.MaxClockSkew = DefaultMaxClockSkew
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/enroll", d.handleEnroll)
	// HMAC-guarded routes.
	mux.Handle("POST /agent/heartbeat", d.Authenticate(http.HandlerFunc(d.handleHeartbeat)))
	mux.Handle("POST /agent/inventory", d.Authenticate(http.HandlerFunc(d.handleInventory)))
	// Downlink (phase 8): claim jobs, pull packages, report results. All
	// HMAC-guarded so a job/package is only ever served to an enrolled,
	// approved device, and a job is only ever claimed/completed by the
	// device it is addressed to.
	mux.Handle("GET /agent/jobs", d.Authenticate(http.HandlerFunc(d.handleGetJobs)))
	mux.Handle("GET /agent/packages/{id}", d.Authenticate(http.HandlerFunc(d.handleGetPackage)))
	mux.Handle("POST /agent/jobs/{id}/result", d.Authenticate(http.HandlerFunc(d.handleJobResult)))
	return mux
}

type enrollRequest struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Hostname     string `json:"hostname,omitempty"`
	OS           string `json:"os,omitempty"`
	Arch         string `json:"arch,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
}

type enrollResponse struct {
	DeviceID     string `json:"device_id"`
	DeviceSecret string `json:"device_secret"` // returned exactly once
	Status       string `json:"status"`
}

func (d Deps) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return
	}
	var req enrollRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Name = strings.TrimSpace(req.Name)
	if req.Token == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "token and name are required")
		return
	}

	// One transaction so token consume + device insert + secret insert
	// either all land or none do. If the device insert fails after the
	// token is marked used, the rollback restores the token row to
	// pending so the operator can retry.
	tx, err := d.DB.BeginTx(r.Context(), nil)
	if err != nil {
		d.logErr("enroll: begin tx", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	tokenID, err := enrollment.Consume(r.Context(), tx, req.Token, d.Now())
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, enrollment.ErrNotFound):
		writeError(w, http.StatusForbidden, "token_not_found", "enrolment token not recognised")
		return
	case errors.Is(err, enrollment.ErrExpired):
		writeError(w, http.StatusForbidden, "token_expired", "enrolment token expired")
		return
	case errors.Is(err, enrollment.ErrNotUsable):
		writeError(w, http.StatusForbidden, "token_not_usable", "enrolment token already used or revoked")
		return
	default:
		d.logErr("enroll: consume token", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	res, err := devices.Enroll(r.Context(), tx, devices.EnrollInput{
		Name:         req.Name,
		Hostname:     req.Hostname,
		OS:           req.OS,
		Arch:         req.Arch,
		AgentVersion: req.AgentVersion,
	}, d.Now())
	if err != nil {
		// devices.Enroll only rejects empty name (we already checked)
		// — anything else is a DB-level surprise.
		d.logErr("enroll: insert device", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if err := tx.Commit(); err != nil {
		d.logErr("enroll: commit", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	committed = true

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "system"},
			Action: "device.enrolled",
			Target: audit.Target{Type: "device", ID: res.Device.ID},
			Detail: map[string]any{
				"token_id":      tokenID,
				"name":          res.Device.Name,
				"hostname":      res.Device.Hostname,
				"os":            res.Device.OS,
				"arch":          res.Device.Arch,
				"agent_version": res.Device.AgentVersion,
			},
		})
	}

	writeJSON(w, http.StatusCreated, enrollResponse{
		DeviceID:     res.Device.ID,
		DeviceSecret: res.Secret,
		Status:       res.Device.Status,
	})
}

// --- helpers (mirrored from internal/api so /agent stays free of
// cross-package shared internals; the duplication is small and keeps
// the two trees independent for testing.) ---

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: code, Message: msg})
}

func (d Deps) logErr(msg string, err error) {
	if d.Logger == nil {
		return
	}
	d.Logger.Error(msg, slog.String("err", err.Error()))
}
