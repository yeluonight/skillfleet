package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/source"
)

// bindSourceRequest is the POST /api/skills/{id}/bind-source body. The
// skill is identified by the path id (its name); the body describes the
// upstream to bind it to. Only the git/github source types are
// fetchable in phase 6; ref_type defaults to "branch" and ref_name to
// the remote's default branch when omitted.
type bindSourceRequest struct {
	SourceType string `json:"source_type"`
	URL        string `json:"url"`
	Provider   string `json:"provider"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	RefType    string `json:"ref_type"`
	RefName    string `json:"ref_name"`
	Subdir     string `json:"subdir"`
}

// sourceView is the bound-source projection returned to the WebUI. It
// mirrors the columns the Source Tab (v1.0 §13.6) renders.
type sourceView struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	SourceType       string `json:"source_type"`
	URL              string `json:"url,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Owner            string `json:"owner,omitempty"`
	Repo             string `json:"repo,omitempty"`
	RefType          string `json:"ref_type,omitempty"`
	RefName          string `json:"ref_name,omitempty"`
	Subdir           string `json:"subdir,omitempty"`
	LastCheckedAt    int64  `json:"last_checked_at,omitempty"`
	LastRemoteCommit string `json:"last_remote_commit,omitempty"`
}

// bindSourceResponse reports the new binding plus the baseline upstream
// version it produced (so the UI can show "bound, baseline captured").
type bindSourceResponse struct {
	Source  sourceView  `json:"source"`
	Version versionView `json:"version"`
}

// handleBindSource binds a skill to an upstream git/github repo. It
// fetches the subdir at the requested ref, records a skill_sources row,
// and publishes a baseline upstream version (kind=upstream) carrying the
// source id + commit so a later update check has something to compare
// against. The whole thing is rejected if the skill doesn't exist or is
// already bound.
//
// Phase 6 fetches PUBLIC repos only (no credentials); a private repo
// surfaces as a fetch error mapped to 422, not a silent failure.
func (d Deps) handleBindSource(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	if !d.requireSources(w, true) {
		return
	}

	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}

	req, ok := decodeJSON[bindSourceRequest](w, r, 16*1024)
	if !ok {
		return
	}

	// Validate the source type up front. Phase 6 only knows how to fetch
	// git/github repos; other valid types exist in the enum but aren't
	// bindable through this network path.
	st := source.SourceType(strings.TrimSpace(req.SourceType))
	if !isFetchableSourceType(st) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"source_type must be one of git_repo, github_repo, github_release")
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "url is required")
		return
	}
	ref, err := parseRemoteRef(req.RefType, req.RefName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// The target skill must exist (binding attaches an upstream to an
	// existing registry skill, it doesn't create one).
	versions, err := d.Registry.ListByName(r.Context(), name)
	if err != nil {
		d.logErr("source: bind list versions", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if len(versions) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "skill not found")
		return
	}
	// Refuse to bind a skill that already has a source (detach first).
	for _, v := range versions {
		if v.SourceID != "" {
			writeError(w, http.StatusConflict, "already_bound",
				"skill is already bound to a source; detach it first")
			return
		}
	}

	// Fetch the subdir at the requested ref. This is the network step;
	// fetch errors are client-facing (bad url, missing ref/subdir, too
	// large) so they map to 4xx, not 500.
	result, err := d.Fetcher.FetchSubdir(r.Context(), req.URL, ref, req.Subdir)
	if err != nil {
		d.writeFetchError(w, "source: bind fetch", err)
		return
	}

	// Record the binding. The displayed name defaults to the skill name.
	src, err := d.Sources.Create(r.Context(), source.Source{
		Name:             name,
		Type:             st,
		URL:              strings.TrimSpace(req.URL),
		Provider:         strings.TrimSpace(req.Provider),
		Owner:            strings.TrimSpace(req.Owner),
		Repo:             strings.TrimSpace(req.Repo),
		RefType:          ref.Type,
		RefName:          ref.Name,
		Subdir:           strings.TrimSpace(req.Subdir),
		LastRemoteCommit: result.Commit,
		LastCheckedAt:    d.Now(),
	}, d.Now())
	if err != nil {
		if errors.Is(err, source.ErrEmptyName) || errors.Is(err, source.ErrBadType) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		d.logErr("source: bind create", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Publish the fetched tree as a baseline upstream version, tagged with
	// the source id + commit. This is what an update check diffs against.
	files := make([]registry.InMemoryFile, 0, len(result.Files))
	for _, f := range result.Files {
		files = append(files, registry.InMemoryFile{Path: f.Path, Content: f.Content})
	}
	v, err := d.Registry.PublishFromFiles(r.Context(), files, registry.PublishParams{
		Name:         name,
		Kind:         registry.KindUpstream,
		VersionLabel: "upstream baseline",
		SourceID:     src.ID,
		SourceCommit: result.Commit,
	}, d.Now())
	if err != nil {
		// The binding row is already written; roll it back so a failed
		// baseline publish doesn't leave a dangling source the skill
		// can't re-bind (it would trip the already_bound guard above with
		// no version to point at).
		if delErr := d.Sources.Delete(r.Context(), src.ID); delErr != nil {
			d.logErr("source: bind rollback", delErr)
		}
		d.logErr("source: bind publish baseline", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.source_bound",
			Target: audit.Target{Type: "skill", ID: name},
			Detail: map[string]any{
				"source_id":  src.ID,
				"url":        src.URL,
				"commit":     result.Commit,
				"version_id": v.ID,
			},
		})
	}

	writeJSON(w, http.StatusCreated, bindSourceResponse{
		Source:  sourceToView(src),
		Version: versionToView(v),
	})
}

// bindPreviewFile is one entry of a preview's file tree: enough for the
// wizard to show what would be imported without downloading content.
type bindPreviewFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Binary bool   `json:"binary"`
}

