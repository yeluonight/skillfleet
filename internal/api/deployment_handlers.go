// deployment_handlers.go — the operator-facing deployment API (v1.0
// §14.1): plan a deploy (dry-run), execute one (create a pending job the
// agent will claim), roll one back, and list jobs. These handlers only
// ever create/read deployment_jobs rows and run the server-side planner;
// the actual filesystem work happens on the agent (internal/agentinstall).
//
// Authorisation: all four sit behind requireAuth; the three writes also
// behind requireCSRF (wired in api.go). The plan/execute path never
// touches a device — it resolves a registry version into a Plan and
// stores intent; the agent does the install when it next polls.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/registry"
)

// registryReader adapts *registry.Store to deploy.RegistryReader, so the
// planner can read a version without deploy importing registry (mirrors
// the registryVersionLister adapter used by the drift handlers).
type registryReader struct {
	reg *registry.Store
}

func (r registryReader) GetVersion(ctx context.Context, versionID string) (deploy.VersionRef, error) {
	v, err := r.reg.Get(ctx, versionID)
	if err != nil {
		if errors.Is(err, registry.ErrVersionNotFnd) {
			return deploy.VersionRef{}, deploy.ErrPlanNoVersion
		}
		return deploy.VersionRef{}, err
	}
	return deploy.VersionRef{
		ID:            v.ID,
		Name:          v.Name,
		BaseVersionID: v.BaseVersionID,
		ContentSHA256: v.ContentSHA256,
		Manifest:      v.Manifest,
		PackagePath:   v.PackagePath,
	}, nil
}

func (r registryReader) ArchiveAbsPath(v deploy.VersionRef) string {
	// ArchivePath only reads PackagePath; a minimal Version suffices.
	return r.reg.ArchivePath(registry.Version{PackagePath: v.PackagePath})
}

// deployRequestBody is the JSON an operator POSTs to plan/execute. It
// names the skill + version and the target {tool_key, scope, root_id}.
type deployRequestBody struct {
	SkillName string `json:"skill_name"`
	VersionID string `json:"version_id"`
	ToolKey   string `json:"tool_key"`
	Scope     string `json:"scope"`
	RootID    string `json:"root_id"`
	DeviceID  string `json:"device_id"`
}

// planResponse echoes the resolved plan (dry-run) so the operator can
// preview what would be written before committing a job.
type planResponse struct {
	Plan deploy.Plan `json:"plan"`
}

// handleDeployPlan resolves an install request into a Plan WITHOUT
// creating a job — a dry-run preview of what would be installed. 503 if
// the registry isn't wired; 400/404 for a bad/unknown version.
func (d Deps) handleDeployPlan(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	body, ok := decodeDeployBody(w, r)
	if !ok {
		return
	}
	plan, err := d.planInstall(r.Context(), body)
	if err != nil {
		writePlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, planResponse{Plan: plan})
}

// handleDeployExecute plans the install and creates a pending job for the
// target device. The agent claims it on its next poll. Requires a device
// id (the job is addressed to a device); returns the created job view.
func (d Deps) handleDeployExecute(w http.ResponseWriter, r *http.Request) {
	if !d.requireDeployStack(w) {
		return
	}
	body, ok := decodeDeployBody(w, r)
	if !ok {
		return
	}
	if body.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "device_id is required to execute a deployment")
		return
	}

	plan, err := d.planInstall(r.Context(), body)
	if err != nil {
		writePlanError(w, err)
		return
	}

	// Record the operator's intent (request_json) alongside the resolved
	// plan (plan_json). requested_by ties the job to the session user.
	var requestedBy string
	if sess, ok := SessionFromContext(r.Context()); ok {
		requestedBy = sess.UserID
	}
	req := deploy.Request{
		Operation:   deploy.OpInstall,
		SkillName:   body.SkillName,
		VersionID:   plan.VersionID,
		Target:      deploy.Target{ToolKey: body.ToolKey, Scope: body.Scope, RootID: body.RootID},
		RequestedBy: requestedBy,
	}
	reqJSON, _ := json.Marshal(req)
	planJSON, _ := json.Marshal(plan)

	job, err := d.Deploy.Create(r.Context(), deploy.CreateParams{
		DeviceID:    body.DeviceID,
		Operation:   deploy.OpInstall,
		RequestJSON: string(reqJSON),
		PlanJSON:    string(planJSON),
	}, d.Now())
	if err != nil {
		if errors.Is(err, deploy.ErrEmptyDeviceID) || errors.Is(err, deploy.ErrBadOperation) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		d.logErr("deploy execute: create job", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "deployment.created",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{
				"device_id":  body.DeviceID,
				"skill_name": body.SkillName,
				"version_id": plan.VersionID,
				"tool_key":   body.ToolKey,
				"scope":      body.Scope,
			},
		})
	}

	writeJSON(w, http.StatusCreated, deploymentJobView{}.from(job))
}

