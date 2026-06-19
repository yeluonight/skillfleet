// Package draft manages the editable working copies of Skill Packages
// (v1.0 §7.3). A draft is the only place skill content is mutable:
// central versions are immutable, so editing always means "fork a
// version into a draft, edit the draft, publish a new version".
//
// Storage split (mirrors skill_draft_files, migration 0006): small
// text files live inline in content_text — cheap for the WebUI to read
// and edit; binary or oversized files have their bytes written under
// the server store (store/drafts/<draftID>/<sha>) with only the blob
// path kept in content_path. is_binary selects which column is
// authoritative for a row.
//
// This file (t8) covers creation + load. File mutation (PUT/POST/
// DELETE) and publish land in t9 / t10.
package draft

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/safefs"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// draftsSubdir is where binary draft blobs live under the store root.
const draftsSubdir = "drafts"

// Status values for a draft (mirrors the migration 0006 CHECK set).
const (
	StatusOpen      = "open"
	StatusPublished = "published"
	StatusDiscarded = "discarded"
)

// Errors returned by the store.
var (
	ErrNotFound  = errors.New("draft: not found")
	ErrEmptyName = errors.New("draft: name is empty")
	ErrNotOpen   = errors.New("draft: draft is not open")
)

// Store reads and writes drafts. It owns the drafts blob directory and
// borrows the registry to fork existing versions.
type Store struct {
	db       *sql.DB
	registry *registry.Store
	storeDir string // server store root; blobs live under storeDir/drafts
	blobsDir string
}

// New returns a draft Store rooted at storeDir (the server's resolved
// store dir). reg is used to read a base version's files when forking.
func New(db *sql.DB, reg *registry.Store, storeDir string) (*Store, error) {
	blobs := filepath.Join(storeDir, draftsSubdir)
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		return nil, fmt.Errorf("draft: mkdir drafts: %w", err)
	}
	return &Store{db: db, registry: reg, storeDir: storeDir, blobsDir: blobs}, nil
}

// File is one file in a draft. For text files Content holds the bytes
// and IsBinary is false; for binary files Content is the on-disk bytes
// loaded on demand (Load populates text inline but leaves binary
// content to be fetched via the file API in t9).
type File struct {
	Path     string
	IsBinary bool
	Size     int64
	SHA256   string
	Encoding string
	Content  []byte // populated for text files on Load
}

// Draft is the in-memory projection of a skill_drafts row plus files.
type Draft struct {
	ID            string
	Name          string
	Title         string
	Status        string
	BaseVersionID string
	SourceID      string
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Files         []File
}

// CreateParams configures a new draft.
type CreateParams struct {
	// Name is required for a blank draft. When BaseVersionID is set and
	// Name is empty, the base version's name is inherited.
	Name string
	// Title is an optional operator-facing label.
	Title string
	// BaseVersionID, when set, forks that version's files into the
	// draft. When empty the draft starts with a single SKILL.md stub.
	BaseVersionID string
	// CreatedBy is the acting user id (for provenance).
	CreatedBy string
}

