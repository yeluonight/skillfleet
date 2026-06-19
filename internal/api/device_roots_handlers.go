package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

// deviceRootRequest is the WebUI body for requesting a root registration.
// Candidate paths are accepted only after matching the device's latest
// inventory. Custom paths explicitly bypass that server-side gate so the
// agent can apply its local home-subtree/candidate policy as the final
// authority before mutating allowed_roots.
type deviceRootRequest struct {
	ToolKey string `json:"tool_key"`
	Scope   string `json:"scope"`
	Path    string `json:"path"`
	Custom  bool   `json:"custom,omitempty"`
}

// handleRegisterDeviceRoot enqueues a register_root job for one device. The
// default path is guarded by a defence-in-depth candidate check: only paths the
// agent itself reported in its latest inventory may be sent back down. Custom
// paths are queued with an explicit flag and rely on the agent's stricter local
// policy check before any config write.
func (d Deps) handleRegisterDeviceRoot(w http.ResponseWriter, r *http.Request) {
	if !d.requireDeploy(w) {
		return
	}
	deviceID, ok := d.deviceIDFromPath(w, r)
	if !ok {
		return
	}
	if !d.requireDeviceExists(w, r, deviceID) {
		return
	}

	body, ok := decodeJSON[deviceRootRequest](w, r, 64<<10, skipContentTypeCheck(), withDecodeErrorDetail())
	if !ok {
		return
	}
	body.ToolKey = strings.TrimSpace(body.ToolKey)
	body.Scope = strings.TrimSpace(body.Scope)
	body.Path = strings.TrimSpace(body.Path)
	if body.ToolKey == "" || body.Scope == "" || body.Path == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "tool_key, scope and path are required")
		return
	}

	var (
		candidate inventory.RootCandidate
		err       error
	)
	if !body.Custom {
		var found bool
		candidate, found, err = d.findLatestRootCandidate(r.Context(), deviceID, body.ToolKey, body.Scope, body.Path)
		if err != nil {
			d.logErr("device roots: candidate lookup", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		if !found {
			writeError(w, http.StatusUnprocessableEntity, "root_not_a_candidate", "root path was not reported as a candidate by this device")
			return
		}
	}

	var requestedBy string
	if sess, ok := SessionFromContext(r.Context()); ok {
		requestedBy = sess.UserID
	}
	req := deploy.Request{
		Operation:   deploy.OpRegisterRoot,
		Target:      deploy.Target{ToolKey: body.ToolKey, Scope: body.Scope},
		RootPath:    body.Path,
		RequestedBy: requestedBy,
	}
	reqJSON, _ := json.Marshal(req)
	job, err := d.Deploy.Create(r.Context(), deploy.CreateParams{
		DeviceID:    deviceID,
		Operation:   deploy.OpRegisterRoot,
		RequestJSON: string(reqJSON),
	}, d.Now())
	if err != nil {
		writeRootJobCreateError(w, d, err)
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "device.root_register_requested",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{
				"device_id":     deviceID,
				"tool_key":      body.ToolKey,
				"scope":         body.Scope,
				"path":          body.Path,
				"exists":        candidate.Exists,
				"shared":        candidate.Shared,
				"registered":    candidate.Registered,
				"tool_detected": candidate.ToolDetected,
			},
		})
	}

	writeJSON(w, http.StatusCreated, deploymentJobView{}.from(job))
}

// handleRemoveDeviceRoot enqueues a remove_root job. Root IDs are matched
// against the latest inventory's registered roots so a WebUI click cannot
// request removal of an arbitrary, unreported id.
func (d Deps) handleRemoveDeviceRoot(w http.ResponseWriter, r *http.Request) {
	if !d.requireDeploy(w) {
		return
	}
	deviceID, ok := d.deviceIDFromPath(w, r)
	if !ok {
		return
	}
	if !d.requireDeviceExists(w, r, deviceID) {
		return
	}
	rootID := strings.TrimSpace(r.PathValue("rootId"))
	if rootID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing root id")
		return
	}

	candidate, found, err := d.findLatestRegisteredRoot(r.Context(), deviceID, rootID)
	if err != nil {
		d.logErr("device roots: registered root lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if !found {
		writeError(w, http.StatusUnprocessableEntity, "root_not_registered", "root id was not reported as a registered root by this device")
		return
	}

	var requestedBy string
	if sess, ok := SessionFromContext(r.Context()); ok {
		requestedBy = sess.UserID
	}
	req := deploy.Request{
		Operation:   deploy.OpRemoveRoot,
		Target:      deploy.Target{RootID: rootID},
		RequestedBy: requestedBy,
	}
	reqJSON, _ := json.Marshal(req)
	job, err := d.Deploy.Create(r.Context(), deploy.CreateParams{
		DeviceID:    deviceID,
		Operation:   deploy.OpRemoveRoot,
		RequestJSON: string(reqJSON),
	}, d.Now())
	if err != nil {
		writeRootJobCreateError(w, d, err)
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "device.root_remove_requested",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{
				"device_id": deviceID,
				"root_id":   rootID,
				"tool_key":  candidate.ToolKey,
				"scope":     candidate.Scope,
				"path":      candidate.Path,
			},
		})
	}

	writeJSON(w, http.StatusCreated, deploymentJobView{}.from(job))
}

func (d Deps) deviceIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing device id")
		return "", false
	}
	return id, true
}

func (d Deps) requireDeviceExists(w http.ResponseWriter, r *http.Request, deviceID string) bool {
	if _, err := devices.Get(r.Context(), d.DB, deviceID); err != nil {
		if errors.Is(err, devices.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "device not found")
			return false
		}
		d.logErr("device roots: device lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return false
	}
	return true
}

func (d Deps) findLatestRootCandidate(ctx context.Context, deviceID, toolKey, scope, path string) (inventory.RootCandidate, bool, error) {
	roots, err := d.latestRootCandidates(ctx, deviceID)
	if err != nil {
		return inventory.RootCandidate{}, false, err
	}
	for _, c := range roots {
		if c.ToolKey == toolKey && c.Scope == scope && c.Path == path {
			return c, true, nil
		}
	}
	return inventory.RootCandidate{}, false, nil
}

func (d Deps) findLatestRegisteredRoot(ctx context.Context, deviceID, rootID string) (inventory.RootCandidate, bool, error) {
	roots, err := d.latestRootCandidates(ctx, deviceID)
	if err != nil {
		return inventory.RootCandidate{}, false, err
	}
	for _, c := range roots {
		if c.Registered && c.RootID == rootID {
			return c, true, nil
		}
	}
	return inventory.RootCandidate{}, false, nil
}

func (d Deps) latestRootCandidates(ctx context.Context, deviceID string) ([]inventory.RootCandidate, error) {
	var rootsJSON sql.NullString
	err := d.DB.QueryRowContext(ctx, `
		SELECT roots_json FROM inventory_runs WHERE device_id = ?
		 ORDER BY created_at DESC LIMIT 1
	`, deviceID).Scan(&rootsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeInventoryRoots(rootsJSON)
}

func writeRootJobCreateError(w http.ResponseWriter, d Deps, err error) {
	if errors.Is(err, deploy.ErrEmptyDeviceID) || errors.Is(err, deploy.ErrBadOperation) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	d.logErr("device roots: create job", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
}
