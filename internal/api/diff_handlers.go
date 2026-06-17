package api

import (
	"bytes"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/internal/source"
)

// diff_handlers.go serves the upstream diff page (§17 task 6): a TWO-WAY
// file-level comparison between a skill's current upstream snapshot and the
// pending upstream version an update check produced. Three-way (local edits
// vs base vs upstream) is Phase 7.
//
// What "current" and "pending" mean in phase 6 (no adoption/merge yet): the
// registry holds one or more upstream-kind versions for a bound skill. The
// OLDEST is the bind-time baseline (what the operator is effectively on); the
// NEWEST is the update awaiting action. With only the baseline present there
// is no pending update and the diff is empty (has_update=false).
//
// The server does file-level alignment + status classification and hands back
// each side's text content; line-level diffing is the client's job (the WebUI
// renders it with Monaco's DiffEditor). Binary or oversized files are flagged
// without content, exactly like the version-file endpoint, so the response
// can't smuggle non-UTF-8 bytes into a JSON string.

// diffStatus classifies one path across the two sides.
type diffStatus string

const (
	diffAdded     diffStatus = "added"     // only in pending (upstream introduced it)
	diffRemoved   diffStatus = "removed"   // only in baseline (upstream dropped it)
	diffModified  diffStatus = "modified"  // in both, content differs
	diffUnchanged diffStatus = "unchanged" // in both, identical bytes
)

// diffFile is one path's two-way comparison. For text files within the
// editable limit, BaseContent/TargetContent carry the sides' UTF-8 text (a
// side absent for added/removed is empty with its *Present=false). Binary or
// oversized files set Editable=false and omit content; the UI shows a
// "binary/oversized changed" marker instead of a line diff.
type diffFile struct {
	Path   string     `json:"path"`
	Status diffStatus `json:"status"`

	// Editable is true only when BOTH present sides are text within the size
	// limit — i.e. a line-level diff is meaningful. Binary/oversized → false.
	Editable bool `json:"editable"`
	Binary   bool `json:"binary"`

	BasePresent   bool   `json:"base_present"`
	TargetPresent bool   `json:"target_present"`
	BaseContent   string `json:"base_content,omitempty"`
	TargetContent string `json:"target_content,omitempty"`

	// Sizes for the UI even when content is withheld (binary/oversized).
	BaseSize   int64 `json:"base_size"`
	TargetSize int64 `json:"target_size"`
}

// upstreamDiffResponse is the GET /api/skills/{id}/upstream-diff payload.
type upstreamDiffResponse struct {
	Name      string `json:"name"`
	HasUpdate bool   `json:"has_update"`
	// Version ids being compared (empty when has_update is false).
	BaseVersionID   string `json:"base_version_id,omitempty"`
	TargetVersionID string `json:"target_version_id,omitempty"`
	// Files lists every path that differs (added/removed/modified). Unchanged
	// files are omitted to keep the payload focused on what changed; the
	// counts summarise the full picture.
	Files     []diffFile `json:"files"`
	Unchanged int        `json:"unchanged"`
}

