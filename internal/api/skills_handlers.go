package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/internal/skillmd"
	"github.com/yeluonight/skillfleet/internal/source"
)

// skillSummaryView is one row of the Registry list (v1.0 §13.3). The
// installation/risk columns described there arrive in later phases; the
// source column lands in phase 6 (source_state). Phase 4 surfaces the
// version-derived fields it can compute.
type skillSummaryView struct {
	Name            string `json:"name"`
	VersionCount    int    `json:"version_count"`
	LatestVersionID string `json:"latest_version_id"`
	LatestLabel     string `json:"latest_label,omitempty"`
	LatestKind      string `json:"latest_kind"`
	SourceState     string `json:"source_state"` // bound|unbound (phase 6)
	UpdatedAt       int64  `json:"updated_at"`
}

// versionView is one entry in a skill's version history.
type versionView struct {
	ID            string `json:"id"`
	VersionLabel  string `json:"version_label,omitempty"`
	Kind          string `json:"kind"`
	ContentSHA256 string `json:"content_sha256"`
	BaseVersionID string `json:"base_version_id,omitempty"`
	FileCount     int    `json:"file_count"`
	TotalBytes    int64  `json:"total_bytes"`
	CreatedAt     int64  `json:"created_at"`
}

// skillDetailView is the GET /api/skills/:id payload: the skill name, its
// full (newest-first) version history, and (phase 6) its source binding.
// SourceState is always set (bound|unbound); Source and LastCheckedAt are
// present only when the skill is bound. upstream_state is intentionally
// NOT here: the registry layer can't tell whether the latest upstream
// version is an un-adopted update, and GET must stay read-only/fast — the
// authoritative upstream_state comes from POST check-updates / the
// scheduler (t7).
type skillDetailView struct {
	Name          string        `json:"name"`
	Versions      []versionView `json:"versions"`
	Source        *sourceView   `json:"source,omitempty"`
	SourceState   string        `json:"source_state"`
	LastCheckedAt int64         `json:"last_checked_at,omitempty"`
}

// handleListSkills returns every skill in the Registry, one row per
// distinct version name.
func (d Deps) handleListSkills(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	skills, err := d.Registry.ListSkills(r.Context())
	if err != nil {
		d.logErr("skills: list", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Build a name→bound set in one query so the source column doesn't
	// become N+1 across the skill list. When Sources isn't configured the
	// set stays empty and every skill reads back unbound.
	bound := map[string]bool{}
	if d.Sources != nil {
		srcs, err := d.Sources.ListAll(r.Context())
		if err != nil {
			d.logErr("skills: list sources", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		for _, s := range srcs {
			bound[s.Name] = true
		}
	}

	out := make([]skillSummaryView, 0, len(skills))
	for _, s := range skills {
		out = append(out, skillSummaryView{
			Name:            s.Name,
			VersionCount:    s.VersionCount,
			LatestVersionID: s.LatestVersionID,
			LatestLabel:     s.LatestLabel,
			LatestKind:      string(s.LatestKind),
			SourceState:     deriveSourceState(bound[s.Name]),
			UpdatedAt:       s.UpdatedAt.UnixMilli(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

type createSkillRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// handleCreateSkill creates a brand-new skill. Two intake modes are
// selected by Content-Type: a JSON body publishes an initial minimal
// SKILL.md (the blank-skill case); an application/zip body imports a
// multi-file package (v1.0 §17 Phase 4 "可上传 zip"). Either way the
// skill "exists" once it has a version; a name collision is 409.
func (d Deps) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	if isZipContentType(r) {
		d.createSkillFromZip(w, r)
		return
	}
	if !hasJSONContentType(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"expected application/json or application/zip")
		return
	}
	req, ok := decodeJSON[createSkillRequest](w, r, 16*1024, skipContentTypeCheck())
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if !validSkillName(name) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"name must be 1-64 chars: letters, digits, '-', '_', '.'")
		return
	}

	exists, err := d.Registry.SkillExists(r.Context(), name)
	if err != nil {
		d.logErr("skills: exists check", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "already_exists", "a skill with that name already exists")
		return
	}

	v, err := d.Registry.PublishFromFiles(r.Context(),
		[]registry.InMemoryFile{{Path: "SKILL.md", Content: initialSkillMD(name, req.Description)}},
		registry.PublishParams{Name: name, Kind: registry.KindManual, VersionLabel: "initial"},
		d.Now(),
	)
	if err != nil {
		d.logErr("skills: create", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.created",
			Target: audit.Target{Type: "skill", ID: name},
			Detail: map[string]any{"version_id": v.ID},
		})
	}

	writeJSON(w, http.StatusCreated, skillDetailView{
		Name:        name,
		Versions:    []versionView{versionToView(v)},
		SourceState: sourceStateUnbound,
	})
}

// createSkillFromZip imports an uploaded zip as a new skill's first
// version (kind=import). The skill name comes from the ?name= query
// param when present, otherwise from the package's SKILL.md
// frontmatter (resolved by the manifest); either way it must pass the
// skill-name guard and not already exist.
func (d Deps) createSkillFromZip(w http.ResponseWriter, r *http.Request) {
	// Read the body under the package size cap. ReadAll into memory is
	// acceptable: the zip reader needs a ReaderAt, and the cap bounds it.
	body := http.MaxBytesReader(w, r.Body, skill.MaxPackageBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large",
			"zip exceeds the package size limit")
		return
	}

	files, err := skill.ImportZip(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		d.writeZipImportErr(w, err)
		return
	}

	inMem := make([]registry.InMemoryFile, 0, len(files))
	for _, f := range files {
		inMem = append(inMem, registry.InMemoryFile{Path: f.Path, Content: f.Content})
	}

	// Resolve the name: explicit query param wins; else derive from the
	// SKILL.md the import contains (a temp publish would over-engineer
	// it, so peek the SKILL.md directly).
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = nameFromImportedFiles(inMem)
	}
	if !validSkillName(name) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"could not determine a valid skill name; pass ?name= or include a SKILL.md with a name")
		return
	}

	exists, err := d.Registry.SkillExists(r.Context(), name)
	if err != nil {
		d.logErr("skills: zip exists check", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "already_exists", "a skill with that name already exists")
		return
	}

	v, err := d.Registry.PublishFromFiles(r.Context(), inMem,
		registry.PublishParams{Name: name, Kind: registry.KindImport, VersionLabel: "import"},
		d.Now(),
	)
	if err != nil {
		d.logErr("skills: zip import publish", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  d.sessionActor(r),
			Action: "skill.imported",
			Target: audit.Target{Type: "skill", ID: name},
			Detail: map[string]any{"version_id": v.ID, "file_count": v.Manifest.FileCount},
		})
	}
	writeJSON(w, http.StatusCreated, skillDetailView{
		Name:        name,
		Versions:    []versionView{versionToView(v)},
		SourceState: sourceStateUnbound,
	})
}

