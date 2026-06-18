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

// ListFilter narrows List. Zero-value fields are ignored, so the empty
// filter returns the most recent rows up to Limit. Times are ms epoch:
// Since is an inclusive lower bound (created_at >= Since), Until an
// exclusive upper bound (created_at < Until) — the exclusive upper bound
// lets a caller page backwards by passing the previous page's oldest
// created_at as the next Until without re-seeing that boundary row.
type ListFilter struct {
	// ActionPrefix matches the dotted action namespace by prefix, e.g.
	// "device." selects device.approved / device.revoked / ... Action
	// strings never contain LIKE wildcards, so a literal "%"-suffix LIKE is
	// safe without ESCAPE.
	ActionPrefix string
	// ActorType filters by actor kind ("user" | "agent" | "system"); the
	// WebUI's three actor lanes map straight onto it.
	ActorType string
	// TargetID filters to one mutated object (a device id, skill name, ...)
	// for "show everything that happened to X".
	TargetID string
	Since    int64
	Until    int64
	// Limit caps the page; <=0 → DefaultListLimit, and anything over
	// MaxListLimit is clamped so one query can't scan the whole table.
	Limit int
}

const (
	// DefaultListLimit is the page size when ListFilter.Limit is unset.
	DefaultListLimit = 50
	// MaxListLimit caps ListFilter.Limit so a single page stays bounded.
	MaxListLimit = 500
)

// Entry is one audit_logs row as read back by List. Nullable columns
// surface as the Go zero value ("" / nil Detail); Detail is the raw
// detail_json so callers (the WebUI audit page) render it verbatim.
type Entry struct {
	ID         string
	ActorType  string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Detail     json.RawMessage
	CreatedAt  time.Time
}

// List returns audit rows matching f, newest first (created_at DESC, id
// DESC for a stable tiebreak). Unlike Write it surfaces its error: a query
// failure is a real API error, not a best-effort side line.
func (l *Logger) List(ctx context.Context, f ListFilter) ([]Entry, error) {
	q := `
		SELECT id, actor_type, actor_id, action, target_type, target_id, detail_json, created_at
		  FROM audit_logs
		 WHERE 1 = 1`
	var args []any
	if f.ActionPrefix != "" {
		q += ` AND action LIKE ?`
		args = append(args, f.ActionPrefix+"%")
	}
	if f.ActorType != "" {
		q += ` AND actor_type = ?`
		args = append(args, f.ActorType)
	}
	if f.TargetID != "" {
		q += ` AND target_id = ?`
		args = append(args, f.TargetID)
	}
	if f.Since > 0 {
		q += ` AND created_at >= ?`
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		q += ` AND created_at < ?`
		args = append(args, f.Until)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var (
			e          Entry
			actorID    sql.NullString
			targetType sql.NullString
			targetID   sql.NullString
			detail     sql.NullString
			createdMS  int64
		)
		if err := rows.Scan(&e.ID, &e.ActorType, &actorID, &e.Action,
			&targetType, &targetID, &detail, &createdMS); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		e.ActorID = actorID.String
		e.TargetType = targetType.String
		e.TargetID = targetID.String
		if detail.Valid {
			e.Detail = json.RawMessage(detail.String)
		}
		e.CreatedAt = time.UnixMilli(createdMS)
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
