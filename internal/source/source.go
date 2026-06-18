// Package source is the store for SkillFleet source bindings (v1.0 §6
// skill_sources). It is the only component that writes the skill_sources
// table and the only one that reads it for the web UI and the update-check
// engine.
//
// What this package does NOT do: it never fetches from the network
// (network fetching is owned by a separate fetch package). It is a pure
// database read/write surface — it persists source metadata and exposes
// CRUD operations, but it has no knowledge of git remotes, HTTP, or any
// other I/O outside the SQLite handle passed to it.
//
// Conventions (inherited from internal/registry):
//   - All timestamps are millisecond Unix epochs stored as INTEGER.
//   - Callers inject time.Time; the store converts to/from ms internally.
//   - Application-level IDs are minted via internal/idgen.
//   - Strict parameterised queries; zero string concatenation of SQL.
package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

// SourceType enumerates how a skill source came to be bound. Mirrors the
// migration 0007 CHECK set exactly.
type SourceType string

const (
	TypeWebUICreated    SourceType = "webui_created"
	TypeLocalImport     SourceType = "local_import"
	TypeDeviceImport    SourceType = "device_import"
	TypeGitRepo         SourceType = "git_repo"
	TypeGitHubRepo      SourceType = "github_repo"
	TypeGitHubRelease   SourceType = "github_release"
	TypeZipUpload       SourceType = "zip_upload"
	TypeUnknownExternal SourceType = "unknown_external"
)

func (t SourceType) valid() bool {
	switch t {
	case TypeWebUICreated, TypeLocalImport, TypeDeviceImport,
		TypeGitRepo, TypeGitHubRepo, TypeGitHubRelease,
		TypeZipUpload, TypeUnknownExternal:
		return true
	}
	return false
}

// RefType narrows what ref_name means (branch | tag | commit | release).
// The zero value is the empty string (NULL), meaning "no ref type
// specified".
type RefType string

const (
	RefBranch  RefType = "branch"
	RefTag     RefType = "tag"
	RefCommit  RefType = "commit"
	RefRelease RefType = "release"
)

// Errors returned by the store.
var (
	ErrNotFound      = errors.New("source: not found")
	ErrEmptyName     = errors.New("source: name is empty")
	ErrBadType       = errors.New("source: invalid source type")
	ErrZeroCheckTime = errors.New("source: check time is zero")
)