// nameFromImportedFiles peeks the SKILL.md (if any) to read its
// frontmatter name. Returns "" when there is no SKILL.md or it has no
// name; the caller then rejects with a helpful message.
func nameFromImportedFiles(files []registry.InMemoryFile) string {
	for _, f := range files {
		if f.Path == skill.SkillMDName {
			res, err := skillmd.Parse(f.Content, "")
			if err != nil {
				return ""
			}
			return strings.TrimSpace(res.Name)
		}
	}
	return ""
}

// writeZipImportErr maps skill.ImportZip errors to client responses.
func (d Deps) writeZipImportErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, skill.ErrZipEmpty):
		writeError(w, http.StatusBadRequest, "empty_zip", "zip contains no usable files")
	case errors.Is(err, skill.ErrZipTooManyFiles):
		writeError(w, http.StatusBadRequest, "too_many_files", "zip exceeds the file count limit")
	case errors.Is(err, skill.ErrZipFileTooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", err.Error())
	case errors.Is(err, skill.ErrZipTooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case isPathError(err):
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
	default:
		// A malformed zip or unsafe entry path lands here.
		writeError(w, http.StatusBadRequest, "bad_zip", "could not read zip: "+err.Error())
	}
}

// isZipContentType reports whether the request body is a zip upload.
func isZipContentType(r *http.Request) bool {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "application/zip") ||
		strings.HasPrefix(ct, "application/x-zip-compressed")
}

// handleGetSkill returns a skill's version history. The path id is the
// skill name (the registry keys skills by name, not a surrogate id).
func (d Deps) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	name := strings.TrimSpace(r.PathValue("id"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing skill id")
		return
	}
	versions, err := d.Registry.ListByName(r.Context(), name)
	if err != nil {
		d.logErr("skills: get", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if len(versions) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "skill not found")
		return
	}
	views := make([]versionView, 0, len(versions))
	for _, v := range versions {
		views = append(views, versionToView(v))
	}

	detail := skillDetailView{
		Name:        name,
		Versions:    views,
		SourceState: sourceStateUnbound,
	}
	// Attach the source binding if one exists. A missing Sources store or
	// an unbound skill both read back as source_state unbound with no
	// source object. upstream_state is deliberately omitted (see the
	// skillDetailView doc comment).
	if d.Sources != nil {
		src, err := d.Sources.GetBySkillName(r.Context(), name)
		switch {
		case err == nil:
			sv := sourceToView(src)
			detail.Source = &sv
			detail.SourceState = sourceStateBound
			if !src.LastCheckedAt.IsZero() {
				detail.LastCheckedAt = src.LastCheckedAt.UnixMilli()
			}
		case errors.Is(err, source.ErrNotFound):
			// unbound — leave the defaults.
		default:
			d.logErr("skills: get binding", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

func versionToView(v registry.Version) versionView {
	return versionView{
		ID:            v.ID,
		VersionLabel:  v.VersionLabel,
		Kind:          string(v.Kind),
		ContentSHA256: v.ContentSHA256,
		BaseVersionID: v.BaseVersionID,
		FileCount:     v.Manifest.FileCount,
		TotalBytes:    v.Manifest.TotalBytes,
		CreatedAt:     v.CreatedAt.UnixMilli(),
	}
}

// validSkillName enforces a conservative name charset so a skill name
// is always a safe single path segment (it doubles as a package dir
// name and a URL path value). 1-64 chars of [A-Za-z0-9._-], not "." or
// "..".
func validSkillName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	if name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

// initialSkillMD returns a minimal SKILL.md body for a new skill. The
// frontmatter name matches the skill so skillmd's name-vs-folder check
// passes and the manifest's Name resolves cleanly.
func initialSkillMD(name, description string) []byte {
	desc := strings.TrimSpace(description)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(name)
	b.WriteString("\n")
	if desc != "" {
		b.WriteString("description: ")
		b.WriteString(desc)
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(name)
	b.WriteString("\n\nDescribe what this skill does.\n")
	return []byte(b.String())
}

// hasJSONContentType reports whether the request declares a JSON body.
func hasJSONContentType(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

// sessionActor builds an audit actor from the request's session, if any.
func (d Deps) sessionActor(r *http.Request) audit.Actor {
	if sess, ok := SessionFromContext(r.Context()); ok {
		return audit.Actor{Type: "user", ID: sess.UserID}
	}
	return audit.Actor{Type: "user"}
}
