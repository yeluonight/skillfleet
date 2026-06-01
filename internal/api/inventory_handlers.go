package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/devices"
)

// inventorySkillView is one row of the device's skill matrix.
type inventorySkillView struct {
	ToolKey        string             `json:"tool_key"`
	Scope          string             `json:"scope"`
	Name           string             `json:"name"`
	SkillPath      string             `json:"skill_path"`
	HasSkillMD     bool               `json:"has_skill_md"`
	Description    string             `json:"description,omitempty"`
	EffectiveState string             `json:"effective_state"`
	NativeState    string             `json:"native_state,omitempty"`
	ContentSHA256  string             `json:"content_sha256,omitempty"`
	FileCount      int                `json:"file_count"`
	TotalBytes     int64              `json:"total_bytes"`
	Warnings       []inventoryWarning `json:"warnings,omitempty"`
}

type inventoryWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// inventoryRunView summarises the latest run plus the full skill list.
type inventoryRunView struct {
	RunID        string               `json:"run_id"`
	StartedAt    int64                `json:"started_at"`
	SkillCount   int                  `json:"skill_count"`
	RootCount    int                  `json:"root_count"`
	AgentVersion string               `json:"agent_version,omitempty"`
	Skills       []inventorySkillView `json:"skills"`
}

// handleDeviceInventory returns the latest inventory run for a device,
// flattened into a skill list the WebUI renders as a tool x scope x
// skill matrix. A device with no inventory yet returns 200 with a null
// run so the UI can show "never scanned" rather than 404.
func (d Deps) handleDeviceInventory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing device id")
		return
	}

	// 404 if the device itself doesn't exist (distinct from "exists but
	// never scanned").
	if _, err := devices.Get(r.Context(), d.DB, id); err != nil {
		if errors.Is(err, devices.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "device not found")
			return
		}
		d.logErr("inventory: device lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Latest run for the device. The replacement model means at most
	// one run row per device, but ORDER BY created_at DESC keeps this
	// correct even if that invariant ever changes.
	var (
		runID        string
		startedAt    int64
		skillCount   int
		rootCount    int
		agentVersion sql.NullString
	)
	err := d.DB.QueryRowContext(r.Context(), `
		SELECT id, started_at, skill_count, root_count, agent_version
		  FROM inventory_runs WHERE device_id = ?
		 ORDER BY created_at DESC LIMIT 1
	`, id).Scan(&runID, &startedAt, &skillCount, &rootCount, &agentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		// Device exists but has never reported inventory.
		writeJSON(w, http.StatusOK, map[string]any{"run": nil})
		return
	}
	if err != nil {
		d.logErr("inventory: run lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	skills, err := d.loadInventorySkills(r, runID)
	if err != nil {
		d.logErr("inventory: skills query", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"run": inventoryRunView{
			RunID:        runID,
			StartedAt:    startedAt,
			SkillCount:   skillCount,
			RootCount:    rootCount,
			AgentVersion: agentVersion.String,
			Skills:       skills,
		},
	})
}

// loadInventorySkills reads all discovered_skills for a run, ordered
// for a stable matrix (tool, scope, name), decoding the warnings JSON.
func (d Deps) loadInventorySkills(r *http.Request, runID string) ([]inventorySkillView, error) {
	rows, err := d.DB.QueryContext(r.Context(), `
		SELECT tool_key, scope, name, skill_path, has_skill_md, description,
		       effective_state, native_state, content_sha256, file_count,
		       total_bytes, warnings_json
		  FROM discovered_skills WHERE run_id = ?
		 ORDER BY tool_key, scope, name
	`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]inventorySkillView, 0)
	for rows.Next() {
		var (
			v            inventorySkillView
			hasMD        int
			description  sql.NullString
			nativeState  sql.NullString
			contentSHA   sql.NullString
			warningsJSON sql.NullString
		)
		if err := rows.Scan(
			&v.ToolKey, &v.Scope, &v.Name, &v.SkillPath, &hasMD, &description,
			&v.EffectiveState, &nativeState, &contentSHA, &v.FileCount,
			&v.TotalBytes, &warningsJSON,
		); err != nil {
			return nil, err
		}
		v.HasSkillMD = hasMD == 1
		v.Description = description.String
		v.NativeState = nativeState.String
		v.ContentSHA256 = contentSHA.String
		if warningsJSON.Valid && warningsJSON.String != "" {
			// Best-effort decode; a malformed blob (shouldn't happen,
			// we wrote it) yields no warnings rather than a 500.
			_ = json.Unmarshal([]byte(warningsJSON.String), &v.Warnings)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
