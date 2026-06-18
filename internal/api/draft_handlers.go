package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/draft"
	"github.com/yeluonight/skillfleet/internal/safefs"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// draftFileView is one file in a draft's tree. Text content is inlined
// when editable; binary/oversized files report metadata only (their
// bytes are fetched via the file API in t9).
type draftFileView struct {
	Path     string `json:"path"`
	IsBinary bool   `json:"is_binary"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Content  string `json:"content,omitempty"`
}

// draftView is the GET/POST /api/skill-drafts payload.
type draftView struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Title         string          `json:"title,omitempty"`
	Status        string          `json:"status"`
	BaseVersionID string          `json:"base_version_id,omitempty"`
	CreatedAt     int64           `json:"created_at"`
	UpdatedAt     int64           `json:"updated_at"`
	Files         []draftFileView `json:"files"`
}

type createDraftRequest struct {
	// Provide BaseVersionID to fork an existing version, or Name to
	// start a blank skill. Title is optional.
	Name          string `json:"name"`
	Title         string `json:"title"`
	BaseVersionID string `json:"base_version_id"`
}

// handleCreateDraft opens a new draft, either forked from a version
// (base_version_id) or blank (name). Returns the draft with its files.
func (d Deps) handleCreateDraft(w http.ResponseWriter, r *http.Request) {
	if !d.requireDrafts(w) {
		return
	}
	req, ok := decodeJSON[createDraftRequest](w, r, 16*1024)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	base := strings.TrimSpace(req.BaseVersionID)
	if name == "" && base == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "provide name (blank draft) or base_version_id (fork)")
		return
	}
	// A blank draft's name becomes a skill name + package dir, so apply
	// the same charset guard as skill creation. A fork inherits the
	// base version's (already-valid) name, so skip the check there.
	if base == "" && !validSkillName(name) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"name must be 1-64 chars: letters, digits, '-', '_', '.'")
		return
	}

	dr, err := d.Drafts.Create(r.Context(), draft.CreateParams{
		Name:          name,
		Title:         strings.TrimSpace(req.Title),
		BaseVersionID: base,
		CreatedBy:     d.sessionActor(r).ID,
	}, d.Now())
	if err != nil {
		if errors.Is(err, draft.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "bad_request", "base version not found")
			return
		}
		if errors.Is(err, draft.ErrEmptyName) {
			writeError(w, http.StatusBadRequest, "bad_request", "name is required")
			return
		}
		d.logErr("draft: create", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "draft.created",
			Target: audit.Target{Type: "draft", ID: dr.ID},
			Detail: map[string]any{"name": dr.Name, "base_version_id": base},
		})
	}
	writeJSON(w, http.StatusCreated, draftToView(dr))
}

func (d Deps) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	if !d.requireDrafts(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing draft id")
		return
	}
	dr, err := d.Drafts.Load(r.Context(), id)
	if err != nil {
		if errors.Is(err, draft.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "draft not found")
			return
		}
		d.logErr("draft: load", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, draftToView(dr))
}

func draftToView(dr draft.Draft) draftView {
	files := make([]draftFileView, 0, len(dr.Files))
	for _, f := range dr.Files {
		fv := draftFileView{
			Path:     f.Path,
			IsBinary: f.IsBinary,
			Size:     f.Size,
			Encoding: f.Encoding,
			SHA256:   f.SHA256,
		}
		// Content is present only for inlined text files (Load leaves
		// binary content nil).
		if !f.IsBinary && f.Content != nil {
			fv.Content = string(f.Content)
		}
		files = append(files, fv)
	}
	return draftView{
		ID:            dr.ID,
		Name:          dr.Name,
		Title:         dr.Title,
		Status:        dr.Status,
		BaseVersionID: dr.BaseVersionID,
		CreatedAt:     dr.CreatedAt.UnixMilli(),
		UpdatedAt:     dr.UpdatedAt.UnixMilli(),
		Files:         files,
	}
}

// draftFileWriteRequest is the body for create/replace file. content is
// the full UTF-8 file body. ConvertToUTF8 opts into normalising an
// otherwise-rejected encoding nuisance (a leading UTF-8 BOM) instead of
// failing the write — the WebUI's "convert to UTF-8 and save" action
// (v1.0 §7.7). Without it, a BOM is rejected rather than silently
// stripped (§1.3.8: non-UTF-8 is never silently rewritten).
type draftFileWriteRequest struct {
	Content       string `json:"content"`
	ConvertToUTF8 bool   `json:"convert_to_utf8"`
}

// utf8BOM is the 3-byte UTF-8 byte-order mark. It is valid UTF-8 but
// pollutes frontmatter parsing and string comparisons, so the editor
// treats it as an encoding nuisance to reject-or-convert, never to keep
// silently. Built from raw bytes so the source file itself stays
// BOM-free (a literal BOM mid-file is a compile error).
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// handleCreateDraftFile creates a NEW file in a draft (POST). It is an
// error if the path already exists — use PUT to replace.
func (d Deps) handleCreateDraftFile(w http.ResponseWriter, r *http.Request) {
	d.writeDraftFile(w, r, false)
}

// handleReplaceDraftFile replaces (or creates) a file in a draft (PUT).
// PUT is idempotent: it does not require the path to pre-exist.
func (d Deps) handleReplaceDraftFile(w http.ResponseWriter, r *http.Request) {
	d.writeDraftFile(w, r, true)
}

// writeDraftFile is the shared body of the create/replace handlers.
// When allowExisting is false (POST), an existing path yields 409.
func (d Deps) writeDraftFile(w http.ResponseWriter, r *http.Request, allowExisting bool) {
	if !d.requireDrafts(w) {
		return
	}
	if !hasJSONContentType(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	rawPath := r.PathValue("path")

	var req draftFileWriteRequest
	// Read the raw body first so we can reject non-UTF-8 bytes loudly:
	// json.Decode would otherwise replace invalid bytes with U+FFFD,
	// which is exactly the silent rewrite §1.3.8 forbids. MaxEditableBytes
	// + JSON overhead headroom bounds the read.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, skill.MaxEditableBytes+4096))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds the editable size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "could not read request body")
		return
	}
	if !utf8.Valid(body) {
		writeError(w, http.StatusBadRequest, "bad_request", "request body is not valid UTF-8")
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	// Normalise the one encoding nuisance a UTF-8 JSON body can still
	// carry: a leading BOM. Reject it unless the caller explicitly opts
	// into conversion (§1.3.8 — never strip silently).
	content := []byte(req.Content)
	if bytes.HasPrefix(content, utf8BOM) {
		if !req.ConvertToUTF8 {
			writeError(w, http.StatusUnprocessableEntity, "encoding_bom",
				"file begins with a UTF-8 BOM; resend with convert_to_utf8=true to strip it")
			return
		}
		content = content[len(utf8BOM):]
	}

	if !allowExisting {
		exists, err := d.Drafts.FileExists(r.Context(), id, rawPath)
		if err != nil {
			if errors.Is(err, safefs.ErrEmptyPath) || isPathError(err) {
				writeError(w, http.StatusBadRequest, "bad_path", err.Error())
				return
			}
			d.logErr("draft file: exists", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		if exists {
			writeError(w, http.StatusConflict, "already_exists", "file already exists; use PUT to replace")
			return
		}
	}

	f, err := d.Drafts.PutFile(r.Context(), id, rawPath, content, d.Now())
	if err != nil {
		d.writeDraftFileErr(w, err)
		return
	}
	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "draft.file.saved",
			Target: audit.Target{Type: "draft", ID: id},
			Detail: map[string]any{"path": f.Path},
		})
	}
	writeJSON(w, http.StatusOK, draftFileView{
		Path:     f.Path,
		IsBinary: f.IsBinary,
		Size:     f.Size,
		Encoding: f.Encoding,
		SHA256:   f.SHA256,
	})
}

// handleDeleteDraftFile removes a file from a draft.
func (d Deps) handleDeleteDraftFile(w http.ResponseWriter, r *http.Request) {
	if !d.requireDrafts(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	rawPath := r.PathValue("path")
	if err := d.Drafts.DeleteFile(r.Context(), id, rawPath, d.Now()); err != nil {
		if errors.Is(err, draft.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "file not in draft")
			return
		}
		d.writeDraftFileErr(w, err)
		return
	}
	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "draft.file.deleted",
			Target: audit.Target{Type: "draft", ID: id},
			Detail: map[string]any{"path": rawPath},
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteDraft discards an entire draft.
func (d Deps) handleDeleteDraft(w http.ResponseWriter, r *http.Request) {
	if !d.requireDrafts(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := d.Drafts.Delete(r.Context(), id); err != nil {
		if errors.Is(err, draft.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "draft not found")
			return
		}
		d.logErr("draft: delete", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "draft.discarded",
			Target: audit.Target{Type: "draft", ID: id},
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeDraftFileErr maps the common draft mutation errors to responses.
func (d Deps) writeDraftFileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, draft.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "draft not found")
	case errors.Is(err, draft.ErrNotOpen):
		writeError(w, http.StatusConflict, "not_open", "draft is not open for edits")
	case isPathError(err):
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
	default:
		d.logErr("draft file: mutate", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// isPathError reports whether err is one of safefs's path-rejection
// sentinels (all client-fixable → 400).
func isPathError(err error) bool {
	for _, s := range []error{
		safefs.ErrEmptyPath, safefs.ErrAbsolutePath, safefs.ErrDriveLetter,
		safefs.ErrDotDot, safefs.ErrBackslash, safefs.ErrControlChar,
		safefs.ErrTrailingSlash, safefs.ErrPathTooLong, safefs.ErrPathTooDeep,
		safefs.ErrReservedName,
	} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}
