// Package db wires SkillFleet's SQLite connection.
//
// IMPLEMENTATION_PLAN.md §5.1 mandates four PRAGMAs immediately after
// the connection is open: journal_mode=WAL, synchronous=NORMAL,
// foreign_keys=ON, busy_timeout=5000. This package centralises that
// setup so phase 1 t2's main wiring and the migration runner share the
// same configured handle.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	// Register the modernc.org/sqlite driver under the "sqlite" name.
	_ "modernc.org/sqlite"
)

// DriverName is the database/sql driver name registered by the
// modernc.org/sqlite import. Exposed so tests can dial directly when
// they need a connection without the project's PRAGMA setup.
const DriverName = "sqlite"

// Default file name placed under the configured data_dir.
const DefaultFileName = "skillfleet.db"

// PRAGMAs applied to every connection on open (§5.1).
var startupPragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA foreign_keys=ON",
	"PRAGMA busy_timeout=5000",
}

// Open returns a *sql.DB pointed at dsn with the §5.1 PRAGMAs applied.
//
// The PRAGMAs run inside a per-connection initialiser so they apply to
// every connection in the pool — applying them once on the *sql.DB
// would leave new connections (created after the first) unconfigured.
// The pool is capped at 1 writer + N readers via WAL; SQLite serialises
// writers internally so the cap only matters for concurrent reads.
//
// Callers MUST defer Close on the returned handle.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("db: empty dsn")
	}

	driverDSN := buildDSN(dsn)
	d, err := sql.Open(DriverName, driverDSN)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", dsn, err)
	}

	// WAL gives us reader/writer parallelism but SQLite still
	// serialises writers. Allowing more than one open connection lets
	// reads proceed while a write is in flight; 8 is a generous cap
	// for a single-host control plane.
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(4)
	d.SetConnMaxIdleTime(5 * time.Minute)

	if err := applyPragmas(ctx, d); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// buildDSN translates a filesystem path into a modernc.org/sqlite DSN.
// Passing a bare path works, but using a file: URI lets us be explicit
// about cache mode (shared cache plays poorly with WAL).
func buildDSN(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		// Fall back to the raw path; sql.Open will surface a clearer
		// error if it's also unusable.
		abs = path
	}
	return "file:" + abs + "?_pragma=foreign_keys(1)"
}

func applyPragmas(ctx context.Context, d *sql.DB) error {
	for _, pragma := range startupPragmas {
		if _, err := d.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("db: %s: %w", pragma, err)
		}
	}
	// Verify journal_mode actually flipped to WAL — SQLite silently
	// falls back to the previous mode (memory / delete) when WAL is
	// rejected (e.g. on filesystems that don't support memory-mapped
	// I/O for shared memory). A silent fallback would invalidate the
	// concurrency assumptions in §5.1.
	var mode string
	if err := d.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("db: read journal_mode: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("db: WAL not active (journal_mode=%s)", mode)
	}
	return nil
}
