// Package audit inserts rows into the `audit_logs` table.
//
// Audit lines are best-effort: a write failure must never block the
// operation being audited. Callers pass a *slog.Logger so the package
// can record a warning when the insert itself fails. Action strings
// use a dotted namespace (`auth.login.success`, `setup.consumed`,
// `gc.package`) so the WebUI can prefix-filter without a join.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

// Actor describes who initiated the audited operation.
type Actor struct {
	Type string // "user" | "agent" | "system"
	ID   string // userID for user actions; "" for system
}

// Target identifies what the action mutated (optional).
type Target struct {
	Type string
	ID   string
}

// Record represents one row in audit_logs.
type Record struct {
	Actor  Actor
	Action string
	Target Target
	Detail map[string]any // marshalled to detail_json
}

// Logger writes audit records. Construct via New; the zero value is
// not usable.
type Logger struct {
	db  *sql.DB
	log *slog.Logger
	now func() time.Time
}

// New constructs a Logger. If now is nil, time.Now is used.
func New(db *sql.DB, log *slog.Logger, now func() time.Time) *Logger {
	if now == nil {
		now = time.Now
	}
	return &Logger{db: db, log: log, now: now}
}

// Write inserts r into audit_logs. Errors are logged at WARN level and
// swallowed so the audited operation does not abort on audit failure.
func (l *Logger) Write(ctx context.Context, r Record) {
	if r.Action == "" {
		if l.log != nil {
			l.log.Warn("audit: empty action; dropping record")
		}
		return
	}
	var detailJSON sql.NullString
	if r.Detail != nil {
		b, err := json.Marshal(r.Detail)
		if err != nil {
			if l.log != nil {
				l.log.Warn("audit: marshal detail", slog.String("err", err.Error()))
			}
		} else {
			detailJSON = sql.NullString{String: string(b), Valid: true}
		}
	}

	_, err := l.db.ExecContext(ctx, `
		INSERT INTO audit_logs (id, actor_type, actor_id, action, target_type, target_id, detail_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		idgen.New("aud"),
		r.Actor.Type,
		nullable(r.Actor.ID),
		r.Action,
		nullable(r.Target.Type),
		nullable(r.Target.ID),
		detailJSON,
		l.now().UnixMilli(),
	)
	if err != nil && l.log != nil {
		l.log.Warn("audit: insert failed",
			slog.String("action", r.Action),
			slog.String("err", err.Error()),
		)
	}
}

// WriteSync is identical to Write but returns the underlying insert
// error. Use only when the caller actually needs to know whether the
// audit landed (rare; tests use this).
func (l *Logger) WriteSync(ctx context.Context, r Record) error {
	if r.Action == "" {
		return fmt.Errorf("audit: empty action")
	}
	var detailJSON sql.NullString
	if r.Detail != nil {
		b, err := json.Marshal(r.Detail)
		if err != nil {
			return fmt.Errorf("audit: marshal detail: %w", err)
		}
		detailJSON = sql.NullString{String: string(b), Valid: true}
	}
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO audit_logs (id, actor_type, actor_id, action, target_type, target_id, detail_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		idgen.New("aud"),
		r.Actor.Type, nullable(r.Actor.ID),
		r.Action,
		nullable(r.Target.Type), nullable(r.Target.ID),
		detailJSON,
		l.now().UnixMilli(),
	)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