// handleDeployRollback creates a rollback job that undoes a prior
// install job, restoring it from the backup the install recorded. The
// {id} is the original job; we read its result to find the backup path +
// resolved target, then enqueue a rollback addressed to the same device.
func (d Deps) handleDeployRollback(w http.ResponseWriter, r *http.Request) {
	if !d.requireDeploy(w) {
		return
	}
	origID := strings.TrimSpace(r.PathValue("id"))
	if origID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing job id")
		return
	}

	orig, err := d.Deploy.Get(r.Context(), origID)
	if err != nil {
		if errors.Is(err, deploy.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such job")
			return
		}
		d.logErr("deploy rollback: get orig", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	// Only a succeeded install can be rolled back — there must be a
	// backup, and rolling back a failed/expired job is meaningless.
	if orig.Operation != deploy.OpInstall || orig.Status != deploy.StatusSucceeded {
		writeError(w, http.StatusConflict, "not_rollbackable", "only a succeeded install can be rolled back")
		return
	}

	var origReq deploy.Request
	_ = json.Unmarshal([]byte(orig.RequestJSON), &origReq)
	var origRes deploy.Result
	if err := json.Unmarshal([]byte(orig.ResultJSON), &origRes); err != nil || origRes.BackupPath == "" {
		writeError(w, http.StatusConflict, "no_backup", "original job has no backup to restore")
		return
	}

	// The rollback plan tells the agent which root + skill + backup dir to
	// restore. deploy.RollbackPlan is the shared shape the agent decodes
	// (agentinstall.Executor.Rollback); BackupWasEmpty is left false here
	// (omitempty) so the wire bytes match the prior hand-built map.
	rollbackPlan := deploy.RollbackPlan{
		Target:    origReq.Target,
		SkillName: origReq.SkillName,
		BackupDir: origRes.BackupPath,
	}
	planJSON, _ := json.Marshal(rollbackPlan)
	var requestedBy string
	if sess, ok := SessionFromContext(r.Context()); ok {
		requestedBy = sess.UserID
	}
	req := deploy.Request{
		Operation:      deploy.OpRollback,
		SkillName:      origReq.SkillName,
		Target:         origReq.Target,
		RollsBackJobID: origID,
		RequestedBy:    requestedBy,
	}
	reqJSON, _ := json.Marshal(req)

	job, err := d.Deploy.Create(r.Context(), deploy.CreateParams{
		DeviceID:    orig.DeviceID,
		Operation:   deploy.OpRollback,
		RequestJSON: string(reqJSON),
		PlanJSON:    string(planJSON),
	}, d.Now())
	if err != nil {
		d.logErr("deploy rollback: create job", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "deployment.rollback_requested",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{"rolls_back_job_id": origID, "device_id": orig.DeviceID},
		})
	}

	writeJSON(w, http.StatusCreated, deploymentJobView{}.from(job))
}

// stateChangeRequestBody is the JSON an operator POSTs to change a
// skill's enable state on one device. It names the skill, the target
// {tool_key, scope, root_id}, the device, and the desired state.
type stateChangeRequestBody struct {
	SkillName    string `json:"skill_name"`
	ToolKey      string `json:"tool_key"`
	Scope        string `json:"scope"`
	RootID       string `json:"root_id"`
	DeviceID     string `json:"device_id"`
	DesiredState string `json:"desired_state"`
}

// handleDeployStateChange plans + enqueues a state-change job: it flips a
// skill's native enable/disable state on one device by editing the tool's
// out-of-band config (the agent does the write). Unlike install it needs
// no registry/version — the plan is just {target, skill, desired_state} —
// so it sits behind requireDeploy (not the full deploy stack). A desired
// state the tool can't represent (codex+ask, antigravity+anything) is
// rejected 422 before any job is created.
func (d Deps) handleDeployStateChange(w http.ResponseWriter, r *http.Request) {
	if !d.requireDeploy(w) {
		return
	}
	body, ok := decodeJSON[stateChangeRequestBody](w, r, 64<<10, skipContentTypeCheck(), withDecodeErrorDetail())
	if !ok {
		return
	}
	if body.SkillName == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "skill_name is required")
		return
	}
	if body.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "device_id is required to execute a state change")
		return
	}

	var requestedBy string
	if sess, ok := SessionFromContext(r.Context()); ok {
		requestedBy = sess.UserID
	}
	req := deploy.Request{
		Operation:    deploy.OpStateChange,
		SkillName:    body.SkillName,
		Target:       deploy.Target{ToolKey: body.ToolKey, Scope: body.Scope, RootID: body.RootID},
		DesiredState: adapters.EffectiveState(body.DesiredState),
		RequestedBy:  requestedBy,
	}

	// Plan (validate + build the StateChangePlan). A stateless planner —
	// nil registry is fine, PlanStateChange never reads it.
	plan, err := deploy.NewPlanner(nil).PlanStateChange(req)
	if err != nil {
		writeStateChangeError(w, err)
		return
	}

	reqJSON, _ := json.Marshal(req)
	planJSON, _ := json.Marshal(plan)

	job, err := d.Deploy.Create(r.Context(), deploy.CreateParams{
		DeviceID:    body.DeviceID,
		Operation:   deploy.OpStateChange,
		RequestJSON: string(reqJSON),
		PlanJSON:    string(planJSON),
	}, d.Now())
	if err != nil {
		if errors.Is(err, deploy.ErrEmptyDeviceID) || errors.Is(err, deploy.ErrBadOperation) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		d.logErr("deploy state-change: create job", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "deployment.state_change",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{
				"device_id":     body.DeviceID,
				"skill_name":    body.SkillName,
				"tool_key":      body.ToolKey,
				"scope":         body.Scope,
				"desired_state": body.DesiredState,
			},
		})
	}

	writeJSON(w, http.StatusCreated, deploymentJobView{}.from(job))
}

