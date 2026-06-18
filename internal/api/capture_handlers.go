package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/registry"
)

// capture_handlers.go implements "save the local copy as a new central
// version" (v1.0 §8.3): a device's locally-edited skill is published into
// the registry as a new immutable version with kind=local_edit and
// base_version_id pointing at the version it was edited from.
//
// Phase 7 boundary (confirmed decision): the agent is upload-only and has
// no "read my local dir and POST it" path yet (that, plus the file bytes
// in inventory, is Phase 8). So capture takes the file contents from the
// CALLER — e.g. an operator pasting/uploading the edited tree, or a future
// agent that gained an upload path. The server-side registry write is the
// real, finished part; what is deferred is the agent automatically
// sourcing the bytes.
//
// Write (auth + CSRF). Content is UTF-8 text in JSON: capture is for the
// editable skill files (SKILL.md, scripts, docs). Binary capture would
// need a multipart/zip path, which we don't add speculatively in Phase 7.

// captureFile is one file of the local tree the caller is capturing.
// Content is the file's UTF-8 text; the path is package-relative and is
// re-validated by the registry's safefs guard on publish.
type captureFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// captureLocalRequest is the POST /api/skills/{id}/capture-local body.
type captureLocalRequest struct {
	// Files is the local tree to publish. Required and non-empty.
	Files []captureFile `json:"files"`

	// BaseVersionID is the version the local copy was edited from, recorded
	// as the new version's base_version_id (§8.3). Optional: omitted when
	// the caller can't attribute a base (the capture still publishes, just
	// without a recorded ancestor).
	BaseVersionID string `json:"base_version_id,omitempty"`

	// VersionLabel is an optional human label for the captured version.
	VersionLabel string `json:"version_label,omitempty"`

	// DeviceID / ToolKey / Scope are optional provenance for the audit
	// record — where the captured copy came from. They do not affect the
	// published version (which is content-addressed).
	DeviceID string `json:"device_id,omitempty"`
	ToolKey  string `json:"tool_key,omitempty"`
	Scope    string `json:"scope,omitempty"`
}

// captureLocalResponse echoes the published version.
type captureLocalResponse struct {
	Version versionView `json:"version"`
}

// handleCaptureLocal publishes a caller-supplied local tree as a new
// local_edit version of an existing skill.
func (d Deps) handleCaptureLocal(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}
	// Cap the body at 4 MiB — capture is for editable skill files, not
	// arbitrary uploads.
	req, ok := decodeJSON[captureLocalRequest](w, r, 4*1024*1024)
	if !ok {
		return
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "files is required and must be non-empty")
		return
	}

	// The skill must already exist — capture adds a version to a known
	// skill, it doesn't create one (use POST /api/skills for that).
	versions, err := d.Registry.ListByName(r.Context(), name)
	if err != nil {
		d.logErr("capture: list versions", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if len(versions) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "skill not found")
		return
	}

	// If a base version is named, it must belong to this skill — a base
	// pointing at another skill's version would be meaningless provenance.
	if req.BaseVersionID != "" && !versionBelongsToSkill(versions, req.BaseVersionID) {
		writeError(w, http.StatusBadRequest, "bad_request", "base_version_id does not belong to this skill")
		return
	}

	files := make([]registry.InMemoryFile, 0, len(req.Files))
	for _, f := range req.Files {
		p := strings.TrimSpace(f.Path)
		if p == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "every file needs a non-empty path")
			return
		}
		files = append(files, registry.InMemoryFile{Path: p, Content: []byte(f.Content)})
	}

	// Publish as an immutable local_edit version. PublishFromFiles runs the
	// manifest generation (which enforces the safefs package-path guard)
	// and dedupes identical content under the same name — capturing bytes
	// that already match an existing version is a no-op that returns it.
	v, err := d.Registry.PublishFromFiles(r.Context(), files, registry.PublishParams{
		Name:          name,
		Kind:          registry.KindLocalEdit,
		VersionLabel:  strings.TrimSpace(req.VersionLabel),
		BaseVersionID: req.BaseVersionID,
	}, d.Now())
	if err != nil {
		if errors.Is(err, registry.ErrEmptyName) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		// A bad package path (safefs reject) or invalid SKILL.md surfaces
		// as a manifest error — client-fixable, so 400 not 500.
		d.logErr("capture: publish", err)
		writeError(w, http.StatusBadRequest, "capture_failed", "could not publish local copy: "+err.Error())
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.local_captured",
			Target: audit.Target{Type: "skill", ID: name},
			Detail: map[string]any{
				"version_id":      v.ID,
				"base_version_id": req.BaseVersionID,
				"content_sha256":  v.ContentSHA256,
				"device_id":       req.DeviceID,
				"tool_key":        req.ToolKey,
				"scope":           req.Scope,
			},
		})
	}

	writeJSON(w, http.StatusCreated, captureLocalResponse{Version: versionToView(v)})
}

// versionBelongsToSkill reports whether id is among the skill's versions.
func versionBelongsToSkill(versions []registry.Version, id string) bool {
	for _, v := range versions {
		if v.ID == id {
			return true
		}
	}
	return false
}
