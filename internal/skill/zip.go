// Package skill — zip.go imports a zip archive into the in-memory file
// set the registry publishes from (v1.0 §17 Phase 4 "可上传 zip", §6
// upload limits). zip is an import/export boundary format only; the
// internal storage format remains deterministic tar.gz (ADR-0008).
//
// Security mirrors Unpack: every entry name is validated through
// safefs.CleanPackagePath, non-regular entries are skipped, and the
// per-file (20 MiB) + total (100 MiB) caps from v1.0 §6 are enforced
// against zip bombs. A common single top-level directory (the usual
// "skill-name/..." wrapper GitHub/zip tools add) is stripped so the
// SKILL.md lands at the package root.
package skill

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yeluonight/skillfleet/internal/safefs"
)

// Upload limits for imported zips (v1.0 §6).
const (
	MaxUploadFileBytes = 20 << 20  // 20 MiB per file
	MaxPackageBytes    = 100 << 20 // 100 MiB per package
	MaxUploadFileCount = 10000     // zip-bomb backstop
)

// ZipFile is one extracted entry: a package-relative path and its bytes.
type ZipFile struct {
	Path    string
	Content []byte
}

// Errors returned by ImportZip.
var (
	ErrZipTooManyFiles = errors.New("skill: zip exceeds file count limit")
	ErrZipTooBig       = errors.New("skill: zip exceeds total size limit")
	ErrZipFileTooBig   = errors.New("skill: zip entry exceeds file size limit")
	ErrZipEmpty        = errors.New("skill: zip contains no usable files")
)

// ImportZip reads a zip from r (with total compressed size n) and
// returns its files as an in-memory set, ready for
// registry.PublishFromFiles. Directory entries and anything that isn't
// a regular file are skipped. If every file shares one top-level
// directory, that prefix is stripped so package paths are root-relative.
func ImportZip(r io.ReaderAt, size int64) ([]ZipFile, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("skill: open zip: %w", err)
	}

	// First pass: collect regular-file names (forward-slash already in
	// zip spec) to detect a common top-level directory.
	var names []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		names = append(names, f.Name)
	}
	if len(names) == 0 {
		return nil, ErrZipEmpty
	}
	prefix := commonTopDir(names)

	var (
		out   []ZipFile
		total int64
		count int
	)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Mode must be a regular file; zip stores unix mode in the
		// upper bits of ExternalAttrs via FileInfo().Mode().
		if !f.FileInfo().Mode().IsRegular() {
			continue
		}

		name := strings.TrimPrefix(f.Name, prefix)
		clean, err := safefs.CleanPackagePath(name)
		if err != nil {
			return nil, fmt.Errorf("skill: unsafe zip path %q: %w", f.Name, err)
		}

		count++
		if count > MaxUploadFileCount {
			return nil, ErrZipTooManyFiles
		}
		if int64(f.UncompressedSize64) > MaxUploadFileBytes {
			return nil, fmt.Errorf("%w: %s (%d bytes)", ErrZipFileTooBig, clean, f.UncompressedSize64)
		}

		content, err := readZipEntry(f)
		if err != nil {
			return nil, err
		}
		total += int64(len(content))
		if total > MaxPackageBytes {
			return nil, fmt.Errorf("%w: after %s", ErrZipTooBig, clean)
		}
		out = append(out, ZipFile{Path: clean, Content: content})
	}
	if len(out) == 0 {
		return nil, ErrZipEmpty
	}
	return out, nil
}

// readZipEntry decompresses one entry, guarding against a header that
// lies about its size (claims small, streams large) via a hard byte cap.
func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("skill: open zip entry %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	// Read at most MaxUploadFileBytes+1 so an oversized lie is caught.
	content, err := io.ReadAll(io.LimitReader(rc, MaxUploadFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("skill: read zip entry %s: %w", f.Name, err)
	}
	if int64(len(content)) > MaxUploadFileBytes {
		return nil, fmt.Errorf("%w: %s", ErrZipFileTooBig, f.Name)
	}
	return content, nil
}

// commonTopDir returns the single shared top-level directory prefix
// (including its trailing slash) if every name has one, else "". E.g.
// ["deploy/SKILL.md","deploy/x.sh"] -> "deploy/"; a SKILL.md at root or
// mixed tops -> "".
//
// A "." or ".." top segment is never strippable: stripping it would
// silently neutralise a path escape into a benign-looking name instead
// of letting safefs reject it. Returning "" here keeps the raw escape
// intact so CleanPackagePath fails it loudly.
func commonTopDir(names []string) string {
	var top string
	for i, n := range names {
		slash := strings.IndexByte(n, '/')
		if slash < 0 {
			return "" // a root-level file: no common dir to strip.
		}
		seg := n[:slash]
		if seg == "." || seg == ".." {
			return "" // never strip a traversal segment.
		}
		dir := n[:slash+1]
		if i == 0 {
			top = dir
		} else if dir != top {
			return ""
		}
	}
	return top
}
