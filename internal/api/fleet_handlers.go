package api

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/drift"
)

// fleetStatusRow is one device×tool×scope deployment of a skill.
type fleetStatusRow struct {
	DeviceID             string `json:"device_id"`
	DeviceName           string `json:"device_name"`
	DeviceStatus         string `json:"device_status"`
	ToolKey              string `json:"tool_key"`
	Scope                string `json:"scope"`
	RootID               string `json:"root_id"`
	RootPath             string `json:"root_path"`
	EffectiveState       string `json:"effective_state"`
	LocalState           string `json:"local_state"`
	LocalSHA256          string `json:"local_sha256,omitempty"`
	MatchedVersionID     string `json:"matched_version_id,omitempty"`
	RegistryVersionCount int    `json:"registry_version_count"`
	HasActiveJob         bool   `json:"has_active_job"`
}

type fleetStatusResponse struct {
	SkillName   string           `json:"skill_name"`
	Deployments []fleetStatusRow `json:"deployments"`
}

// handleSkillFleetStatus returns one skill's current deployment footprint across
// all devices' latest inventory runs. local_state and matched_version_id are
// computed from discovered content SHA values against the registry, not stored.
func (d Deps) handleSkillFleetStatus(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill name")
		return
	}

	ctx := r.Context()
	shas, regCount, err := (registryVersionLister{reg: d.Registry}).ListVersionSHAs(ctx, name)
	if err != nil {
		d.logErr("fleet-status: list registry versions", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	hasName := regCount > 0

	rows, err := d.DB.QueryContext(ctx, `
		SELECT d.id, d.name, d.status,
		       ds.tool_key, ds.scope, ds.effective_state, ds.content_sha256,
		       ti.root_id, ti.root_path,
		       EXISTS(SELECT 1 FROM deployment_jobs j
		               WHERE j.device_id = d.id
		                 AND j.status IN ('pending', 'claimed', 'running')
		                 AND j.request_json LIKE '%"skill_name":"' || ds.name || '"%') AS has_active_job
		  FROM discovered_skills ds
		  JOIN tool_instances ti ON ds.tool_instance_id = ti.id
		  JOIN devices d         ON ds.device_id = d.id
		 WHERE ds.name = ?
		   AND ds.run_id IN (
		       SELECT ir.id FROM inventory_runs ir
		        WHERE ir.device_id = d.id
		        ORDER BY ir.created_at DESC LIMIT 1
		   )
		 ORDER BY d.name, ds.tool_key, ds.scope
	`, name)
	if err != nil {
		d.logErr("fleet-status: query discovered skills", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	defer func() { _ = rows.Close() }()

	out := make([]fleetStatusRow, 0)
	for rows.Next() {
		var (
			deviceID, deviceName, deviceStatus string
			toolKey, scope, effectiveState      string
			rootID, rootPath                    string
			contentSHA                          sql.NullString
			hasActiveJob                        bool
		)
		if err := rows.Scan(
			&deviceID, &deviceName, &deviceStatus,
			&toolKey, &scope, &effectiveState, &contentSHA,
			&rootID, &rootPath,
			&hasActiveJob,
		); err != nil {
			d.logErr("fleet-status: scan row", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}

		state, matched := drift.Classify(contentSHA.String, shas, hasName)
		out = append(out, fleetStatusRow{
			DeviceID:             deviceID,
			DeviceName:           deviceName,
			DeviceStatus:         deviceStatus,
			ToolKey:              toolKey,
			Scope:                scope,
			RootID:               rootID,
			RootPath:             rootPath,
			EffectiveState:       effectiveState,
			LocalState:           string(state),
			LocalSHA256:          contentSHA.String,
			MatchedVersionID:     matched,
			RegistryVersionCount: regCount,
			HasActiveJob:         hasActiveJob,
		})
	}
	if err := rows.Err(); err != nil {
		d.logErr("fleet-status: rows iter", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	writeJSON(w, http.StatusOK, fleetStatusResponse{
		SkillName:   name,
		Deployments: out,
	})
}
