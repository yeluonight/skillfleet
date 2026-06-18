package drift

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// VersionLister is the slice of the registry this package consumes: the
// content fingerprints recorded for a given skill name. Defined here
// (consumer side) so the drift package never imports the registry store
// and tests inject a fake. The API layer adapts *registry.Store to this
// with a small closure over ListByName (registry rows → sha set).
type VersionLister interface {
	// ListVersionSHAs returns, for one skill name, each distinct
	// content_sha256 the registry holds mapped to a representative version
	// id, plus the total number of versions for the name. count is
	// returned separately from len(shas) because two versions can share a
	// content_sha256 (dedup makes that rare, but the contract is "how many
	// versions exist for this name").
	ListVersionSHAs(ctx context.Context, name string) (shas map[string]string, count int, err error)
}

// ComputeDeviceDrift classifies every skill in a device's latest
// inventory run against the registry (v1.0 §8.2). It reads the device's
// discovered_skills (the tool × scope × name matrix the agent reported),
// and for each distinct skill name asks the registry for its known
// content_sha256 set, then runs Classify.
//
// A device with no inventory run yet yields an empty slice (not an
// error) — the caller renders "never scanned" rather than failing.
//
// Per-name registry lookups are memoised within the call so a device
// with many copies of the same skill (e.g. the same name under several
// tools) costs one registry query per distinct name, not per row.
func ComputeDeviceDrift(ctx context.Context, db *sql.DB, reg VersionLister, deviceID string) ([]SkillDrift, error) {
	runID, err := latestRunID(ctx, db, deviceID)
	if err != nil {
		return nil, err
	}
	if runID == "" {
		// Device exists but never reported inventory: nothing to classify.
		return []SkillDrift{}, nil
	}

	rows, err := db.QueryContext(ctx, `
		SELECT tool_key, scope, name, content_sha256
		  FROM discovered_skills WHERE run_id = ?
		 ORDER BY tool_key, scope, name
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("drift: query discovered skills: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Cache per-name registry lookups across rows of this run.
	type regEntry struct {
		shas  map[string]string
		count int
	}
	cache := make(map[string]regEntry)

	out := make([]SkillDrift, 0)
	for rows.Next() {
		var (
			toolKey    string
			scope      string
			name       string
			contentSHA sql.NullString
		)
		if err := rows.Scan(&toolKey, &scope, &name, &contentSHA); err != nil {
			return nil, fmt.Errorf("drift: scan discovered skill: %w", err)
		}

		entry, ok := cache[name]
		if !ok {
			shas, count, err := reg.ListVersionSHAs(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("drift: list registry versions for %q: %w", name, err)
			}
			entry = regEntry{shas: shas, count: count}
			cache[name] = entry
		}

		hasName := entry.count > 0
		state, matched := Classify(contentSHA.String, entry.shas, hasName)

		out = append(out, SkillDrift{
			Name:                 name,
			ToolKey:              toolKey,
			Scope:                scope,
			LocalSHA:             contentSHA.String,
			LocalState:           state,
			MatchedVersionID:     matched,
			RegistryVersionCount: entry.count,
		})
	}
	return out, rows.Err()
}

// latestRunID returns the most recent inventory run id for a device, or
// "" when the device has no run. Mirrors the replacement-model lookup in
// inventory_handlers (ORDER BY created_at DESC LIMIT 1).
func latestRunID(ctx context.Context, db *sql.DB, deviceID string) (string, error) {
	var runID string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM inventory_runs WHERE device_id = ?
		 ORDER BY created_at DESC LIMIT 1
	`, deviceID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("drift: latest run lookup: %w", err)
	}
	return runID, nil
}
