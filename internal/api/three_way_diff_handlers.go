package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/source"
)

// three_way_diff_handlers.go serves the three-way comparison the §5.5
// merge UI is built on: base | local | remote (v1.0 §8.5). It extends the
// phase-6 two-way upstream diff with the device's local copy as a third
// side.
//
// Phase 7 boundary (confirmed decision): the agent reports only a
// content_sha256 per skill, never file bytes, and there is no downlink to
// fetch them on demand (that is Phase 8). So the LOCAL side is sha-only:
//
//   - base vs remote is a real file-level diff (both versions live in the
//     registry, so we have their bytes) — reused verbatim from computeDiff.
//   - local is summarised at the top level by comparing its content_sha256
//     to base's and remote's. local_content_available is false, and the
//     per-file §5.5 classification (local_only_changed / both_changed) is
//     deferred to Phase 8 when local bytes can be pulled.
//
// What "base" and "remote" mean: for a bound skill the registry holds one
// or more upstream-kind versions — the OLDEST is the bind baseline (base),
// the NEWEST is the pending update (remote). With fewer than two upstream
// versions there is no pending update, so has_remote_update is false and
// the file list is empty; the local-vs-base comparison still stands.
//
// Read-only (auth, no CSRF). Not bound → 404 (the upstream sides come from
// a binding). Comparison is by content_sha256 throughout, so a moved
// commit whose subtree is byte-identical is never reported as a change.

// localSide is the device copy's standing relative to the registry sides.
// Content is withheld (sha-only) in Phase 7; comparison verbs are
// "same" / "different" / "unknown" (unknown when a sha is missing).
type localSide struct {
	DeviceID string `json:"device_id"`
	ToolKey  string `json:"tool_key"`
	Scope    string `json:"scope"`
	SHA      string `json:"sha,omitempty"`

	// ContentAvailable is always false in Phase 7: the device reports a
	// fingerprint, not bytes, so no per-file local diff is possible yet.
	ContentAvailable bool `json:"content_available"`

	// VsBase / VsRemote compare local's sha to the base / remote version
	// shas: "same", "different", or "unknown" (a side's sha missing).
	VsBase   string `json:"vs_base"`
	VsRemote string `json:"vs_remote"`
}

// threeWayDiffResponse is the GET /api/skills/{id}/three-way-diff payload.
type threeWayDiffResponse struct {
	Name string `json:"name"`

	// HasRemoteUpdate is true when a pending upstream version exists to
	// diff the baseline against. False ⇒ Files empty, base/remote ids empty.
	HasRemoteUpdate bool `json:"has_remote_update"`

	BaseVersionID   string `json:"base_version_id,omitempty"`
	RemoteVersionID string `json:"remote_version_id,omitempty"`

	// Local is present only when the request located a device copy
	// (device_id given and a matching discovered_skills row found).
	Local *localSide `json:"local,omitempty"`

	// Files is the base-vs-remote file-level diff (added/removed/modified,
	// unchanged omitted). Empty when has_remote_update is false.
	Files     []diffFile `json:"files"`
	Unchanged int        `json:"unchanged"`
}

