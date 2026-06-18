package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/drift"
	"github.com/yeluonight/skillfleet/internal/registry"
)

// drift_handlers.go serves the device drift view (v1.0 §8.2): for one
// device's latest inventory run, which skills are running a known
// registry version (clean), which were edited locally (local_modified),
// and which the registry has never heard of (untracked).
//
// Phase 7 infers state by content, not by an installation record: the
// agent already reports a content_sha256 per discovered skill; we compare
// it to the registry's known shas for the same name (internal/drift). No
// skill_installations table, no installed_version_id — that strong
// binding arrives in Phase 8 with a real install action + a downlink.
//
// Read-only (auth, no CSRF). The comparison is by content_sha256, so a
// repo/path/mtime change that leaves bytes identical never reads as
// local_modified — the same guard the update-check engine enforces
// upstream.

// registryVersionLister adapts *registry.Store to drift.VersionLister by
// projecting ListByName's rows down to the (sha → version id) set the
// drift engine needs. Dedup means a name rarely has two versions sharing
// a sha; if it ever did, the last one wins for the representative id,
// which is fine — clean only needs SOME matching version id.
type registryVersionLister struct {
	reg *registry.Store
}

func (l registryVersionLister) ListVersionSHAs(ctx context.Context, name string) (map[string]string, int, error) {
	versions, err := l.reg.ListByName(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	shas := make(map[string]string, len(versions))
	for _, v := range versions {
		shas[v.ContentSHA256] = v.ID
	}
	return shas, len(versions), nil
}

// driftSkillView is one row of the device drift list.
type driftSkillView struct {
	Name                 string `json:"name"`
	ToolKey              string `json:"tool_key"`
	Scope                string `json:"scope"`
	LocalSHA             string `json:"local_sha,omitempty"`
	LocalState           string `json:"local_state"`
	MatchedVersionID     string `json:"matched_version_id,omitempty"`
	RegistryVersionCount int    `json:"registry_version_count"`
}

// driftSummary counts each state across the device's skills so the UI
// can show "3 modified, 1 untracked" without re-scanning the list.
type driftSummary struct {
	Clean         int `json:"clean"`
	LocalModified int `json:"local_modified"`
	Untracked     int `json:"untracked"`
}

type deviceDriftResponse struct {
	DeviceID string           `json:"device_id"`
	Skills   []driftSkillView `json:"skills"`
	Summary  driftSummary     `json:"summary"`
}

// handleDeviceDrift returns the drift classification for a device's
// latest inventory run. Registry not configured → 503; device missing →
// 404; device present but never scanned → 200 with an empty list.
func (d Deps) handleDeviceDrift(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing device id")
		return
	}

	// 404 if the device itself doesn't exist (distinct from "exists but
	// never scanned", which ComputeDeviceDrift returns as an empty list).
	if _, err := devices.Get(r.Context(), d.DB, id); err != nil {
		if errors.Is(err, devices.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "device not found")
			return
		}
		d.logErr("drift: device lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	drifts, err := drift.ComputeDeviceDrift(r.Context(), d.DB, registryVersionLister{reg: d.Registry}, id)
	if err != nil {
		d.logErr("drift: compute", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	skills := make([]driftSkillView, 0, len(drifts))
	var summary driftSummary
	for _, dr := range drifts {
		skills = append(skills, driftSkillView{
			Name:                 dr.Name,
			ToolKey:              dr.ToolKey,
			Scope:                dr.Scope,
			LocalSHA:             dr.LocalSHA,
			LocalState:           string(dr.LocalState),
			MatchedVersionID:     dr.MatchedVersionID,
			RegistryVersionCount: dr.RegistryVersionCount,
		})
		switch dr.LocalState {
		case drift.StateClean:
			summary.Clean++
		case drift.StateLocalModified:
			summary.LocalModified++
		case drift.StateUntracked:
			summary.Untracked++
		}
	}

	writeJSON(w, http.StatusOK, deviceDriftResponse{
		DeviceID: id,
		Skills:   skills,
		Summary:  summary,
	})
}
