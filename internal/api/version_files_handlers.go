package api

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/safefs"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// fileTreeEntry is one node of a version's file tree (v1.0 §7.2). It
// comes straight from the manifest, so no archive unpack is needed to
// render the tree — only reading a file's bytes (handleVersionFile)
// touches the archive.
type fileTreeEntry struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Exec     bool   `json:"exec"`
	Binary   bool   `json:"binary"`
	Editable bool   `json:"editable"` // text AND within the size limit
}

// handleVersionFiles returns a version's file tree from its manifest.
func (d Deps) handleVersionFiles(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	v, ok := d.lookupVersion(w, r)
	if !ok {
		return
	}

	entries := make([]fileTreeEntry, 0, len(v.Manifest.Files))
	for _, f := range v.Manifest.Files {
		entries = append(entries, fileTreeEntry{
			Path:     f.Path,
			SHA256:   f.SHA256,
			Size:     f.Size,
			Exec:     f.Exec,
			Binary:   f.Binary,
			Editable: !f.Binary && f.Size <= skill.MaxEditableBytes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version_id":     v.ID,
		"name":           v.Name,
		"content_sha256": v.ContentSHA256,
		"files":          entries,
	})
}

// fileContentView carries a single file's metadata plus, when the file
// is editable text, its content. Binary or oversized files return
// editable=false and omit content (the WebUI shows a download view).
type fileContentView struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Exec     bool   `json:"exec"`
	Binary   bool   `json:"binary"`
	Editable bool   `json:"editable"`
	Encoding string `json:"encoding"`          // "utf-8" for text, "binary" otherwise
	Content  string `json:"content,omitempty"` // present only when editable
}

// handleVersionFile returns one file from a version. The trailing
// {path...} segment is the package-relative path. Text files within
// the editable size limit return their content inline; binary or
// oversized files return metadata only.
func (d Deps) handleVersionFile(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistry(w) {
		return
	}
	v, ok := d.lookupVersion(w, r)
	if !ok {
		return
	}

	rawPath := r.PathValue("path")
	clean, err := safefs.CleanPackagePath(rawPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}

	// Find the file's metadata in the manifest first — cheaper than
	// unpacking, and it tells us whether to bother reading content.
	var meta *skill.File
	for i := range v.Manifest.Files {
		if v.Manifest.Files[i].Path == clean {
			meta = &v.Manifest.Files[i]
			break
		}
	}
	if meta == nil {
		writeError(w, http.StatusNotFound, "not_found", "file not in package")
		return
	}

	view := fileContentView{
		Path:     meta.Path,
		SHA256:   meta.SHA256,
		Size:     meta.Size,
		Exec:     meta.Exec,
		Binary:   meta.Binary,
		Editable: !meta.Binary && meta.Size <= skill.MaxEditableBytes,
		Encoding: "binary",
	}
	if !view.Editable {
		// Binary or oversized: metadata only, no content body.
		writeJSON(w, http.StatusOK, view)
		return
	}

	content, err := d.Registry.ReadVersionFile(r.Context(), v, clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Manifest and archive disagree — shouldn't happen, but
			// don't 500 a missing entry.
			writeError(w, http.StatusNotFound, "not_found", "file not in package")
			return
		}
		d.logErr("version file: read", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Defensive: the manifest said text, but re-validate before
	// declaring UTF-8 so we never hand the client a mojibake string.
	if !utf8.Valid(content) {
		view.Editable = false
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.Encoding = "utf-8"
	view.Content = string(content)
	writeJSON(w, http.StatusOK, view)
}

// lookupVersion resolves the {id} path value to a registry Version,
// writing the appropriate error response and returning ok=false on
// failure.
func (d Deps) lookupVersion(w http.ResponseWriter, r *http.Request) (registry.Version, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing version id")
		return registry.Version{}, false
	}
	v, err := d.Registry.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrVersionNotFnd) {
			writeError(w, http.StatusNotFound, "not_found", "version not found")
			return registry.Version{}, false
		}
		d.logErr("version lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return registry.Version{}, false
	}
	return v, true
}