// writeStateChangeError maps PlanStateChange errors to status codes. An
// unsupported tool/state is a client error the operator should see as
// 422 (the request was well-formed but semantically impossible for the
// target tool).
func writeStateChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploy.ErrUnsupportedState), errors.Is(err, deploy.ErrUnknownTool):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_state", err.Error())
	case errors.Is(err, deploy.ErrPlanNoSkill), errors.Is(err, deploy.ErrPlanNoTool):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// handleListDeployments lists jobs, optionally filtered by ?device= and
// ?skill=. Read-only (requireAuth only).
func (d Deps) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	if d.Deploy == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []deploymentJobView{}})
		return
	}
	jobs, err := d.Deploy.List(r.Context(), deploy.ListFilter{
		DeviceID:  r.URL.Query().Get("device"),
		SkillName: r.URL.Query().Get("skill"),
		Status:    deploy.Status(r.URL.Query().Get("status")),
	})
	if err != nil {
		d.logErr("deploy list", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	out := make([]deploymentJobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, deploymentJobView{}.from(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// planInstall is the shared resolve step for plan + execute: it builds a
// fresh stateless Planner over the registry adapter and resolves the
// request body into a Plan.
func (d Deps) planInstall(ctx context.Context, body deployRequestBody) (deploy.Plan, error) {
	planner := deploy.NewPlanner(registryReader{reg: d.Registry})
	return planner.PlanInstall(ctx, deploy.Request{
		Operation: deploy.OpInstall,
		SkillName: body.SkillName,
		VersionID: body.VersionID,
		Target:    deploy.Target{ToolKey: body.ToolKey, Scope: body.Scope, RootID: body.RootID},
	}, markerSourceFor(body), d.Now())
}

// markerSourceFor is a placeholder for deriving the install marker's
// source provenance; Phase 8 leaves it nil (the marker still records
// skill/version/content), as source-block provenance is a later refinement.
func markerSourceFor(deployRequestBody) *deploy.MarkerSource { return nil }

// decodeDeployBody reads + validates the common request body. Returns
// (body, true) on success; on failure it has already written the error.
func decodeDeployBody(w http.ResponseWriter, r *http.Request) (deployRequestBody, bool) {
	body, ok := decodeJSON[deployRequestBody](w, r, 64<<10, skipContentTypeCheck(), withDecodeErrorDetail())
	if !ok {
		return deployRequestBody{}, false
	}
	if body.VersionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "version_id is required")
		return deployRequestBody{}, false
	}
	return body, true
}

// writePlanError maps planner errors to status codes.
func writePlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploy.ErrPlanNoVersion):
		writeError(w, http.StatusNotFound, "version_not_found", "no such version")
	case errors.Is(err, deploy.ErrPlanNameMismatch):
		writeError(w, http.StatusBadRequest, "name_mismatch", err.Error())
	case errors.Is(err, deploy.ErrPlanNoArchive):
		writeError(w, http.StatusConflict, "no_archive", "version has no package archive")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// deploymentJobView is the list/detail projection of a deployment job.
// SkillName/VersionID are parsed from request_json; the result summary
// fields are parsed from result_json (present only on terminal jobs).
type deploymentJobView struct {
	ID           string `json:"id"`
	DeviceID     string `json:"device_id"`
	Operation    string `json:"operation"`
	Status       string `json:"status"`
	SkillName    string `json:"skill_name,omitempty"`
	VersionID    string `json:"version_id,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	RolledBack   bool   `json:"rolled_back,omitempty"`
	ResolvedRoot string `json:"resolved_root,omitempty"`
}

func (deploymentJobView) from(j deploy.Job) deploymentJobView {
	v := deploymentJobView{
		ID:        j.ID,
		DeviceID:  j.DeviceID,
		Operation: string(j.Operation),
		Status:    string(j.Status),
		CreatedAt: j.CreatedAt.UnixMilli(),
		UpdatedAt: j.UpdatedAt.UnixMilli(),
	}
	if j.RequestJSON != "" {
		var req deploy.Request
		if json.Unmarshal([]byte(j.RequestJSON), &req) == nil {
			v.SkillName = req.SkillName
			v.VersionID = req.VersionID
		}
	}
	if j.ResultJSON != "" {
		var res deploy.Result
		if json.Unmarshal([]byte(j.ResultJSON), &res) == nil {
			v.ErrorCode = res.ErrorCode
			v.ErrorMessage = res.ErrorMessage
			v.RolledBack = res.RolledBack
			v.ResolvedRoot = res.ResolvedRootPath
		}
	}
	return v
}
