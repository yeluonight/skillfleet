package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

// inventorySkillView is one row of the device's skill matrix.
type inventorySkillView struct {
	ToolKey        string             `json:"tool_key"`
	Scope          string             `json:"scope"`
	Name           string             `json:"name"`
	SkillPath      string             `json:"skill_path"`
	RootID         string             `json:"root_id,omitempty"`
	RootPath       string             `json:"root_path,omitempty"`
	Shared         bool               `json:"shared,omitempty"`
	HasSkillMD     bool               `json:"has_skill_md"`
	Description    string             `json:"description,omitempty"`
	EffectiveState string             `json:"effective_state"`
	NativeState    string             `json:"native_state,omitempty"`
	ContentSHA256  string             `json:"content_sha256,omitempty"`
	FileCount      int                `json:"file_count"`
	TotalBytes     int64              `json:"total_bytes"`
	ModifiedAt     int64              `json:"modified_at,omitempty"`
	Warnings       []inventoryWarning `json:"warnings,omitempty"`
}

type inventoryWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// inventoryRunView summarises the latest run plus the full skill list.
type inventoryRunView struct {
	RunID        string                    `json:"run_id"`
	StartedAt    int64                     `json:"started_at"`
	SkillCount   int                       `json:"skill_count"`
	RootCount    int                       `json:"root_count"`
	AgentVersion string                    `json:"agent_version,omitempty"`
	Roots        []inventory.RootCandidate `json:"roots,omitempty"`
	Skills       []inventorySkillView      `json:"skills"`
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
		rootsJSON    sql.NullString
	)
	err := d.DB.QueryRowContext(r.Context(), `
		SELECT id, started_at, skill_count, root_count, agent_version, roots_json
		  FROM inventory_runs WHERE device_id = ?
		 ORDER BY created_at DESC LIMIT 1
	`, id).Scan(&runID, &startedAt, &skillCount, &rootCount, &agentVersion, &rootsJSON)
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

	roots, err := decodeInventoryRoots(rootsJSON)
	if err != nil {
		d.logErr("inventory: roots decode", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	skills, err := d.loadInventorySkills(r, runID, roots)
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
			Roots:        roots,
			Skills:       skills,
		},
	})
}

func decodeInventoryRoots(raw sql.NullString) ([]inventory.RootCandidate, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var roots []inventory.RootCandidate
	if err := json.Unmarshal([]byte(raw.String), &roots); err != nil {
		return nil, fmt.Errorf("decode roots_json: %w", err)
	}
	return roots, nil
}

// loadInventorySkills reads all discovered_skills for a run, ordered
// for a stable matrix (tool, scope, name), decoding the warnings JSON. It
// JOINs tool_instances to carry each skill's root_path/root_id, and marks
// `shared` from the run's roots_json (a path multiple tools read, e.g.
// ~/.agents/skills) so the WebUI can group by path and flag shared roots.
func (d Deps) loadInventorySkills(r *http.Request, runID string, roots []inventory.RootCandidate) ([]inventorySkillView, error) {
	// sharedByPath: a root path the agent flagged Shared (cross-tool). Built
	// from roots_json since the discovered_skills/tool_instances tables don't
	// persist the flag.
	sharedByPath := make(map[string]bool, len(roots))
	for _, rc := range roots {
		if rc.Shared {
			sharedByPath[rc.Path] = true
		}
	}

	rows, err := d.DB.QueryContext(r.Context(), `
		SELECT ds.tool_key, ds.scope, ds.name, ds.skill_path, ds.has_skill_md, ds.description,
		       ds.effective_state, ds.native_state, ds.content_sha256, ds.file_count,
		       ds.total_bytes, ds.modified_at, ds.warnings_json,
		       ti.root_id, ti.root_path
		  FROM discovered_skills ds
		  JOIN tool_instances ti ON ds.tool_instance_id = ti.id
		 WHERE ds.run_id = ?
		 ORDER BY ds.tool_key, ds.scope, ds.name
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
			modifiedAt   sql.NullInt64
			warningsJSON sql.NullString
		)
		if err := rows.Scan(
			&v.ToolKey, &v.Scope, &v.Name, &v.SkillPath, &hasMD, &description,
			&v.EffectiveState, &nativeState, &contentSHA, &v.FileCount,
			&v.TotalBytes, &modifiedAt, &warningsJSON,
			&v.RootID, &v.RootPath,
		); err != nil {
			return nil, err
		}
		v.HasSkillMD = hasMD == 1
		v.Description = description.String
		v.NativeState = nativeState.String
		v.ContentSHA256 = contentSHA.String
		v.ModifiedAt = modifiedAt.Int64
		v.Shared = sharedByPath[v.RootPath]
		if warningsJSON.Valid && warningsJSON.String != "" {
			// Best-effort decode; a malformed blob (shouldn't happen,
			// we wrote it) yields no warnings rather than a 500.
			_ = json.Unmarshal([]byte(warningsJSON.String), &v.Warnings)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