// bindPreviewResponse is the dry-run result of POST .../bind-source/preview.
// It fetches the upstream subdir and reports what WOULD be bound — the
// resolved commit, the SKILL.md-derived name/description, the file tree, and
// any manifest warnings — WITHOUT writing a skill_sources row or publishing a
// baseline version. The wizard shows this for confirmation; the actual bind
// is a separate POST. content_sha256 lets the UI note when a later real bind
// would capture identical content.
type bindPreviewResponse struct {
	Commit        string            `json:"commit"`
	Name          string            `json:"name,omitempty"`
	Description   string            `json:"description,omitempty"`
	HasSkillMD    bool              `json:"has_skill_md"`
	ContentSHA256 string            `json:"content_sha256"`
	FileCount     int               `json:"file_count"`
	TotalBytes    int64             `json:"total_bytes"`
	Files         []bindPreviewFile `json:"files"`
	Warnings      []string          `json:"warnings,omitempty"`
}

// handleBindSourcePreview is the dry-run half of the binding wizard
// (§2.9 t8): it performs the same network fetch as bind-source but writes
// nothing, so the operator can review the SKILL.md and file tree before
// committing. It deliberately does NOT require the skill to already exist or
// be unbound — it's a pure "what's at this URL/ref/subdir?" probe. The actual
// state-changing bind (with its skill-exists / not-already-bound guards) is
// the separate bind-source POST.
//
// Like bind-source this is PUBLIC-repos-only in phase 6; fetch errors map to
// the same 4xx/502 codes via writeFetchError so the wizard surfaces a
// meaningful message rather than a generic failure.
func (d Deps) handleBindSourcePreview(w http.ResponseWriter, r *http.Request) {
	if !d.requireSources(w, true) {
		return
	}
	if strings.TrimSpace(r.PathValue("id")) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}
	req, ok := decodeJSON[bindSourceRequest](w, r, 16*1024)
	if !ok {
		return
	}

	st := source.SourceType(strings.TrimSpace(req.SourceType))
	if !isFetchableSourceType(st) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"source_type must be one of git_repo, github_repo, github_release")
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "url is required")
		return
	}
	ref, err := parseRemoteRef(req.RefType, req.RefName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	result, err := d.Fetcher.FetchSubdir(r.Context(), req.URL, ref, req.Subdir)
	if err != nil {
		d.writeFetchError(w, "source: preview fetch", err)
		return
	}

	resp := bindPreviewResponse{
		Commit:        result.Commit,
		Name:          result.Manifest.Name,
		Description:   result.Manifest.Description,
		HasSkillMD:    result.Manifest.HasSkillMD,
		ContentSHA256: result.Manifest.ContentSHA256,
		FileCount:     result.Manifest.FileCount,
		TotalBytes:    result.Manifest.TotalBytes,
		Files:         make([]bindPreviewFile, 0, len(result.Manifest.Files)),
	}
	for _, f := range result.Manifest.Files {
		resp.Files = append(resp.Files, bindPreviewFile{
			Path:   f.Path,
			Size:   f.Size,
			Binary: f.Binary,
		})
	}
	for _, warn := range result.Manifest.Warnings {
		resp.Warnings = append(resp.Warnings, warn.Message)
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeFetchError maps a fetch-layer error to the right client status.
// Bad input (url/subdir) is 400; a missing ref/subdir or an oversized
// tree is 422 (the request was well-formed but the upstream can't be
// used); anything else is a 500 we log.
func (d Deps) writeFetchError(w http.ResponseWriter, logCtx string, err error) {
	switch {
	case errors.Is(err, source.ErrBadURL):
		writeError(w, http.StatusBadRequest, "bad_url", "invalid or disallowed repo URL")
	case errors.Is(err, source.ErrBadSubdir):
		writeError(w, http.StatusBadRequest, "bad_subdir", "invalid subdir")
	case errors.Is(err, source.ErrRefNotFound):
		writeError(w, http.StatusUnprocessableEntity, "ref_not_found", "ref not found on remote")
	case errors.Is(err, source.ErrSubdirNotFound):
		writeError(w, http.StatusUnprocessableEntity, "subdir_not_found", "subdir not found in repo")
	case errors.Is(err, source.ErrTooLarge):
		writeError(w, http.StatusUnprocessableEntity, "too_large", "upstream content exceeds size limit")
	case errors.Is(err, source.ErrTooManyFiles):
		writeError(w, http.StatusUnprocessableEntity, "too_many_files", "upstream subdir exceeds file-count limit")
	default:
		d.logErr(logCtx, err)
		writeError(w, http.StatusBadGateway, "fetch_failed", "failed to fetch from upstream")
	}
}

// isFetchableSourceType reports whether a source type can be bound via a
// network fetch in phase 6. github_release is accepted at the type level
// (the enum allows it) though phase 6 resolves it like a repo ref.
func isFetchableSourceType(t source.SourceType) bool {
	switch t {
	case source.TypeGitRepo, source.TypeGitHubRepo, source.TypeGitHubRelease:
		return true
	}
	return false
}

// parseRemoteRef turns the request's ref_type/ref_name into a
// source.RemoteRef. An empty ref_type defaults to "branch"; an empty
// ref_name is allowed only for branch (meaning the remote's default
// branch). A commit ref_type requires a ref_name.
func parseRemoteRef(refType, refName string) (source.RemoteRef, error) {
	rt := source.RefType(strings.TrimSpace(refType))
	rn := strings.TrimSpace(refName)
	switch rt {
	case "":
		rt = source.RefBranch
	case source.RefBranch, source.RefTag, source.RefCommit, source.RefRelease:
		// ok
	default:
		return source.RemoteRef{}, errors.New("ref_type must be one of branch, tag, commit, release")
	}
	if rn == "" && rt != source.RefBranch {
		return source.RemoteRef{}, errors.New("ref_name is required for tag/commit/release refs")
	}
	return source.RemoteRef{Type: rt, Name: rn}, nil
}

func sourceToView(s source.Source) sourceView {
	v := sourceView{
		ID:               s.ID,
		Name:             s.Name,
		SourceType:       string(s.Type),
		URL:              s.URL,
		Provider:         s.Provider,
		Owner:            s.Owner,
		Repo:             s.Repo,
		RefType:          string(s.RefType),
		RefName:          s.RefName,
		Subdir:           s.Subdir,
		LastRemoteCommit: s.LastRemoteCommit,
	}
	if !s.LastCheckedAt.IsZero() {
		v.LastCheckedAt = s.LastCheckedAt.UnixMilli()
	}
	return v
}

// Source-state strings (v1.0 §11.2). Phase 6 can only distinguish bound
// from unbound at the registry layer — `detached`, `inferred`, etc. need
// either a persisted state column or device-side metadata (Phase 8), so
// detach simply removes the binding and the skill reads back as unbound.
const (
	sourceStateBound   = "bound"
	sourceStateUnbound = "unbound"
)

// deriveSourceState maps "does a binding row exist?" to a §11.2 state.
// This is the only source_state distinction the registry layer can make
// without installation data.
func deriveSourceState(bound bool) string {
	if bound {
		return sourceStateBound
	}
	return sourceStateUnbound
}

// checkUpdatesResponse reports the outcome of a manual update check. The
// state mirrors v1.0 §11.3 upstream_state. pending_version_id is set only
// when state == update_available (a new pending upstream version was
// published). error is a short human string for check_failed, omitted
// otherwise.
type checkUpdatesResponse struct {
	UpstreamState        string `json:"upstream_state"`
	RemoteCommit         string `json:"remote_commit,omitempty"`
	RemoteContentSHA256  string `json:"remote_content_sha256,omitempty"`
	CurrentContentSHA256 string `json:"current_content_sha256,omitempty"`
	PendingVersionID     string `json:"pending_version_id,omitempty"`
	LastCheckedAt        int64  `json:"last_checked_at"`
	Error                string `json:"error,omitempty"`
}

// handleCheckUpdates runs the §8.4 update check for a bound skill on
// demand (the manual half of the "manual + scheduler" decision; t7 adds
// the periodic half). It resolves the skill's binding, runs the t5
// Checker, and returns the resulting upstream_state.
//
// A completed-but-failed check (network down, bad ref, …) is NOT an HTTP
// error: the check ran, its honest outcome is check_failed, and the UI
// should show that rather than a 5xx. Only "this skill has no source to
// check" (404) and infrastructure-not-configured (503) are HTTP errors.
func (d Deps) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	if !d.requireSources(w, true) {
		return
	}
	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}

	src, err := d.Sources.GetBySkillName(r.Context(), name)
	if errors.Is(err, source.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_bound", "skill is not bound to a source")
		return
	}
	if err != nil {
		d.logErr("source: check get binding", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	now := d.Now()
	checker := source.NewChecker(d.Sources, d.Fetcher, d.Registry)
	res, checkErr := checker.Check(r.Context(), src.ID, now)

	// A misconfigured binding (e.g. a non-fetchable source type slipped in)
	// is a 409, not a silent failure — the row exists but can't be checked.
	if errors.Is(checkErr, source.ErrCheckNotBound) {
		writeError(w, http.StatusConflict, "not_checkable",
			"source type cannot be checked for updates")
		return
	}

	resp := checkUpdatesResponse{
		UpstreamState:        string(res.State),
		RemoteCommit:         res.RemoteCommit,
		RemoteContentSHA256:  res.RemoteContentSHA256,
		CurrentContentSHA256: res.CurrentContentSHA256,
		PendingVersionID:     res.PendingVersionID,
		LastCheckedAt:        now.UnixMilli(),
	}
	if res.State == source.StateCheckFailed {
		// Log the underlying cause server-side; surface a short, non-leaky
		// message to the client. The check still "succeeded" as an HTTP
		// request — its result is just "failed".
		if checkErr != nil {
			d.logErr("source: update check failed for "+name, checkErr)
		}
		resp.Error = "update check failed; see server logs"
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.update_checked",
			Target: audit.Target{Type: "skill", ID: name},
			Detail: map[string]any{
				"source_id":          src.ID,
				"upstream_state":     string(res.State),
				"remote_commit":      res.RemoteCommit,
				"pending_version_id": res.PendingVersionID,
			},
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDetachSource removes a skill's source binding. Per the phase 6
// decision, detach deletes the skill_sources row; the immutable upstream
// versions it produced stay in the registry as historical snapshots (their
// source_id is left dangling, which is harmless — there is no FK and
// nothing cascades). The skill subsequently reads back as source_state
// unbound. Re-binding is then possible via bind-source.
func (d Deps) handleDetachSource(w http.ResponseWriter, r *http.Request) {
	if !d.requireSources(w, false) {
		return
	}
	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}

	src, err := d.Sources.GetBySkillName(r.Context(), name)
	if errors.Is(err, source.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_bound", "skill is not bound to a source")
		return
	}
	if err != nil {
		d.logErr("source: detach get binding", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if err := d.Sources.Delete(r.Context(), src.ID); err != nil {
		if errors.Is(err, source.ErrNotFound) {
			// Raced with another detach; the end state is the same.
			writeError(w, http.StatusNotFound, "not_bound", "skill is not bound to a source")
			return
		}
		d.logErr("source: detach delete", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.source_detached",
			Target: audit.Target{Type: "skill", ID: name},
			Detail: map[string]any{"source_id": src.ID, "url": src.URL},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"detached":     true,
		"source_state": sourceStateUnbound,
	})
}
