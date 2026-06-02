// Server-side persistence for an inventory Report. Lives in the same
// package as the wire types but is only imported by the server; the
// agent links just the structs above.

package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

// ErrInvalidReport wraps every Report.Validate failure so callers can
// distinguish a client-fixable payload problem from a server/DB fault
// via errors.Is(err, ErrInvalidReport).
var ErrInvalidReport = errors.New("inventory: invalid report")

// validScopes / validStates mirror the migration-0005 CHECK
// constraints so a malformed report is rejected with a clear error
// before it reaches SQLite (whose CHECK message is less actionable).
var (
	validScopes     = map[string]bool{"user": true, "project": true, "system": true}
	validRootScopes = map[string]bool{"user": true, "system": true}
	validStates     = map[string]bool{
		"on": true, "off": true, "name-only": true,
		"user-invocable-only": true, "ask": true, "unknown": true,
	}
)

// StoreResult reports what a Store call recorded.
type StoreResult struct {
	RunID      string
	SkillCount int
	RootCount  int
}

// Validate checks the report against the schema's enumerations and
// required fields, returning the first problem found wrapped in
// ErrInvalidReport. Called by Store before any write so a bad payload
// never opens a transaction.
func (r Report) Validate() error {
	for i, tool := range r.Tools {
		if tool.ToolKey == "" {
			return fmt.Errorf("%w: tools[%d].tool_key empty", ErrInvalidReport, i)
		}
		if !validScopes[tool.Scope] {
			return fmt.Errorf("%w: tools[%d].scope %q invalid", ErrInvalidReport, i, tool.Scope)
		}
		if tool.RootID == "" {
			return fmt.Errorf("%w: tools[%d].root_id empty", ErrInvalidReport, i)
		}
		for j, sk := range tool.Skills {
			if sk.Name == "" {
				return fmt.Errorf("%w: tools[%d].skills[%d].name empty", ErrInvalidReport, i, j)
			}
			if !validStates[sk.EffectiveState] {
				return fmt.Errorf("%w: tools[%d].skills[%d].effective_state %q invalid", ErrInvalidReport, i, j, sk.EffectiveState)
			}
		}
	}
	for i, rc := range r.Roots {
		if rc.ToolKey == "" {
			return fmt.Errorf("%w: roots[%d].tool_key empty", ErrInvalidReport, i)
		}
		if !validRootScopes[rc.Scope] {
			return fmt.Errorf("%w: roots[%d].scope %q invalid", ErrInvalidReport, i, rc.Scope)
		}
		if rc.Path == "" {
			return fmt.Errorf("%w: roots[%d].path empty", ErrInvalidReport, i)
		}
		if !isReportedRootPathAbs(rc.Path) {
			return fmt.Errorf("%w: roots[%d].path %q not absolute", ErrInvalidReport, i, rc.Path)
		}
		if rc.Registered && rc.RootID == "" {
			return fmt.Errorf("%w: roots[%d].root_id empty for registered root", ErrInvalidReport, i)
		}
	}
	return nil
}

func isReportedRootPathAbs(path string) bool {
	if strings.HasPrefix(path, "/") {
		return true
	}
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return true
	}
	return len(path) >= 3 && isASCIIAlpha(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func isASCIIAlpha(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// Store persists a report for deviceID, replacing the device's prior
// inventory wholesale inside one transaction (v1.0 §12 replacement
// model). now is injected for deterministic timestamps.
//
// Order within the tx:
//  1. delete prior inventory_runs for the device (CASCADE wipes the
//     device's tool_instances + discovered_skills with it)
//  2. insert the new inventory_runs row
//  3. insert tool_instances + their discovered_skills
//
// Deleting prior runs first means only the latest run survives, which
// matches the "latest run is truth" model and keeps the tables from
// growing unbounded.
func Store(ctx context.Context, db *sql.DB, deviceID string, r Report, now time.Time) (StoreResult, error) {
	if err := r.Validate(); err != nil {
		return StoreResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return StoreResult{}, fmt.Errorf("inventory: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Drop prior runs (CASCADE removes their children).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM inventory_runs WHERE device_id = ?`, deviceID); err != nil {
		return StoreResult{}, fmt.Errorf("inventory: clear prior runs: %w", err)
	}

	// 2. New run row.
	runID := idgen.New("inv")
	ms := now.UnixMilli()
	skillCount := r.SkillCount()
	rootCount := r.RootCount()
	var rootsJSON any
	if len(r.Roots) > 0 {
		raw, err := json.Marshal(r.Roots)
		if err != nil {
			return StoreResult{}, fmt.Errorf("inventory: marshal roots: %w", err)
		}
		rootsJSON = string(raw)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO inventory_runs(id, device_id, started_at, skill_count, root_count, agent_version, roots_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, deviceID, ms, skillCount, rootCount, nullable(r.AgentVersion), rootsJSON, ms); err != nil {
		return StoreResult{}, fmt.Errorf("inventory: insert run: %w", err)
	}

	// 3. Tool instances + skills.
	for _, tool := range r.Tools {
		tiID := idgen.New("ti")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tool_instances(id, device_id, run_id, tool_key, display_name, scope, root_id, root_path, last_scanned_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, tiID, deviceID, runID, tool.ToolKey, tool.DisplayName, tool.Scope, tool.RootID, tool.RootPath, ms); err != nil {
			return StoreResult{}, fmt.Errorf("inventory: insert tool_instance: %w", err)
		}

		for _, sk := range tool.Skills {
			var warningsJSON any
			if len(sk.Warnings) > 0 {
				raw, err := json.Marshal(sk.Warnings)
				if err != nil {
					return StoreResult{}, fmt.Errorf("inventory: marshal warnings: %w", err)
				}
				warningsJSON = string(raw)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO discovered_skills(
					id, device_id, run_id, tool_instance_id, tool_key, scope, name, skill_path,
					has_skill_md, description, effective_state, native_state,
					content_sha256, file_count, total_bytes, warnings_json, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
				idgen.New("ds"), deviceID, runID, tiID, tool.ToolKey, tool.Scope, sk.Name, sk.SkillPath,
				boolToInt(sk.HasSkillMD), nullable(sk.Description), sk.EffectiveState, nullable(sk.NativeState),
				nullable(sk.ContentSHA256), sk.FileCount, sk.TotalBytes, warningsJSON, ms,
			); err != nil {
				return StoreResult{}, fmt.Errorf("inventory: insert skill: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return StoreResult{}, fmt.Errorf("inventory: commit: %w", err)
	}
	committed = true
	return StoreResult{RunID: runID, SkillCount: skillCount, RootCount: rootCount}, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
