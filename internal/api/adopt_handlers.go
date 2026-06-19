package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/deploy"
)

// adoptRequest is the WebUI body for adopting a device's discovered skill
// into the registry. The skill is addressed by the path's device id + the
// skill name; the source root (tool/scope) disambiguates when the same skill
// name exists under more than one of the device's roots.
type adoptRequest struct {
	ToolKey string `json:"tool_key,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

// handleAdoptDeviceSkill enqueues a capture_skill job: it asks the agent to
// read a discovered skill's real files from the device and upload them so the
// server adopts them into the registry as a new version (device -> registry).
//
// The server resolves the skill's on-disk path from the device's latest
// inventory (the agent reported it as skill_path), so the operator never
// types a path and the agent only ever reads a directory it itself scanned.
func (d Deps) handleAdoptDeviceSkill(w http.ResponseWriter, r *http.Request) {
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
	skillName := strings.TrimSpace(r.PathValue("name"))
	if skillName == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill name")
		return
	}

	body, ok := decodeJSON[adoptRequest](w, r, 16<<10, skipContentTypeCheck(), withDecodeErrorDetail())
	if !ok {
		return
	}
	body.ToolKey = strings.TrimSpace(body.ToolKey)
	body.Scope = strings.TrimSpace(body.Scope)

	// Resolve the discovered skill's path (+ root) from the latest run. When
	// tool_key/scope are given they disambiguate a shared name; otherwise the
	// first match wins (a name in exactly one root is the common case).
	src, found, err := d.findDiscoveredSkill(r.Context(), deviceID, skillName, body.ToolKey, body.Scope)
	if err != nil {
		d.logErr("adopt: discovered skill lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if !found {
		writeError(w, http.StatusUnprocessableEntity, "skill_not_discovered",
			"skill was not reported in this device's latest inventory")
		return
	}

	var requestedBy string
	if sess, ok := SessionFromContext(r.Context()); ok {
		requestedBy = sess.UserID
	}
	req := deploy.Request{
		Operation:   deploy.OpCaptureSkill,
		SkillName:   skillName,
		Target:      deploy.Target{ToolKey: src.toolKey, Scope: src.scope, RootID: src.rootID},
		CapturePath: src.skillPath,
		RequestedBy: requestedBy,
	}
	reqJSON, _ := json.Marshal(req)
	job, err := d.Deploy.Create(r.Context(), deploy.CreateParams{
		DeviceID:    deviceID,
		Operation:   deploy.OpCaptureSkill,
		RequestJSON: string(reqJSON),
	}, d.Now())
	if err != nil {
		writeRootJobCreateError(w, d, err)
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.adopt_requested",
			Target: audit.Target{Type: "deployment_job", ID: job.ID},
			Detail: map[string]any{
				"device_id":  deviceID,
				"skill_name": skillName,
				"tool_key":   src.toolKey,
				"scope":      src.scope,
				"root_id":    src.rootID,
			},
		})
	}

	writeJSON(w, http.StatusCreated, deploymentJobView{}.from(job))
}

// discoveredSkillSource is the resolved location of a skill on a device.
type discoveredSkillSource struct {
	skillPath string
	toolKey   string
	scope     string
	rootID    string
}

// findDiscoveredSkill looks up a skill's path + root from the device's latest
// inventory run. toolKey/scope, when non-empty, narrow a name that exists in
// more than one root; otherwise the first row (by tool/scope order) is used.
func (d Deps) findDiscoveredSkill(ctx context.Context, deviceID, name, toolKey, scope string) (discoveredSkillSource, bool, error) {
	rows, err := d.DB.QueryContext(ctx, `
		SELECT ds.skill_path, ds.tool_key, ds.scope, ti.root_id
		  FROM discovered_skills ds
		  JOIN tool_instances ti ON ds.tool_instance_id = ti.id
		 WHERE ds.device_id = ?
		   AND ds.name = ?
		   AND ds.run_id IN (
		       SELECT ir.id FROM inventory_runs ir
		        WHERE ir.device_id = ?
		        ORDER BY ir.created_at DESC LIMIT 1
		   )
		 ORDER BY ds.tool_key, ds.scope
	`, deviceID, name, deviceID)
	if err != nil {
		return discoveredSkillSource{}, false, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var s discoveredSkillSource
		if err := rows.Scan(&s.skillPath, &s.toolKey, &s.scope, &s.rootID); err != nil {
			return discoveredSkillSource{}, false, err
		}
		if toolKey != "" && s.toolKey != toolKey {
			continue
		}
		if scope != "" && s.scope != scope {
			continue
		}
		return s, true, rows.Err()
	}
	return discoveredSkillSource{}, false, rows.Err()
}
