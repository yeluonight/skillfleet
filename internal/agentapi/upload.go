// /agent/upload: receive a discovered skill's real file bytes from an
// authenticated agent and adopt them into the registry as a new version
// (mgmt-refactor track A). This is the device -> registry direction: the
// agent ran a capture_skill job, read the skill directory on disk, and
// POSTs the files here so a skill that previously only existed on a device
// becomes a managed, editable, versionable registry skill.
//
// The body carries base64-encoded file content, so this route uses the
// larger MaxAgentUploadBytes cap (via AuthenticateLarge); every other
// /agent/* route keeps the tight default. The HMAC middleware has already
// confirmed the device is enrolled + approved before we get here.

package agentapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/deploy"
)

// SkillAdopter publishes uploaded files as a new registry version. The
// server wires an adapter over *registry.Store; defining it as an interface
// keeps agentapi from importing registry and makes the handler testable
// with a fake. It receives already-decoded file bytes (the handler decodes
// the base64 once). Returns the new version id.
type SkillAdopter interface {
	AdoptSkill(name string, files []deploy.AdoptFile, source deploy.AdoptSource) (versionID string, err error)
}

// handleUpload adopts an uploaded skill into the registry. The agent has
// already been authenticated by the HMAC middleware; we take the device id
// from the auth context (not the body) as the authoritative source.
func (d Deps) handleUpload(w http.ResponseWriter, r *http.Request) {
	ac, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing auth context")
		return
	}
	if d.Adopter == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "skill adoption not configured")
		return
	}

	var req deploy.UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.SkillName = strings.TrimSpace(req.SkillName)
	if req.SkillName == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "skill_name is required")
		return
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "at least one file is required")
		return
	}
	// Decode each file's base64 once here; the adopter consumes raw bytes.
	// A malformed upload fails 400 before the registry is touched.
	files := make([]deploy.AdoptFile, 0, len(req.Files))
	for _, f := range req.Files {
		content, err := base64.StdEncoding.DecodeString(f.ContentBase64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "file "+f.Path+": invalid base64")
			return
		}
		files = append(files, deploy.AdoptFile{Path: f.Path, Content: content})
	}

	// The device id is authoritative from the signed context, overriding
	// whatever the body claimed.
	req.Source.DeviceID = ac.DeviceID

	versionID, err := d.Adopter.AdoptSkill(req.SkillName, files, req.Source)
	if err != nil {
		if errors.Is(err, ErrAdoptInvalid) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		d.logErr("agent upload: adopt skill", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "device", ID: ac.DeviceID},
			Action: "skill.adopted",
			Target: audit.Target{Type: "skill", ID: req.SkillName},
			Detail: map[string]any{
				"version_id": versionID,
				"tool_key":   req.Source.ToolKey,
				"scope":      req.Source.Scope,
				"root_id":    req.Source.RootID,
				"file_count": len(req.Files),
			},
		})
	}

	writeJSON(w, http.StatusCreated, deploy.UploadResponse{VersionID: versionID})
}

// ErrAdoptInvalid is returned by a SkillAdopter when the upload is
// rejected for a caller-fixable reason (bad path, empty name). The handler
// maps it to 400; any other error is 500.
var ErrAdoptInvalid = errors.New("agentapi: invalid skill upload")
