// Package skill builds and manipulates the on-disk representation of a
// multi-file Skill Package — the unit the central Registry versions
// and the WebUI edits (v1.0 §7.1). This file defines the manifest: the
// canonical, JSON-serialisable description of one package's contents.
//
// A manifest is the bridge between a directory tree and a database
// row. skill_versions.manifest_json (v1.0 §12) stores exactly the
// Marshal output of a Manifest, and skill_versions.content_sha256
// stores Manifest.ContentSHA256. The manifest records every file's
// path, hash, size, exec bit, and a text/binary classification, plus
// the parsed SKILL.md metadata (name, description) so the Registry can
// list and search packages without re-reading disk.
//
// Determinism: a Manifest is built from fingerprint.Compute, whose
// file list is sorted by forward-slash path, so two builds of the same
// content produce byte-identical Marshal output (and the same
// ContentSHA256). This is what lets the Registry deduplicate versions
// by content hash (ADR-0008).
package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/yeluonight/skillfleet/internal/fingerprint"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// ManifestSchemaVersion is bumped when the Manifest JSON shape changes
// in a backward-incompatible way. Stored alongside the data so a
// future reader can detect and migrate old rows.
const ManifestSchemaVersion = 1

// SkillMDName is the conventional entry file every skill package is
// expected to carry (v1.0 §7.1). Its absence is a warning, not an
// error: drafts may be in-progress and import sources may be partial.
const SkillMDName = "SKILL.md"

// MaxEditableBytes is the largest file the WebUI will serve inline for
// in-browser editing (v1.0 §6). Larger text files and all binary files
// are download/replace-only; the API returns their metadata with the
// content withheld so the client shows a non-editable view.
const MaxEditableBytes = 1 << 20 // 1 MiB

// sniffLen is how many leading bytes classifyContent inspects to
// decide text-vs-binary. 512 is the long-standing convention
// (net/http.DetectContentType uses the same window) and is plenty to
// catch a NUL byte or an invalid UTF-8 lead.
const sniffLen = 512

// Errors returned by Generate.
var (
	ErrRootMissing = errors.New("skill: package root does not exist")
	ErrRootNotDir  = errors.New("skill: package root is not a directory")
)

