// Package migrations runs SQL files in order against a sqlite database.
//
// Design:
//   - Files live under repo-root migrations/<NNNN>_<slug>.sql.
//   - They are embedded into the server binary via embed.FS so deployment
//     is a single artefact.
//   - The runner records each applied file in a `schema_migrations` table
//     with its sha256 checksum. A file whose checksum no longer matches
//     the recorded value is treated as tampered: startup aborts so the
//     operator notices instead of the database silently drifting.
//   - Each file runs inside a single transaction; partial application
//     is impossible. SQLite's DDL is transactional, so this is safe.
//
// The runner is intentionally minimal — no "down" migrations, no
// templating, no conditional logic. v2.0 §1.3 immutability requires
// that history is append-only.
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FS holds the embedded migration files. Production callers pass
// [Embedded]; tests pass an in-memory fs.FS.
type FS = fs.FS

//go:embed *.sql
var embeddedFS embed.FS

// Embedded returns the migration files compiled into the binary.
func Embedded() fs.FS { return embeddedFS }

// Migration is one parsed file.
type Migration struct {
	Version int
	Name    string
	SQL     string
	Hash    string // hex sha256 of the raw bytes
}

// Result describes the outcome of an Apply run.
type Result struct {
	StartVersion int // schema version before this run
	EndVersion   int // schema version after this run
	AppliedCount int // number of migrations executed in this run
	AppliedFiles []string
}

var filenameRE = regexp.MustCompile(`^(\d+)_([a-zA-Z0-9._-]+)\.sql$`)

// LoadAll reads every .sql file from fsys, parses the version prefix,
// and returns them sorted by version. Duplicate or missing versions
// are reported as errors so gaps don't silently appear in history.
func LoadAll(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: read dir: %w", err)
	}
	var ms []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := filenameRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrations: %s: filename must match NNNN_name.sql", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migrations: %s: bad version %q: %w", e.Name(), m[1], err)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations: duplicate version %d (%s and %s)", version, prev, e.Name())
		}
		seen[version] = e.Name()
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrations: read %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		ms = append(ms, Migration{
			Version: version,
			Name:    m[2],
			SQL:     string(body),
			Hash:    hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })

	// Reject gaps in the sequence. 0001, 0002, 0003 are required;
	// jumping straight from 0001 to 0003 hides a missing file.
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations: gap at version %d (expected %d, got %d in %s_%s.sql)",
				i+1, i+1, m.Version, leftPad4(m.Version), m.Name)
		}
	}
	return ms, nil
}

func leftPad4(n int) string {
	return fmt.Sprintf("%04d", n)
}

// Apply runs every migration in fsys that hasn't yet been recorded in
// db.schema_migrations, in version order. Already-applied migrations
// are checksum-verified; a mismatch is a fatal error.
func Apply(ctx context.Context, db *sql.DB, fsys fs.FS) (Result, error) {
	if err := ensureTable(ctx, db); err != nil {
		return Result{}, err
	}
	want, err := LoadAll(fsys)
	if err != nil {
		return Result{}, err
	}

	applied, err := loadApplied(ctx, db)
	if err != nil {
		return Result{}, err
	}
	start := currentVersion(applied)

	res := Result{StartVersion: start, EndVersion: start}
	for _, m := range want {
		if rec, ok := applied[m.Version]; ok {
			if rec.Hash != m.Hash {
				return res, fmt.Errorf("migrations: version %d (%s) checksum mismatch: db=%s file=%s",
					m.Version, m.Name, rec.Hash, m.Hash)
			}
			continue
		}
		// New migration: must extend the existing tail by exactly +1.
		if m.Version != res.EndVersion+1 {
			return res, fmt.Errorf("migrations: cannot apply version %d while current is %d",
				m.Version, res.EndVersion)
		}
		if err := applyOne(ctx, db, m); err != nil {
			return res, err
		}
		res.AppliedCount++
		res.AppliedFiles = append(res.AppliedFiles, fmt.Sprintf("%04d_%s.sql", m.Version, m.Name))
		res.EndVersion = m.Version
	}
	return res, nil
}

type appliedRow struct {
	Version int
	Name    string
	Hash    string
}

func ensureTable(ctx context.Context, db *sql.DB) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		hash       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	) STRICT`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("migrations: create schema_migrations: %w", err)
	}
	return nil
}

func loadApplied(ctx context.Context, db *sql.DB) (map[int]appliedRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name, hash FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("migrations: load applied: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]appliedRow{}
	for rows.Next() {
		var r appliedRow
		if err := rows.Scan(&r.Version, &r.Name, &r.Hash); err != nil {
			return nil, fmt.Errorf("migrations: scan applied: %w", err)
		}
		out[r.Version] = r
	}
	return out, rows.Err()
}

func currentVersion(applied map[int]appliedRow) int {
	max := 0
	for v := range applied {
		if v > max {
			max = v
		}
	}
	return max
}

func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: begin tx for %d_%s: %w", m.Version, m.Name, err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migrations: exec %d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, hash) VALUES (?, ?, ?)`,
		m.Version, m.Name, m.Hash,
	); err != nil {
		return fmt.Errorf("migrations: record %d_%s: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrations: commit %d_%s: %w", m.Version, m.Name, err)
	}
	rollback = false
	return nil
}

// ErrAlreadyApplied is returned by helpers that expect a fresh database.
// It is not produced by Apply (which is idempotent).
var ErrAlreadyApplied = errors.New("migrations: already applied")