// handleThreeWayDiff computes base|local|remote for a bound skill. Query
// params device_id / tool_key / scope locate the local side; omit them to
// get base-vs-remote only (local nil).
func (d Deps) handleThreeWayDiff(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistryAndSources(w) {
		return
	}
	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}

	// The upstream sides come from a binding; a three-way diff only makes
	// sense for a bound skill.
	if _, err := d.Sources.GetBySkillName(r.Context(), name); err != nil {
		if errors.Is(err, source.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_bound", "skill is not bound to a source")
			return
		}
		d.logErr("three-way: get binding", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	versions, err := d.Registry.ListByName(r.Context(), name)
	if err != nil {
		d.logErr("three-way: list versions", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	// Upstream-kind versions, newest-first (ListByName is created_at DESC).
	var upstreams []registry.Version
	for _, v := range versions {
		if v.Kind == registry.KindUpstream {
			upstreams = append(upstreams, v)
		}
	}

	// Locate the optional local side regardless of upstream count, so a
	// "local differs from base, no remote update yet" answer is possible.
	local, err := d.locateLocalSide(r, name)
	if err != nil {
		d.logErr("three-way: local lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if len(upstreams) < 2 {
		// Baseline only (or none): nothing pending to diff against. We can
		// still report local vs base when a single upstream baseline exists.
		resp := threeWayDiffResponse{
			Name:            name,
			HasRemoteUpdate: false,
			Files:           []diffFile{},
		}
		if len(upstreams) == 1 && local != nil {
			base := upstreams[0]
			local.VsBase = compareSHA(local.SHA, base.ContentSHA256)
			local.VsRemote = "unknown" // no remote
			resp.BaseVersionID = base.ID
		}
		resp.Local = local
		writeJSON(w, http.StatusOK, resp)
		return
	}

	remote := upstreams[0]              // newest = pending update
	base := upstreams[len(upstreams)-1] // oldest = bind baseline

	baseFiles, err := d.Registry.ReadVersionFiles(r.Context(), base)
	if err != nil {
		d.logErr("three-way: read base files", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	remoteFiles, err := d.Registry.ReadVersionFiles(r.Context(), remote)
	if err != nil {
		d.logErr("three-way: read remote files", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	files, unchanged := computeDiff(baseFiles, remoteFiles)

	if local != nil {
		local.VsBase = compareSHA(local.SHA, base.ContentSHA256)
		local.VsRemote = compareSHA(local.SHA, remote.ContentSHA256)
	}

	writeJSON(w, http.StatusOK, threeWayDiffResponse{
		Name:            name,
		HasRemoteUpdate: true,
		BaseVersionID:   base.ID,
		RemoteVersionID: remote.ID,
		Local:           local,
		Files:           files,
		Unchanged:       unchanged,
	})
}

// locateLocalSide reads the device's content_sha256 for this skill from
// its latest inventory run, when device_id / tool_key / scope are given.
// Returns nil (no error) when device_id is absent — the caller treats that
// as "base-vs-remote only". A device_id with no matching discovered row
// yields a localSide with an empty SHA (the UI shows "not installed here").
func (d Deps) locateLocalSide(r *http.Request, name string) (*localSide, error) {
	q := r.URL.Query()
	deviceID := strings.TrimSpace(q.Get("device_id"))
	if deviceID == "" {
		return nil, nil
	}
	toolKey := strings.TrimSpace(q.Get("tool_key"))
	scope := strings.TrimSpace(q.Get("scope"))

	ls := &localSide{
		DeviceID:         deviceID,
		ToolKey:          toolKey,
		Scope:            scope,
		ContentAvailable: false,
		VsBase:           "unknown",
		VsRemote:         "unknown",
	}

	// content_sha256 from the device's latest run for this skill. tool_key /
	// scope narrow the match when given (a skill can exist under several
	// tools); without them we take any row for the name in the latest run.
	query := `
		SELECT ds.content_sha256
		  FROM discovered_skills ds
		  JOIN inventory_runs ir ON ir.id = ds.run_id
		 WHERE ds.device_id = ? AND ds.name = ?`
	args := []any{deviceID, name}
	if toolKey != "" {
		query += ` AND ds.tool_key = ?`
		args = append(args, toolKey)
	}
	if scope != "" {
		query += ` AND ds.scope = ?`
		args = append(args, scope)
	}
	query += ` ORDER BY ir.created_at DESC LIMIT 1`

	var sha sql.NullString
	err := d.DB.QueryRowContext(r.Context(), query, args...).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		// Device has no such skill in its latest run: keep the localSide
		// shell with an empty SHA so the UI can say "not installed here".
		return ls, nil
	}
	if err != nil {
		return nil, err
	}
	ls.SHA = sha.String
	return ls, nil
}

// compareSHA reports how two content fingerprints relate. "unknown" when
// either side is empty (no fingerprint), so the UI never reads a missing
// sha as "same" or "different".
func compareSHA(a, b string) string {
	if a == "" || b == "" {
		return "unknown"
	}
	if a == b {
		return "same"
	}
	return "different"
}
