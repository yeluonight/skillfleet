package registry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yeluonight/skillfleet/internal/safefs"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// InMemoryFile is one file of a package supplied as bytes rather than
// an on-disk tree — the shape the API layer works in (draft files, a
// freshly-created skill's SKILL.md, an unpacked zip held in memory).
type InMemoryFile struct {
	Path    string // package-relative; validated via safefs
	Content []byte
}

// PublishFromFiles materialises files into a temp directory (each path
// re-validated through safefs so nothing escapes), then publishes that
// directory via PublishFromDir. It is the common path for creating a
// skill from API input that never touched disk as a tree.
//
// An empty file set is allowed (it produces a package with no files,
// e.g. a brand-new skill before its SKILL.md is written) — though
// callers usually include at least a SKILL.md.
func (s *Store) PublishFromFiles(ctx context.Context, files []InMemoryFile, p PublishParams, now time.Time) (Version, error) {
	if p.Name == "" {
		return Version{}, ErrEmptyName
	}
	if !p.Kind.valid() {
		return Version{}, fmt.Errorf("%w: %q", ErrBadKind, p.Kind)
	}

	tmp, err := os.MkdirTemp("", "sf-publish-*")
	if err != nil {
		return Version{}, fmt.Errorf("registry: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	for _, f := range files {
		clean, err := safefs.CleanPackagePath(f.Path)
		if err != nil {
			return Version{}, fmt.Errorf("registry: file path %q: %w", f.Path, err)
		}
		dest := filepath.Join(tmp, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return Version{}, fmt.Errorf("registry: mkdir for %s: %w", clean, err)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			return Version{}, fmt.Errorf("registry: write %s: %w", clean, err)
		}
	}

	return s.PublishFromDir(ctx, tmp, p, now)
}

// SkillSummary aggregates all versions sharing a name into the unit the
// Registry list renders (v1.0 §13.3). There is no separate "skills"
// table — a skill *is* its set of versions, keyed by name.
type SkillSummary struct {
	Name             string
	VersionCount     int
	LatestVersionID  string
	LatestLabel      string
	LatestKind       VersionKind
	LatestContentSHA string
	UpdatedAt        time.Time // newest version's created_at
}

// ListSkills returns one SkillSummary per distinct version name,
// ordered by most-recently-updated first. This is a GROUP BY over
// skill_versions; the "latest" columns come from the newest row in
// each name group.
func (s *Store) ListSkills(ctx context.Context) ([]SkillSummary, error) {
	// SQLite's bare-column + MAX() idiom: selecting MAX(created_at)
	// alongside other bare columns returns the row holding the max
	// (documented SQLite behaviour for min/max aggregates).
	rows, err := s.db.QueryContext(ctx, `
		SELECT name,
		       COUNT(*)            AS version_count,
		       id,
		       version_label,
		       version_kind,
		       content_sha256,
		       MAX(created_at)     AS latest_at
		FROM skill_versions
		GROUP BY name
		ORDER BY latest_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("registry: list skills: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SkillSummary
	for rows.Next() {
		var (
			sum      SkillSummary
			label    *string
			kind     string
			latestMS int64
		)
		if err := rows.Scan(&sum.Name, &sum.VersionCount, &sum.LatestVersionID,
			&label, &kind, &sum.LatestContentSHA, &latestMS); err != nil {
			return nil, err
		}
		if label != nil {
			sum.LatestLabel = *label
		}
		sum.LatestKind = VersionKind(kind)
		sum.UpdatedAt = time.UnixMilli(latestMS)
		out = append(out, sum)
	}
	return out, rows.Err()
}

// SkillExists reports whether any version with the given name exists.
func (s *Store) SkillExists(ctx context.Context, name string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM skill_versions WHERE name = ?`, name,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("registry: skill exists: %w", err)
	}
	return n > 0, nil
}

// ReadVersionFiles extracts a version's archive into memory and returns
// its files, sorted by path. Used by the file-tree / file-read API
// (t7). Binary classification lives in the manifest, so callers pair
// these bytes with Version.Manifest.Files for metadata.
func (s *Store) ReadVersionFiles(ctx context.Context, v Version) ([]InMemoryFile, error) {
	abs := s.ArchivePath(v)
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("registry: open archive %s: %w", v.ID, err)
	}
	defer func() { _ = f.Close() }()

	tmp, err := os.MkdirTemp("", "sf-read-*")
	if err != nil {
		return nil, fmt.Errorf("registry: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	paths, err := skill.Unpack(f, tmp)
	if err != nil {
		return nil, fmt.Errorf("registry: unpack %s: %w", v.ID, err)
	}

	out := make([]InMemoryFile, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("registry: read %s: %w", p, err)
		}
		out = append(out, InMemoryFile{Path: p, Content: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ReadVersionFile extracts a single file from a version's archive by
// package-relative path. Returns os.ErrNotExist if the path isn't in
// the package.
func (s *Store) ReadVersionFile(ctx context.Context, v Version, path string) ([]byte, error) {
	clean, err := safefs.CleanPackagePath(path)
	if err != nil {
		return nil, err
	}
	files, err := s.ReadVersionFiles(ctx, v)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Path == clean {
			return f.Content, nil
		}
	}
	return nil, os.ErrNotExist
}
