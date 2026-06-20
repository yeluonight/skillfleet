package api

import (
	"database/sql"
	"net/http"
	"sort"
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
	// ModifiedAt is the device file's newest mtime (unix millis, 0 unknown):
	// when the skill was last edited on the device. MatchedVersionCreatedAt
	// is the publish time of the registry version the device's content
	// matches (0 when local_state isn't clean) — together they let the UI
	// show "device edited X / running registry version published Y".
	ModifiedAt              int64 `json:"modified_at,omitempty"`
	MatchedVersionCreatedAt int64 `json:"matched_version_created_at,omitempty"`
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
	// Load the skill's versions once and derive both maps from it: the
	// content-SHA -> version-id index drift.Classify needs, and the
	// version-id -> published-at lookup for clean deployments. (Calling
	// ListVersionSHAs would re-run ListByName a second time.)
	versions, err := d.Registry.ListByName(ctx, name)
	if err != nil {
		d.logErr("fleet-status: list registry versions", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	shas := make(map[string]string, len(versions))
	versionPublishedAt := make(map[string]int64, len(versions))
	for _, v := range versions {
		shas[v.ContentSHA256] = v.ID
		versionPublishedAt[v.ID] = v.CreatedAt.UnixMilli()
	}
	regCount := len(versions)
	hasName := regCount > 0

	rows, err := d.DB.QueryContext(ctx, `
		SELECT d.id, d.name, d.status,
		       ds.tool_key, ds.scope, ds.effective_state, ds.content_sha256,
		       ti.root_id, ti.root_path, ds.modified_at,
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
			toolKey, scope, effectiveState     string
			rootID, rootPath                   string
			contentSHA                         sql.NullString
			modifiedAt                         sql.NullInt64
			hasActiveJob                       bool
		)
		if err := rows.Scan(
			&deviceID, &deviceName, &deviceStatus,
			&toolKey, &scope, &effectiveState, &contentSHA,
			&rootID, &rootPath, &modifiedAt,
			&hasActiveJob,
		); err != nil {
			d.logErr("fleet-status: scan row", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}

		state, matched := drift.Classify(contentSHA.String, shas, hasName)
		out = append(out, fleetStatusRow{
			DeviceID:                deviceID,
			DeviceName:              deviceName,
			DeviceStatus:            deviceStatus,
			ToolKey:                 toolKey,
			Scope:                   scope,
			RootID:                  rootID,
			RootPath:                rootPath,
			EffectiveState:          effectiveState,
			LocalState:              string(state),
			LocalSHA256:             contentSHA.String,
			MatchedVersionID:        matched,
			RegistryVersionCount:    regCount,
			HasActiveJob:            hasActiveJob,
			ModifiedAt:              modifiedAt.Int64,
			MatchedVersionCreatedAt: versionPublishedAt[matched],
		})
	}
	if err := rows.Err(); err != nil {
		d.logErr("fleet-status: rows iter", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Second pass: synthesize "not_deployed" rows for every REGISTERED root
	// that has no discovered_skills row for this skill. The first pass only
	// returns paths that already hold the skill; without this pass the detail
	// page could not show (or offer a one-click deploy to) a registered root
	// the skill was never installed to. The synthesized rows are local to this
	// response — nothing is persisted (drift has always been computed on read).

	// covered keys the (device, root-path) cells the first pass already
	// produced from real discovered_skills rows, so a registered root that
	// already holds the skill is not duplicated. Path (not root id) is the key:
	// tool_instances.root_id is the adapter's IDBase while a registered root's
	// RootID is the agent's allowed_root id (tool_scope), and the two do not
	// always agree — but both sides carry the same absolute root path.
	covered := make(map[string]bool, len(out))
	for _, r := range out {
		covered[r.DeviceID+"|"+r.RootPath] = true
	}

	// Devices with ANY active job targeting this skill. The synthesized rows
	// use this for has_active_job, mirroring the first pass's per-device-
	// per-skill EXISTS (a device with an active job for this skill lights up
	// all of that skill's rows on the device, real and synthesized alike).
	// Per-device granularity: a job for one root may light up another root's
	// cell on the same device — accepted (plan R9).
	activeJobDevices := make(map[string]bool)
	jobRows, err := d.DB.QueryContext(ctx, `
		SELECT DISTINCT d.id FROM devices d
		 JOIN deployment_jobs j ON j.device_id = d.id
		WHERE j.status IN ('pending', 'claimed', 'running')
		  AND j.request_json LIKE '%"skill_name":"' || ? || '"%'
	`, name)
	if err != nil {
		d.logErr("fleet-status: query active job devices", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	for jobRows.Next() {
		var id string
		if err := jobRows.Scan(&id); err != nil {
			_ = jobRows.Close()
			d.logErr("fleet-status: scan active job device", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		activeJobDevices[id] = true
	}
	if err := jobRows.Err(); err != nil {
		_ = jobRows.Close()
		d.logErr("fleet-status: active job devices iter", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	_ = jobRows.Close()

	// Walk every device that has an inventory run; for each REGISTERED root
	// lacking a discovered row for this skill, synthesize a not_deployed row.
	devRows, err := d.DB.QueryContext(ctx, `
		SELECT d.id, d.name, d.status FROM devices d
		 WHERE EXISTS (SELECT 1 FROM inventory_runs ir WHERE ir.device_id = d.id)
	`)
	if err != nil {
		d.logErr("fleet-status: query devices for synthesis", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	for devRows.Next() {
		var devID, devName, devStatus string
		if err := devRows.Scan(&devID, &devName, &devStatus); err != nil {
			_ = devRows.Close()
			d.logErr("fleet-status: scan device for synthesis", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		roots, err := d.latestRootCandidates(ctx, devID)
		if err != nil {
			_ = devRows.Close()
			d.logErr("fleet-status: load root candidates", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		for _, cand := range roots {
			if !cand.Registered {
				continue
			}
			if covered[devID+"|"+cand.Path] {
				continue
			}
			out = append(out, fleetStatusRow{
				DeviceID:             devID,
				DeviceName:           devName,
				DeviceStatus:         devStatus,
				ToolKey:              cand.ToolKey,
				Scope:                cand.Scope,
				RootID:               cand.RootID,
				RootPath:             cand.Path,
				EffectiveState:       "unknown",
				LocalState:           string(drift.StateNotDeployed),
				RegistryVersionCount: regCount,
				HasActiveJob:         activeJobDevices[devID],
			})
			// Mark this cell covered so two registered roots cannot both
			// synthesize against the same path (defence against dup joins).
			covered[devID+"|"+cand.Path] = true
		}
	}
	if err := devRows.Err(); err != nil {
		_ = devRows.Close()
		d.logErr("fleet-status: devices iter", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	_ = devRows.Close()

	// Stable-sort the combined output (real + synthesized) by
	// (device_name, tool_key, scope, root_path) so the first pass's inner
	// ORDER BY does not leak through once synthesized rows are appended, and
	// ties on (device, tool, scope) — e.g. a real row and a synthesized row
	// for different roots — have a defined order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DeviceName != out[j].DeviceName {
			return out[i].DeviceName < out[j].DeviceName
		}
		if out[i].ToolKey != out[j].ToolKey {
			return out[i].ToolKey < out[j].ToolKey
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].RootPath < out[j].RootPath
	})

	writeJSON(w, http.StatusOK, fleetStatusResponse{
		SkillName:   name,
		Deployments: out,
	})
}
