// Package registry is the server-side store for immutable Skill
// Package versions (v1.0 §7.3, §12 skill_versions). It is the only
// component that writes the skill_versions table and the only one that
// writes package archives into the server store.
//
// Immutability + content addressing (ADR-0008): publishing a package
// computes its manifest (skill.Generate) and a deterministic tar.gz
// (skill.Pack). The archive's sha256 names the file on disk
// (store/packages/<archiveSHA>.tgz); the manifest's tree
// content_sha256 is the logical identity recorded on the row. When a
// new publish has a content_sha256 already present for the same skill
// name, we reuse the existing version instead of writing a duplicate —
// editing that produces identical bytes is a no-op, not a new version.
//
// What this package does NOT do: it never mutates or deletes an
// existing version (drafts fork + publish anew), and it never installs
// to a device (Phase 8). It is a pure registry write/read surface.
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// packagesSubdir is the path under the store root where version
// archives live (v1.0 §16.1 store/packages/).
const packagesSubdir = "packages"

// VersionKind enumerates how a version came to exist. Mirrors the
// migration 0006 CHECK set.
type VersionKind string

const (
	KindManual       VersionKind = "manual"        // created empty / via API without a source
	KindImport       VersionKind = "import"        // uploaded zip (§6)
	KindDraftPublish VersionKind = "draft_publish" // published from a draft (§7.3)
	KindUpstream     VersionKind = "upstream"      // pulled from a bound source (§8)
	KindLocalEdit    VersionKind = "local_edit"    // captured from a device's local edit (§8)
	KindMerged       VersionKind = "merged"        // result of a three-way merge (§8)
)

func (k VersionKind) valid() bool {
	switch k {
	case KindManual, KindImport, KindDraftPublish, KindUpstream, KindLocalEdit, KindMerged:
		return true
	}
	return false
}

// Errors returned by the store.
var (
	ErrBadKind       = errors.New("registry: invalid version kind")
	ErrEmptyName     = errors.New("registry: version name is empty")
	ErrVersionNotFnd = errors.New("registry: version not found")
)

// Store writes and reads immutable skill versions. It owns the
// packages directory and the database handle.
type Store struct {
	db          *sql.DB
	packagesDir string
}

// New returns a Store rooted at storeDir (the server's resolved
// DataDir/store). The packages subdirectory is created eagerly so the
// first publish doesn't race on mkdir.
func New(db *sql.DB, storeDir string) (*Store, error) {
	pkgDir := filepath.Join(storeDir, packagesSubdir)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return nil, fmt.Errorf("registry: mkdir packages: %w", err)
	}
	return &Store{db: db, packagesDir: pkgDir}, nil
}

// Version is the in-memory projection of a skill_versions row.
type Version struct {
	ID            string
	SourceID      string
	Name          string
	VersionLabel  string
	Kind          VersionKind
	BaseVersionID string
	ContentSHA256 string
	Manifest      skill.Manifest
	PackagePath   string // relative to the store root, e.g. "packages/<sha>.tgz"
	CreatedAt     time.Time
}

// PublishParams carries the optional provenance for a publish. Name is
// required; everything else may be zero.
type PublishParams struct {
	Name          string
	VersionLabel  string
	Kind          VersionKind
	SourceID      string
	BaseVersionID string
	SourceCommit  string
	SourceTag     string
	SourceRelease string
}

