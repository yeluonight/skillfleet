// Package noncepurge periodically deletes expired rows from agent_nonces.
//
// agent_nonces (migrations/0004_devices.sql) only ever grows: every
// authenticated agent request INSERTs a row, and nothing removed them.
// A nonce is only meaningful within the HMAC clock-skew window
// (DefaultMaxClockSkew = 5min) — outside it the request timestamp would
// already be rejected at step 2 of Authenticate. Deleting older rows
// therefore loses no replay protection while keeping the table + index
// small and the WAL checkpoints fast.
//
// Run blocks until ctx is cancelled. It ticks at interval, first tick
// fires after one interval (not immediately) so a fresh server start
// doesn't race the listener. Delete errors are logged via the supplied
// logger and swallowed — a failed sweep just retries next interval.
package noncepurge

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Run deletes agent_nonces rows older than ttl, every interval, until
// ctx is cancelled. ttl should be >= the auth clock-skew window;
// callers typically pass agentapi.DefaultMaxClockSkew. interval <= 0
// makes Run a no-op that returns immediately (lets a config flag
// disable pruning without touching the call site).
func Run(ctx context.Context, db *sql.DB, interval time.Duration, ttl time.Duration, log *slog.Logger) {
	if interval <= 0 {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(ctx, db, ttl, log)
		}
	}
}

// sweep performs one deletion pass. It is also the unit-testable entry
// point (Run is a timing wrapper). Returns the number of rows deleted.
func sweep(ctx context.Context, db *sql.DB, ttl time.Duration, log *slog.Logger) int64 {
	if log == nil {
		log = slog.Default()
	}
	cutoff := time.Now().Add(-ttl).UnixMilli()
	res, err := db.ExecContext(ctx,
		`DELETE FROM agent_nonces WHERE used_at < ?`,
		cutoff,
	)
	if err != nil {
		log.Warn("noncepurge: delete failed", slog.String("err", err.Error()))
		return 0
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		log.Debug("noncepurge: deleted expired nonces", slog.Int64("count", n))
	}
	return n
}