// Source is the in-memory projection of a skill_sources row.
type Source struct {
	ID               string
	Name             string
	Type             SourceType
	URL              string // source_url
	Provider         string
	Owner            string
	Repo             string
	RefType          RefType   // nullable
	RefName          string    // nullable
	Subdir           string    // nullable
	LastCheckedAt    time.Time // zero = never checked
	LastRemoteCommit string    // nullable
	ConfigJSON       string    // nullable, reserved extension slot
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Store writes and reads source bindings. It owns the database handle
// but no filesystem state (unlike the registry store, which also manages
// a packages directory).
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a new skill_sources row after validation. If s.ID is
// empty a new id is generated via idgen.New("src"). Name must be
// non-empty and Type must be a valid SourceType; otherwise the
// corresponding sentinel error is returned.
//
// now is injected so callers (and tests) control the timestamp.
func (s *Store) Create(ctx context.Context, src Source, now time.Time) (Source, error) {
	if src.Name == "" {
		return Source{}, ErrEmptyName
	}
	if !src.Type.valid() {
		return Source{}, fmt.Errorf("%w: %q", ErrBadType, src.Type)
	}

	if src.ID == "" {
		src.ID = idgen.New("src")
	}
	src.CreatedAt = now
	src.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_sources(
			id, name, source_type, source_url, provider, owner, repo,
			ref_type, ref_name, subdir, last_checked_at, last_remote_commit,
			config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		src.ID, src.Name, string(src.Type),
		nullable(src.URL), nullable(src.Provider), nullable(src.Owner), nullable(src.Repo),
		nullable(string(src.RefType)), nullable(src.RefName), nullable(src.Subdir),
		nullableTime(src.LastCheckedAt), nullable(src.LastRemoteCommit),
		nullable(src.ConfigJSON),
		now.UnixMilli(), now.UnixMilli(),
	)
	if err != nil {
		return Source{}, fmt.Errorf("source: insert: %w", err)
	}
	return src, nil
}

// Get loads a single source by id. Returns ErrNotFound when no row matches.
func (s *Store) Get(ctx context.Context, id string) (Source, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, source_type, source_url, provider, owner, repo,
		       ref_type, ref_name, subdir, last_checked_at, last_remote_commit,
		       config_json, created_at, updated_at
		FROM skill_sources WHERE id = ?
	`, id)
	src, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	return src, err
}

// GetBySkillName loads the source bound to a skill name. Phase 6 binds at
// most one source per skill (handleBindSource rejects a second bind while
// any version still carries a source_id), so in practice there is one row;
// if more than one ever exists this returns the newest by created_at.
// Returns ErrNotFound when the skill is unbound.
//
// Used by the read/check/detach handlers (t6) to resolve a skill name —
// the registry's primary key — to its binding row.
func (s *Store) GetBySkillName(ctx context.Context, name string) (Source, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, source_type, source_url, provider, owner, repo,
		       ref_type, ref_name, subdir, last_checked_at, last_remote_commit,
		       config_json, created_at, updated_at
		FROM skill_sources WHERE name = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, name)
	src, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	return src, err
}

// Update writes back the mutable fields of a source row, identified by
// src.ID. The name and type are not validated here (callers are expected
// to pass a Source they previously read or created). Returns ErrNotFound
// when no row with src.ID exists.
//
// now is injected so callers (and tests) control the timestamp.
func (s *Store) Update(ctx context.Context, src Source, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE skill_sources SET
			name = ?, source_url = ?, provider = ?, owner = ?, repo = ?,
			ref_type = ?, ref_name = ?, subdir = ?,
			last_checked_at = ?, last_remote_commit = ?,
			config_json = ?, updated_at = ?
		WHERE id = ?
	`,
		src.Name,
		nullable(src.URL), nullable(src.Provider), nullable(src.Owner), nullable(src.Repo),
		nullable(string(src.RefType)), nullable(src.RefName), nullable(src.Subdir),
		nullableTime(src.LastCheckedAt), nullable(src.LastRemoteCommit),
		nullable(src.ConfigJSON),
		now.UnixMilli(),
		src.ID,
	)
	if err != nil {
		return fmt.Errorf("source: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("source: update rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateCheckCursor is a lightweight update that only touches the
// last-checked cursor columns. It is designed for the update-check
// engine (t5), which calls it frequently and should avoid rewriting
// every column. Returns ErrNotFound when no row matches id.
//
// checkedAt is the moment the check completed and must be non-zero — it
// is written to both last_checked_at and the NOT NULL updated_at. A zero
// checkedAt is a caller contract violation and returns ErrZeroCheckTime
// rather than silently writing a negative epoch to updated_at.
func (s *Store) UpdateCheckCursor(ctx context.Context, id string, lastRemoteCommit string, checkedAt time.Time) error {
	if checkedAt.IsZero() {
		return ErrZeroCheckTime
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE skill_sources SET
			last_checked_at = ?, last_remote_commit = ?, updated_at = ?
		WHERE id = ?
	`, checkedAt.UnixMilli(), nullable(lastRemoteCommit), checkedAt.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("source: update check cursor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("source: update check cursor rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a source row by id. Returns ErrNotFound when no row
// matches.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM skill_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("source: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("source: delete rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAll returns every source row ordered by created_at ascending.
func (s *Store) ListAll(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, source_type, source_url, provider, owner, repo,
		       ref_type, ref_name, subdir, last_checked_at, last_remote_commit,
		       config_json, created_at, updated_at
		FROM skill_sources
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("source: list all: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// scanner abstracts *sql.Row and *sql.Rows for scanSource.
type scanner interface {
	Scan(dest ...any) error
}

func scanSource(sc scanner) (Source, error) {
	var (
		src              Source
		typeStr          string
		url              sql.NullString
		provider         sql.NullString
		owner            sql.NullString
		repo             sql.NullString
		refType          sql.NullString
		refName          sql.NullString
		subdir           sql.NullString
		lastCheckedMS    sql.NullInt64
		lastRemoteCommit sql.NullString
		configJSON       sql.NullString
		createdMS        int64
		updatedMS        int64
	)
	if err := sc.Scan(
		&src.ID, &src.Name, &typeStr,
		&url, &provider, &owner, &repo,
		&refType, &refName, &subdir,
		&lastCheckedMS, &lastRemoteCommit,
		&configJSON, &createdMS, &updatedMS,
	); err != nil {
		return Source{}, err
	}
	src.Type = SourceType(typeStr)
	src.URL = url.String
	src.Provider = provider.String
	src.Owner = owner.String
	src.Repo = repo.String
	src.RefType = RefType(refType.String)
	src.RefName = refName.String
	src.Subdir = subdir.String
	if lastCheckedMS.Valid {
		src.LastCheckedAt = time.UnixMilli(lastCheckedMS.Int64)
	}
	src.LastRemoteCommit = lastRemoteCommit.String
	src.ConfigJSON = configJSON.String
	src.CreatedAt = time.UnixMilli(createdMS)
	src.UpdatedAt = time.UnixMilli(updatedMS)
	return src, nil
}

// nullable returns nil for empty strings, s for anything else. Used to
// write nullable TEXT columns: sqlite binds nil as NULL and the schema
// accepts it.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableTime returns nil for zero time, UnixMilli() for anything else.
// Used to write nullable INTEGER timestamp columns.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