// PublishFromDir packs the package rooted at srcDir, writes its archive
// to the store (unless an identical one already exists), and inserts an
// immutable skill_versions row. If a version with the same (name,
// content_sha256) already exists it is returned as-is and no new row or
// file is written — publishing identical content is idempotent.
//
// now is injected so callers (and tests) control the timestamp.
func (s *Store) PublishFromDir(ctx context.Context, srcDir string, p PublishParams, now time.Time) (Version, error) {
	if p.Name == "" {
		return Version{}, ErrEmptyName
	}
	if !p.Kind.valid() {
		return Version{}, fmt.Errorf("%w: %q", ErrBadKind, p.Kind)
	}

	manifest, err := skill.Generate(srcDir)
	if err != nil {
		return Version{}, fmt.Errorf("registry: manifest: %w", err)
	}

	// Dedup: identical content under the same name reuses the version.
	if existing, ok, err := s.findByContent(ctx, p.Name, manifest.ContentSHA256); err != nil {
		return Version{}, err
	} else if ok {
		return existing, nil
	}

	// Pack to a temp file first, then rename into place keyed by the
	// archive sha — so a crash mid-write never leaves a half archive
	// under its final (content-trusted) name.
	relPath, err := s.packToStore(srcDir)
	if err != nil {
		return Version{}, err
	}

	manifestJSON, err := manifest.Marshal()
	if err != nil {
		return Version{}, fmt.Errorf("registry: marshal manifest: %w", err)
	}

	v := Version{
		ID:            idgen.New("sv"),
		SourceID:      p.SourceID,
		Name:          p.Name,
		VersionLabel:  p.VersionLabel,
		Kind:          p.Kind,
		BaseVersionID: p.BaseVersionID,
		ContentSHA256: manifest.ContentSHA256,
		Manifest:      manifest,
		PackagePath:   relPath,
		CreatedAt:     now,
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_versions(
			id, source_id, name, version_label, version_kind,
			source_commit, source_tag, source_release, base_version_id,
			content_sha256, manifest_json, package_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		v.ID, nullable(v.SourceID), v.Name, nullable(v.VersionLabel), string(v.Kind),
		nullable(p.SourceCommit), nullable(p.SourceTag), nullable(p.SourceRelease), nullable(v.BaseVersionID),
		v.ContentSHA256, string(manifestJSON), v.PackagePath, now.UnixMilli(),
	); err != nil {
		// Best-effort cleanup of the archive we just wrote; the row is
		// the source of truth, so a dangling file is GC's problem, not
		// a correctness one. We only remove if no other row references
		// it (archiveSHA-named files are shared only by identical
		// content, which dedup already caught above for this name).
		_ = os.Remove(filepath.Join(s.packagesDir, filepath.Base(relPath)))
		return Version{}, fmt.Errorf("registry: insert version: %w", err)
	}

	return v, nil
}

// packToStore packs srcDir to a temp file in the packages dir, then
// renames it to "<archiveSHA>.tgz". Returns the store-relative path.
// If the destination already exists (same archive bytes from a prior
// publish of identical content under another name) the temp file is
// discarded and the existing file reused.
func (s *Store) packToStore(srcDir string) (relPath string, err error) {
	tmp, err := os.CreateTemp(s.packagesDir, ".pack-*.tmp")
	if err != nil {
		return "", fmt.Errorf("registry: temp archive: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// If we didn't rename it away, clean up.
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	info, packErr := skill.Pack(srcDir, tmp)
	closeErr := tmp.Close()
	if packErr != nil {
		return "", fmt.Errorf("registry: pack: %w", packErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("registry: close archive: %w", closeErr)
	}

	final := filepath.Join(s.packagesDir, info.SHA256+".tgz")
	rel := filepath.ToSlash(filepath.Join(packagesSubdir, info.SHA256+".tgz"))

	// If the archive already exists, the bytes are identical (sha-named),
	// so reuse it. Otherwise rename our temp into place.
	if _, statErr := os.Stat(final); statErr == nil {
		return rel, nil
	}
	if err := os.Rename(tmpName, final); err != nil {
		return "", fmt.Errorf("registry: place archive: %w", err)
	}
	return rel, nil
}

// findByContent returns the existing version for (name, contentSHA) if
// one exists. Used for publish idempotency.
func (s *Store) findByContent(ctx context.Context, name, contentSHA string) (Version, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, source_id, name, version_label, version_kind, base_version_id,
		       content_sha256, manifest_json, package_path, created_at
		FROM skill_versions
		WHERE name = ? AND content_sha256 = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, name, contentSHA)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// Get loads a single version by id.
func (s *Store) Get(ctx context.Context, id string) (Version, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, source_id, name, version_label, version_kind, base_version_id,
		       content_sha256, manifest_json, package_path, created_at
		FROM skill_versions WHERE id = ?
	`, id)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrVersionNotFnd
	}
	return v, err
}

// ListByName returns all versions of a skill name, newest first.
func (s *Store) ListByName(ctx context.Context, name string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_id, name, version_label, version_kind, base_version_id,
		       content_sha256, manifest_json, package_path, created_at
		FROM skill_versions WHERE name = ?
		ORDER BY created_at DESC
	`, name)
	if err != nil {
		return nil, fmt.Errorf("registry: list versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LatestVersionBySource returns the most recent version produced from the
// given source binding with the given kind, newest first. found is false
// when the source has no version of that kind yet.
//
// The update-check engine (phase 6 t5) calls this with KindUpstream to
// find the baseline content_sha256 a fresh fetch is compared against —
// i.e. "the last upstream snapshot we recorded". Comparing a freshly
// fetched content_sha256 against THIS, rather than against the remote
// commit, is the core acceptance guard: a repo whose commit moved but
// whose skill subtree is byte-identical must not be reported as an update.
//
// rowid is the tie-breaker so two versions sharing a created_at resolve to
// the last-inserted one deterministically (created_at is millisecond
// granularity; a fast test or scheduler can mint two in the same ms).
func (s *Store) LatestVersionBySource(ctx context.Context, sourceID string, kind VersionKind) (Version, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, source_id, name, version_label, version_kind, base_version_id,
		       content_sha256, manifest_json, package_path, created_at
		FROM skill_versions
		WHERE source_id = ? AND version_kind = ?
		ORDER BY created_at DESC, rowid DESC
		LIMIT 1
	`, sourceID, string(kind))
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// ArchivePath returns the absolute path to a version's package archive,
// for serving downloads / agent pulls (Phase 8).
func (s *Store) ArchivePath(v Version) string {
	return filepath.Join(s.packagesDir, filepath.Base(v.PackagePath))
}

// scanner abstracts *sql.Row and *sql.Rows for scanVersion.
type scanner interface {
	Scan(dest ...any) error
}

func scanVersion(sc scanner) (Version, error) {
	var (
		v            Version
		sourceID     sql.NullString
		versionLabel sql.NullString
		baseVersion  sql.NullString
		manifestJSON string
		kind         string
		createdMS    int64
	)
	if err := sc.Scan(
		&v.ID, &sourceID, &v.Name, &versionLabel, &kind, &baseVersion,
		&v.ContentSHA256, &manifestJSON, &v.PackagePath, &createdMS,
	); err != nil {
		return Version{}, err
	}
	v.SourceID = sourceID.String
	v.VersionLabel = versionLabel.String
	v.BaseVersionID = baseVersion.String
	v.Kind = VersionKind(kind)
	v.CreatedAt = time.UnixMilli(createdMS)

	m, err := skill.Unmarshal([]byte(manifestJSON))
	if err != nil {
		return Version{}, fmt.Errorf("registry: decode manifest for %s: %w", v.ID, err)
	}
	v.Manifest = m
	return v, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