// File is one entry in a manifest. Field order and json tags are
// fixed; do not reorder without bumping ManifestSchemaVersion.
type File struct {
	// Path is the forward-slash package-relative path (e.g.
	// "scripts/deploy.py"). Guaranteed clean by construction:
	// fingerprint skips anything with a tab/newline and the tree is
	// rooted, so paths never escape.
	Path string `json:"path"`
	// SHA256 is the lowercase hex sha256 of the file's bytes.
	SHA256 string `json:"sha256"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Exec reports whether the owner-executable bit is set. The
	// archive layer (ADR-0008) collapses permissions to 0644/0755
	// based on this single bit.
	Exec bool `json:"exec"`
	// Binary reports whether the leading bytes failed a UTF-8 / NUL
	// check. The WebUI uses this to show a download-only view instead
	// of the text editor (v1.0 §7.9).
	Binary bool `json:"binary"`
}

// Manifest is the canonical description of a Skill Package's contents.
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	// Description is SKILL.md frontmatter.description, or "" when the
	// package has no SKILL.md or it omits the field.
	Description string `json:"description,omitempty"`
	// HasSkillMD records whether a SKILL.md exists at the package root.
	HasSkillMD bool `json:"has_skill_md"`
	// ContentSHA256 is fingerprint.Compute's rollup hash over the
	// whole tree — the package's content identity (v1.0 §8.2).
	ContentSHA256 string `json:"content_sha256"`
	FileCount     int    `json:"file_count"`
	TotalBytes    int64  `json:"total_bytes"`
	// Files is sorted by Path (inherited from fingerprint), making the
	// manifest deterministic.
	Files []File `json:"files"`
	// Warnings carries non-fatal findings (missing SKILL.md, bad
	// frontmatter, non-UTF-8 SKILL.md) for the operator. Ordered as
	// discovered.
	Warnings []skillmd.Warning `json:"warnings,omitempty"`
}

// Generate walks the package directory at root and produces its
// Manifest. The skill name resolves from SKILL.md frontmatter.name
// when present, falling back to the root directory's base name so a
// nameless or SKILL.md-less package still gets a stable identifier.
//
// File content limits are inherited from fingerprint (10 MiB/file,
// 100 MiB/tree); exceeding them is a hard error, not a warning,
// because a manifest that silently dropped files would misrepresent
// the package.
func Generate(root string) (Manifest, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, fmt.Errorf("%w: %s", ErrRootMissing, root)
		}
		return Manifest{}, fmt.Errorf("skill: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return Manifest{}, fmt.Errorf("%w: %s", ErrRootNotDir, root)
	}

	fp, err := fingerprint.Compute(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("skill: fingerprint %s: %w", root, err)
	}

	m := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ContentSHA256: fp.Hash,
		FileCount:     fp.FileCount,
		TotalBytes:    fp.TotalBytes,
		Files:         make([]File, 0, len(fp.Files)),
		Name:          filepath.Base(root),
	}

	for _, fe := range fp.Files {
		binary, err := classifyFile(filepath.Join(root, filepath.FromSlash(fe.Path)))
		if err != nil {
			return Manifest{}, fmt.Errorf("skill: classify %s: %w", fe.Path, err)
		}
		m.Files = append(m.Files, File{
			Path:   fe.Path,
			SHA256: fe.Hash,
			Size:   fe.Size,
			Exec:   fe.Exec,
			Binary: binary,
		})
		if fe.Path == SkillMDName {
			m.HasSkillMD = true
		}
	}

	// Parse SKILL.md for name/description metadata. Its absence or a
	// bad frontmatter is a warning, never fatal — the file list above
	// is the source of truth for content.
	if m.HasSkillMD {
		res, perr := skillmd.ParseFile(filepath.Join(root, SkillMDName))
		switch {
		case perr != nil:
			m.Warnings = append(m.Warnings, skillmd.Warning{
				Code:    "skill_md_parse_failed",
				Message: perr.Error(),
			})
		default:
			if res.Name != "" {
				m.Name = res.Name
			}
			m.Description = res.Description
			m.Warnings = append(m.Warnings, res.Warnings...)
		}
	} else {
		m.Warnings = append(m.Warnings, skillmd.Warning{
			Code:    "missing_skill_md",
			Message: "package has no SKILL.md at its root",
		})
	}

	return m, nil
}

// Marshal returns the canonical JSON encoding of the manifest. Because
// Files is already sorted and Go's encoding/json emits struct fields
// in declaration order, the output is deterministic for a given
// Manifest value — suitable for storing in manifest_json and for
// equality checks in tests.
func (m Manifest) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// Unmarshal parses a manifest_json blob back into a Manifest.
func Unmarshal(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("skill: unmarshal manifest: %w", err)
	}
	return m, nil
}

// classifyFile reports whether the file at path looks binary. It reads
// only the leading sniffLen bytes: a NUL byte, or a leading-byte
// sequence that is not valid UTF-8, marks the file binary. An empty
// file is treated as text (it is safely editable).
func classifyFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, sniffLen)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return looksBinary(buf[:n]), nil
}

// IsBinaryContent reports whether an in-memory byte slice looks binary,
// applying the same heuristic Generate uses for on-disk files: a NUL
// byte or invalid UTF-8 in the leading sniff window marks it binary.
// Exposed so the draft / import layers classify uploaded bytes with
// the identical rule.
func IsBinaryContent(content []byte) bool {
	sample := content
	if len(sample) > sniffLen {
		sample = sample[:sniffLen]
	}
	return looksBinary(sample)
}

// looksBinary applies the text/binary heuristic to a byte sample.
// Exposed-as-unexported so the rule is unit-tested directly without
// touching the filesystem.
func looksBinary(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	for _, b := range sample {
		if b == 0x00 {
			return true // NUL is the strongest binary signal.
		}
	}
	// If the sample is the full sniff window we may have split a
	// multibyte rune at the boundary; utf8.Valid would false-positive
	// on the trailing fragment. Only treat invalid UTF-8 as binary
	// when the sample is short enough to be the whole file, or trim
	// the trailing partial rune first.
	trimmed := sample
	if len(sample) == sniffLen {
		trimmed = trimPartialRune(sample)
	}
	return !utf8.Valid(trimmed)
}

// trimPartialRune drops a trailing byte sequence that could be the
// front of a multibyte rune cut off by the sniff window, so a valid
// UTF-8 file isn't misclassified because we stopped reading mid-rune.
func trimPartialRune(b []byte) []byte {
	// At most 3 trailing bytes can form an incomplete 4-byte rune.
	for i := 0; i < 3 && i < len(b); i++ {
		j := len(b) - 1 - i
		c := b[j]
		if c < 0x80 {
			break // ASCII byte: nothing partial after it.
		}
		if c&0xC0 == 0xC0 {
			// Lead byte of a multibyte rune: if the remaining bytes
			// don't complete it, drop from here.
			return b[:j]
		}
	}
	return b
}
