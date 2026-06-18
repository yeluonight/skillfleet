// /agent/packages/{id}: serve a version's package archive to an
// authenticated agent for download (v1.0 §14.2). {id} is a version id —
// a database key, not a filesystem path, so there is no traversal risk
// in the path parameter; the PackageSource resolves it to an archive on
// disk. The HMAC middleware has already confirmed the device is enrolled
// and approved, which is the authorisation bar for pulling a package.

package agentapi

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
)

// zeroTime is passed to http.ServeContent so it skips Last-Modified
// handling (the archive's logical mtime lives in the DB, not the file).
var zeroTime time.Time

// PackageSource resolves a version id to its archive on disk. The server
// wires an adapter over *registry.Store; defining it as an interface
// keeps agentapi from importing registry and makes the handler testable
// with a fake.
type PackageSource interface {
	// ArchiveForVersion returns an open, seekable handle to the version's
	// package archive plus its size, or an error. ErrPackageNotFound (or
	// any error the handler maps to 404) signals an unknown version.
	ArchiveForVersion(versionID string) (f *os.File, size int64, err error)
}

// ErrPackageNotFound is returned by a PackageSource when the version id
// is unknown or has no archive. The handler maps it to 404.
var ErrPackageNotFound = errors.New("agentapi: package not found")

// handleGetPackage streams a version's archive to the agent. It uses
// http.ServeContent so range requests / conditional headers work, and
// sets an explicit content type for the .tgz. A missing package is 404;
// any other resolve error is 500.
func (d Deps) handleGetPackage(w http.ResponseWriter, r *http.Request) {
	ac, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing auth context")
		return
	}
	if d.Packages == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "package serving not configured")
		return
	}
	versionID := r.PathValue("id")
	if versionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing version id")
		return
	}

	f, size, err := d.Packages.ArchiveForVersion(versionID)
	if err != nil {
		if errors.Is(err, ErrPackageNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such package")
			return
		}
		d.logErr("agent packages: resolve", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	defer func() { _ = f.Close() }()

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "device", ID: ac.DeviceID},
			Action: "deployment.package_served",
			Target: audit.Target{Type: "skill_version", ID: versionID},
		})
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// stat for ModTime is unnecessary; pass a zero time so ServeContent
	// skips Last-Modified handling but still serves the bytes + length.
	http.ServeContent(w, r, versionID+".tgz", zeroTime, f)
	_ = size // size is implied by ServeContent via Seek; kept for clarity
}