// Create opens a new draft. If params.BaseVersionID is set, the draft
// is seeded with that version's files (the fork case); otherwise it
// starts from a minimal SKILL.md stub (the brand-new-skill case).
func (s *Store) Create(ctx context.Context, params CreateParams, now time.Time) (Draft, error) {
	name := params.Name
	baseVersionID := params.BaseVersionID

	var seedFiles []registry.InMemoryFile
	if baseVersionID != "" {
		base, err := s.registry.Get(ctx, baseVersionID)
		if err != nil {
			if errors.Is(err, registry.ErrVersionNotFnd) {
				return Draft{}, fmt.Errorf("draft: base version %q: %w", baseVersionID, ErrNotFound)
			}
			return Draft{}, fmt.Errorf("draft: load base: %w", err)
		}
		if name == "" {
			name = base.Name
		}
		seedFiles, err = s.registry.ReadVersionFiles(ctx, base)
		if err != nil {
			return Draft{}, fmt.Errorf("draft: read base files: %w", err)
		}
	}
	if name == "" {
		return Draft{}, ErrEmptyName
	}
	if seedFiles == nil {
		// Brand-new skill: a single SKILL.md stub.
		seedFiles = []registry.InMemoryFile{{
			Path:    skill.SkillMDName,
			Content: []byte("---\nname: " + name + "\n---\n\n# " + name + "\n\nDescribe what this skill does.\n"),
		}}
	}

	d := Draft{
		ID:            idgen.New("dft"),
		Name:          name,
		Title:         params.Title,
		Status:        StatusOpen,
		BaseVersionID: baseVersionID,
		CreatedBy:     params.CreatedBy,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, fmt.Errorf("draft: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	ms := now.UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_drafts(id, base_version_id, name, title, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, nullable(baseVersionID), d.Name, nullable(d.Title), d.Status, nullable(d.CreatedBy), ms, ms); err != nil {
		return Draft{}, fmt.Errorf("draft: insert draft: %w", err)
	}

	for _, f := range seedFiles {
		df, err := s.persistFile(ctx, tx, d.ID, f.Path, f.Content, ms)
		if err != nil {
			return Draft{}, err
		}
		d.Files = append(d.Files, df)
	}

	if err := tx.Commit(); err != nil {
		return Draft{}, fmt.Errorf("draft: commit: %w", err)
	}
	committed = true
	return d, nil
}

// persistFile writes one file row for a draft, choosing inline text vs
// on-disk blob by content classification + size. Returns the File
// projection. Must run inside the draft's creating/owning tx.
func (s *Store) persistFile(ctx context.Context, tx *sql.Tx, draftID, path string, content []byte, ms int64) (File, error) {
	clean, err := safefs.CleanPackagePath(path)
	if err != nil {
		return File{}, fmt.Errorf("draft: file path %q: %w", path, err)
	}
	binary := skill.IsBinaryContent(content)
	oversize := int64(len(content)) > skill.MaxEditableBytes
	sha := sha256Hex(content)

	f := File{
		Path:     clean,
		IsBinary: binary,
		Size:     int64(len(content)),
		SHA256:   sha,
	}

	var contentText, contentPath any
	if binary || oversize {
		// Store on disk under store/drafts/<draftID>/<sha>.
		blobRel, err := s.writeBlob(draftID, sha, content)
		if err != nil {
			return File{}, err
		}
		contentPath = blobRel
		f.Encoding = "binary"
		// Mark oversized text as binary-for-storage purposes so the row
		// keeps content off the inline column; the file API still
		// reports its true text-ness via re-sniff if needed.
		f.IsBinary = binary
	} else {
		contentText = string(content)
		f.Encoding = "utf-8"
		f.Content = content
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_draft_files(id, draft_id, path, content_path, content_text, encoding, is_binary, size, sha256, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, idgen.New("dff"), draftID, clean, contentPath, contentText, f.Encoding, boolToInt(binary), f.Size, sha, ms); err != nil {
		return File{}, fmt.Errorf("draft: insert file %q: %w", clean, err)
	}
	return f, nil
}

// writeBlob writes content to store/drafts/<draftID>/<sha> and returns
// the store-relative path. Idempotent: an existing identical blob is
// reused.
func (s *Store) writeBlob(draftID, sha string, content []byte) (string, error) {
	dir := filepath.Join(s.blobsDir, draftID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("draft: mkdir blob dir: %w", err)
	}
	abs := filepath.Join(dir, sha)
	if _, err := os.Stat(abs); err != nil {
		if err := os.WriteFile(abs, content, 0o644); err != nil {
			return "", fmt.Errorf("draft: write blob: %w", err)
		}
	}
	return filepath.ToSlash(filepath.Join(draftsSubdir, draftID, sha)), nil
}

// Load reads a draft by id, including its files. Text file content is
// populated inline; binary file content is left nil (fetched on demand
// by the file API in t9).
func (s *Store) Load(ctx context.Context, id string) (Draft, error) {
	var (
		d         Draft
		title     sql.NullString
		base      sql.NullString
		source    sql.NullString
		createdBy sql.NullString
		createdMS int64
		updatedMS int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, title, status, base_version_id, source_id, created_by, created_at, updated_at
		FROM skill_drafts WHERE id = ?
	`, id).Scan(&d.ID, &d.Name, &title, &d.Status, &base, &source, &createdBy, &createdMS, &updatedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("draft: load: %w", err)
	}
	d.Title = title.String
	d.BaseVersionID = base.String
	d.SourceID = source.String
	d.CreatedBy = createdBy.String
	d.CreatedAt = time.UnixMilli(createdMS)
	d.UpdatedAt = time.UnixMilli(updatedMS)

	files, err := s.loadFiles(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	d.Files = files
	return d, nil
}

// loadFiles reads a draft's file rows, ordered by path, populating text
// content inline.
func (s *Store) loadFiles(ctx context.Context, draftID string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, content_text, encoding, is_binary, size, sha256
		FROM skill_draft_files WHERE draft_id = ?
		ORDER BY path
	`, draftID)
	if err != nil {
		return nil, fmt.Errorf("draft: load files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []File
	for rows.Next() {
		var (
			f           File
			contentText sql.NullString
			encoding    sql.NullString
			isBinary    int
			sha         sql.NullString
		)
		if err := rows.Scan(&f.Path, &contentText, &encoding, &isBinary, &f.Size, &sha); err != nil {
			return nil, err
		}
		f.IsBinary = isBinary == 1
		f.Encoding = encoding.String
		f.SHA256 = sha.String
		if contentText.Valid {
			f.Content = []byte(contentText.String)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// BlobPath returns the absolute path to a binary draft file's blob,
// given its store-relative content_path. Used by the file API in t9.
func (s *Store) BlobPath(contentPathRel string) string {
	return filepath.Join(s.storeDir, filepath.FromSlash(contentPathRel))
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
