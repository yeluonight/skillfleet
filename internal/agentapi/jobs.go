// /agent/jobs + /agent/jobs/{id}/result: the downlink work channel
// (v1.0 §14.2). The HMAC middleware has already authenticated the device
// and injected its id into the context, so these handlers trust
// AuthContext.DeviceID as the caller's identity — an agent can only ever
// see and complete jobs addressed to its own device.
//
//   GET  /agent/jobs              claim the next pending job for this
//                                 device (CAS; returns 204 when none)
//   POST /agent/jobs/{id}/result  report a terminal result for a job the
//                                 device has claimed
//
// The deploy.Store is constructed per request over the shared DB handle
// (it is stateless), mirroring how other handlers build their stores; no
// new long-lived dependency is added for the jobs path.

package agentapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/deploy"
)

// handleGetJobs claims and returns the next pending job for the
// authenticated device, or 204 No Content when the device has no
// claimable work. The claim is atomic (deploy.Store.ClaimNext), so two
// agent processes for the same device never both run one job. The
// response shape is deploy.ClaimedJob (shared with the agent client).
func (d Deps) handleGetJobs(w http.ResponseWriter, r *http.Request) {
	ac, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing auth context")
		return
	}

	store := deploy.New(d.DB)
	job, claimed, err := store.ClaimNext(r.Context(), ac.DeviceID, d.Now())
	if err != nil {
		d.logErr("agent jobs: claim", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "device", ID: ac.DeviceID},
			Action: "deployment.claimed",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{"operation": string(job.Operation)},
		})
	}

	writeJSON(w, http.StatusOK, deploy.ClaimedJob{
		ID:          job.ID,
		Operation:   string(job.Operation),
		RequestJSON: job.RequestJSON,
		PlanJSON:    job.PlanJSON,
	})
}

// jobResultRequest is the agent's report, shared with the agent client as
// deploy.JobResult. Status must be "succeeded" or "failed"; result_json is
// the deploy.Result the agent produced.

// handleJobResult records a terminal result for a job. It enforces that
// the job belongs to the reporting device (an agent cannot complete
// another device's job) and that the job is in a reportable state
// (claimed/running) — both via the store, which returns typed errors we
// map to status codes.
func (d Deps) handleJobResult(w http.ResponseWriter, r *http.Request) {
	ac, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing auth context")
		return
	}
	jobID := r.PathValue("id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing job id")
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return
	}
	var req deploy.JobResult
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid result JSON: "+err.Error())
		return
	}

	status := deploy.Status(req.Status)
	if status != deploy.StatusSucceeded && status != deploy.StatusFailed {
		writeError(w, http.StatusBadRequest, "bad_request", "status must be succeeded or failed")
		return
	}

	store := deploy.New(d.DB)
	// Ownership check: the job must exist AND belong to this device.
	// Reporting on another device's job is a 404 (we don't reveal that a
	// job with that id exists for someone else).
	job, err := store.Get(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, deploy.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such job")
			return
		}
		d.logErr("agent jobs: get", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if job.DeviceID != ac.DeviceID {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}

	if err := store.Complete(r.Context(), jobID, status, req.ResultJSON, d.Now()); err != nil {
		switch {
		case errors.Is(err, deploy.ErrNotClaimable):
			writeError(w, http.StatusConflict, "not_claimable", "job is not in a reportable state")
		case errors.Is(err, deploy.ErrJobNotFound):
			writeError(w, http.StatusNotFound, "not_found", "no such job")
		default:
			d.logErr("agent jobs: complete", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		}
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "device", ID: ac.DeviceID},
			Action: "deployment.result",
			Target: audit.Target{Type: "deployment_job", ID: jobID},
			Detail: map[string]any{"status": req.Status},
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