// handleUpstreamDiff computes the two-way diff between a bound skill's
// baseline upstream version and its newest (pending) upstream version.
// Read-only (auth, no CSRF). Not bound → 404; bound but no pending update →
// 200 with has_update=false and an empty file list.
func (d Deps) handleUpstreamDiff(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistryAndSources(w) {
		return
	}
	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}

	// A diff only makes sense for a bound skill (the upstream side comes from
	// its binding's update history).
	if _, err := d.Sources.GetBySkillName(r.Context(), name); err != nil {
		if errors.Is(err, source.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_bound", "skill is not bound to a source")
			return
		}
		d.logErr("diff: get binding", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	versions, err := d.Registry.ListByName(r.Context(), name)
	if err != nil {
		d.logErr("diff: list versions", err)
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
	if len(upstreams) < 2 {
		// Baseline only (or none): nothing pending to diff against.
		writeJSON(w, http.StatusOK, upstreamDiffResponse{
			Name:      name,
			HasUpdate: false,
			Files:     []diffFile{},
		})
		return
	}
	target := upstreams[0]              // newest = pending update
	base := upstreams[len(upstreams)-1] // oldest = bind baseline

	baseFiles, err := d.Registry.ReadVersionFiles(r.Context(), base)
	if err != nil {
		d.logErr("diff: read base files", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	targetFiles, err := d.Registry.ReadVersionFiles(r.Context(), target)
	if err != nil {
		d.logErr("diff: read target files", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	files, unchanged := computeDiff(baseFiles, targetFiles)
	writeJSON(w, http.StatusOK, upstreamDiffResponse{
		Name:            name,
		HasUpdate:       true,
		BaseVersionID:   base.ID,
		TargetVersionID: target.ID,
		Files:           files,
		Unchanged:       unchanged,
	})
}

// computeDiff aligns two file sets by path and classifies each. It returns
// the changed files (added/removed/modified, sorted by path) and a count of
// unchanged ones. Identical bytes ⇒ unchanged (and omitted from the result).
// Content is only attached for text files within the editable size limit and
// only after a UTF-8 re-validation, mirroring handleVersionFile so the JSON
// never carries non-UTF-8 bytes.
func computeDiff(baseFiles, targetFiles []registry.InMemoryFile) ([]diffFile, int) {
	baseByPath := make(map[string][]byte, len(baseFiles))
	for _, f := range baseFiles {
		baseByPath[f.Path] = f.Content
	}
	targetByPath := make(map[string][]byte, len(targetFiles))
	for _, f := range targetFiles {
		targetByPath[f.Path] = f.Content
	}

	// Union of all paths from both sides, sorted for stable output.
	pathSet := make(map[string]struct{}, len(baseFiles)+len(targetFiles))
	for p := range baseByPath {
		pathSet[p] = struct{}{}
	}
	for p := range targetByPath {
		pathSet[p] = struct{}{}
	}
	paths := slices.Sorted(maps.Keys(pathSet))

	out := make([]diffFile, 0)
	unchanged := 0
	for _, p := range paths {
		baseContent, inBase := baseByPath[p]
		targetContent, inTarget := targetByPath[p]

		var status diffStatus
		switch {
		case inBase && !inTarget:
			status = diffRemoved
		case !inBase && inTarget:
			status = diffAdded
		default: // in both
			if bytes.Equal(baseContent, targetContent) {
				unchanged++
				continue // omit unchanged from the file list
			}
			status = diffModified
		}

		df := diffFile{
			Path:          p,
			Status:        status,
			BasePresent:   inBase,
			TargetPresent: inTarget,
			BaseSize:      int64(len(baseContent)),
			TargetSize:    int64(len(targetContent)),
		}
		fillDiffContent(&df, baseContent, inBase, targetContent, inTarget)
		out = append(out, df)
	}
	return out, unchanged
}

// fillDiffContent decides whether a line-level diff is meaningful (both
// present sides are text within the editable limit) and, if so, attaches the
// UTF-8 content. Otherwise it flags binary and withholds content. A side that
// isn't present (added/removed) doesn't block editability — only the present
// side(s) must qualify.
func fillDiffContent(df *diffFile, baseContent []byte, inBase bool, targetContent []byte, inTarget bool) {
	baseOK := !inBase || editableText(baseContent)
	targetOK := !inTarget || editableText(targetContent)
	if !baseOK || !targetOK {
		// At least one present side is binary/oversized/non-UTF-8: mark binary
		// and withhold content. The UI shows a "binary changed" marker.
		df.Binary = true
		df.Editable = false
		return
	}
	df.Editable = true
	if inBase {
		df.BaseContent = string(baseContent)
	}
	if inTarget {
		df.TargetContent = string(targetContent)
	}
}

// editableText reports whether content is text within the editable size
// limit — the same gate handleVersionFile uses, plus a UTF-8 re-validation so
// a manifest mislabel can't leak invalid bytes into a JSON string.
func editableText(content []byte) bool {
	if int64(len(content)) > skill.MaxEditableBytes {
		return false
	}
	if skill.IsBinaryContent(content) {
		return false
	}
	return utf8.Valid(content)
}
