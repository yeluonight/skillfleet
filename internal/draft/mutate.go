package draft

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yeluonight/skillfleet/internal/safefs"
)

// ErrFileNotFound is returned when a draft file path doesn't exist.
var ErrFileNotFound = errors.New("draft: file not found")

// PutFile creates or replaces a file in an open draft. content is
// classified (text-inline vs binary/oversized-on-disk) the same way as
// seeding. Returns the resulting File projection. The draft's
// updated_at is bumped.
//
// PutFile is the single upsert used by both the "create file" (POST)
// and "replace file" (PUT) API routes — the distinction is enforced at
// the handler layer (POST rejects an existing path; PUT requires one).
func (s *Store) PutFile(ctx context.Context, draftID, path string, content []byte, now time.Time) (File, error) {
	clean, err := safefs.CleanPackagePath(path)
	if err != nil {
		return File{}, fmt.Errorf("draft: file path %q: %w", path, err)
	}

	status, err := s.status(ctx, draftID)
	if err != nil {
		return File{}, err
	}
	if status != StatusOpen {
		return File{}, ErrNotOpen
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, fmt.Errorf("draft: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	ms := now.UnixMilli()
	// Delete any existing row for this path first, so the insert in
	// persistFile is always a clean create (keeps the (draft,path)
	// unique index happy and avoids a separate update path).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM skill_draft_files WHERE draft_id = ? AND path = ?`, draftID, clean); err != nil {
		return File{}, fmt.Errorf("draft: clear existing file: %w", err)
	}
	f, err := s.persistFile(ctx, tx, draftID, clean, content, ms)
	if err != nil {
		return File{}, err
	}
	if err := s.touch(ctx, tx, draftID, ms); err != nil {
		return File{}, err
	}
	if err := tx.Commit(); err != nil {
		return File{}, fmt.Errorf("draft: commit: %w", err)
	}
	committed = true
	return f, nil
}

// FileExists reports whether the draft has a file at path.
func (s *Store) FileExists(ctx context.Context, draftID, path string) (bool, error) {
	clean, err := safefs.CleanPackagePath(path)
	if err != nil {
		return false, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM skill_draft_files WHERE draft_id = ? AND path = ?`, draftID, clean,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("draft: file exists: %w", err)
	}
	return n > 0, nil
}

// ReadFile returns a single draft file's bytes (text from the inline
// column, binary from the on-disk blob) plus its is_binary flag.
func (s *Store) ReadFile(ctx context.Context, draftID, path string) (content []byte, isBinary bool, err error) {
	clean, err := safefs.CleanPackagePath(path)
	if err != nil {
		return nil, false, err
	}
	var (
		contentText sql.NullString
		contentPath sql.NullString
		isBin       int
	)
	err = s.db.QueryRowContext(ctx, `
		SELECT content_text, content_path, is_binary
		FROM skill_draft_files WHERE draft_id = ? AND path = ?
	`, draftID, clean).Scan(&contentText, &contentPath, &isBin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrFileNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("draft: read file: %w", err)
	}
	if contentText.Valid {
		return []byte(contentText.String), isBin == 1, nil
	}
	if contentPath.Valid {
		b, err := os.ReadFile(s.BlobPath(contentPath.String))
		if err != nil {
			return nil, false, fmt.Errorf("draft: read blob: %w", err)
		}
		return b, isBin == 1, nil
	}
	// Neither column set: an empty text file.
	return []byte{}, isBin == 1, nil
}

// DeleteFile removes a file from an open draft. Returns ErrFileNotFound
// if the path isn't present. The on-disk blob (if any) is left for GC;
// blobs are sha-named and may be shared, so we don't eagerly unlink.
func (s *Store) DeleteFile(ctx context.Context, draftID, path string, now time.Time) error {
	clean, err := safefs.CleanPackagePath(path)
	if err != nil {
		return err
	}
	status, err := s.status(ctx, draftID)
	if err != nil {
		return err
	}
	if status != StatusOpen {
		return ErrNotOpen
	}

	res, err := s.db.ExecContext(ctx,
		`DELETE FROM skill_draft_files WHERE draft_id = ? AND path = ?`, draftID, clean)
	if err != nil {
		return fmt.Errorf("draft: delete file: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFileNotFound
	}
	// Bump updated_at outside a tx; a stray timestamp on a no-op is
	// harmless and we already confirmed the delete affected a row.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE skill_drafts SET updated_at = ? WHERE id = ?`, now.UnixMilli(), draftID); err != nil {
		return fmt.Errorf("draft: touch: %w", err)
	}
	return nil
}

// Delete discards a draft entirely: the row is removed (skill_draft_files
// cascade) and its blob directory is best-effort cleaned. Returns
// ErrNotFound if the draft doesn't exist.
func (s *Store) Delete(ctx context.Context, draftID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM skill_drafts WHERE id = ?`, draftID)
	if err != nil {
		return fmt.Errorf("draft: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	// Best-effort blob cleanup; a leftover dir is GC's problem, not a
	// correctness issue.
	_ = os.RemoveAll(filepath.Join(s.blobsDir, draftID))
	return nil
}

// status returns a draft's status, or ErrNotFound.
func (s *Store) status(ctx context.Context, draftID string) (string, error) {
	var st string
	err := s.db.QueryRowContext(ctx,
		`SELECT status FROM skill_drafts WHERE id = ?`, draftID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("draft: status: %w", err)
	}
	return st, nil
}

// touch bumps a draft's updated_at within a tx.
func (s *Store) touch(ctx context.Context, tx *sql.Tx, draftID string, ms int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE skill_drafts SET updated_at = ? WHERE id = ?`, ms, draftID); err != nil {
		return fmt.Errorf("draft: touch: %w", err)
	}
	return nil
}
