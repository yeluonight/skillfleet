package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/draft"
)

// validationIssueView is one finding from draft validation.
type validationIssueView struct {
	Severity string `json:"severity"` // "error" | "warning"
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"` // 1-based; 0 when no position
	Col      int    `json:"col,omitempty"`  // 1-based; 0 when no column
}

// validateResponse reports whether a draft is publishable plus its
// findings.
type validateResponse struct {
	OK     bool                  `json:"ok"` // true when no error-severity issues
	Issues []validationIssueView `json:"issues"`
}

// handleValidateDraft runs content validation on a draft without
// mutating it (v1.0 §7.3 "Validate"). Always 200 on a known draft; the
// body's `ok` flag and issue list carry the verdict.
func (d Deps) handleValidateDraft(w http.ResponseWriter, r *http.Request) {
	if !d.requireDrafts(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	issues, err := d.Drafts.Validate(r.Context(), id)
	if err != nil {
		if errors.Is(err, draft.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "draft not found")
			return
		}
		d.logErr("draft: validate", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, validateResponse{
		OK:     !draft.HasErrors(issues),
		Issues: toIssueViews(issues),
	})
}

// publishResponse is returned on a successful publish: the new version
// id plus any advisory warnings that didn't block it.
type publishResponse struct {
	VersionID string                `json:"version_id"`
	Name      string                `json:"name"`
	Warnings  []validationIssueView `json:"warnings,omitempty"`
}

// handlePublishDraft validates and publishes a draft to a new immutable
// version (v1.0 §7.3 "Publish New Version"). Validation errors return
// 422 with the issues; a non-open draft returns 409.
func (d Deps) handlePublishDraft(w http.ResponseWriter, r *http.Request) {
	if !d.requireDrafts(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	res, err := d.Drafts.Publish(r.Context(), id, d.Now())
	switch {
	case err == nil:
		// fall through to success
	case errors.Is(err, draft.ErrValidationFailed):
		writeJSON(w, http.StatusUnprocessableEntity, validateResponse{
			OK:     false,
			Issues: toIssueViews(res.Issues),
		})
		return
	case errors.Is(err, draft.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "draft not found")
		return
	case errors.Is(err, draft.ErrNotOpen):
		writeError(w, http.StatusConflict, "not_open", "draft is not open; already published or discarded")
		return
	default:
		d.logErr("draft: publish", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "draft.published",
			Target: audit.Target{Type: "draft", ID: id},
			Detail: map[string]any{"version_id": res.Version.ID, "name": res.Version.Name},
		})
	}
	writeJSON(w, http.StatusCreated, publishResponse{
		VersionID: res.Version.ID,
		Name:      res.Version.Name,
		Warnings:  toIssueViews(res.Issues),
	})
}

func toIssueViews(issues []draft.Issue) []validationIssueView {
	out := make([]validationIssueView, 0, len(issues))
	for _, i := range issues {
		out = append(out, validationIssueView{
			Severity: string(i.Severity),
			Code:     i.Code,
			Path:     i.Path,
			Message:  i.Message,
			Line:     i.Line,
			Col:      i.Col,
		})
	}
	return out
}
