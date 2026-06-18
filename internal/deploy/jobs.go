// deployment_jobs store: the server-side queue of downlink work
// (v1.0 §12). This file is the only writer of the deployment_jobs table.
//
// The interesting operation is ClaimNext: it must hand a pending job to
// exactly one agent even if a device runs two agent processes (or one
// agent double-polls). We get single-winner semantics from a write
// transaction — SELECT the oldest claimable job for the device, then
// UPDATE it to "claimed" guarded by "AND status = 'pending'"; SQLite
// serialises writers, so a second concurrent claim sees the row already
// non-pending and its conditional UPDATE affects zero rows. We do not
// rely on RETURNING (not yet used anywhere in this codebase) — the
// SELECT-then-conditional-UPDATE inside one tx is portable and just as
// race-free.
//
// Expiry is lazy: rather than run a reaper goroutine, ClaimNext skips
// (and marks) any pending job whose expires_at has passed. A job only
// expires while still pending; once claimed it is the agent's
// responsibility (a claimed job whose agent crashed stays claimed and is
// visible as "stuck" in the WebUI — a claimed-timeout reaper is left to
// a later phase).

package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yeluonight/skillfleet/internal/idgen"
)

// DefaultJobTTL is how long a pending job stays claimable before it
// lazily expires. An agent polling on a ~15s cadence has ample time;
// an hour also covers a briefly-offline device coming back.
const DefaultJobTTL = time.Hour

// Errors returned by the store.
var (
	ErrJobNotFound   = errors.New("deploy: job not found")
	ErrBadOperation  = errors.New("deploy: invalid operation")
	ErrEmptyDeviceID = errors.New("deploy: device id is empty")
	// ErrNotClaimable is returned by Complete when the job is not in a
	// state from which the agent may report a result (it must be claimed
	// or running — reporting on a pending or already-terminal job is a
	// protocol violation).
	ErrNotClaimable = errors.New("deploy: job not in a reportable state")
)

// Job is the in-memory projection of a deployment_jobs row. RequestJSON
// / PlanJSON / ResultJSON are kept as raw strings here; callers
// (un)marshal the typed Request / Plan / Result as needed, so the store
// stays a pure persistence surface that never interprets the payloads.
type Job struct {
	ID          string
	DeviceID    string
	Operation   Operation
	Status      Status
	RequestJSON string
	PlanJSON    string // nullable → "" when absent
	ResultJSON  string // nullable → "" when absent
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time // zero = no expiry
}

// Store reads and writes deployment_jobs. It owns only the database
// handle — no filesystem state. Constructed by the server; linked but
// unused by the agent (which only needs this package's wire types).
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateParams carries the inputs to Create. PlanJSON is optional (a
// rollback job may not need a plan; an install job carries the planner's
// resolved plan). TTL, when zero, defaults to DefaultJobTTL; pass a
// negative TTL to create a job with no expiry (ExpiresAt stays NULL).
type CreateParams struct {
	DeviceID    string
	Operation   Operation
	RequestJSON string
	PlanJSON    string
	TTL         time.Duration
}

// Create inserts a new pending job. The id is minted with the "dj"
// prefix. now is injected for deterministic timestamps.
func (s *Store) Create(ctx context.Context, p CreateParams, now time.Time) (Job, error) {
	if p.DeviceID == "" {
		return Job{}, ErrEmptyDeviceID
	}
	if !p.Operation.valid() {
		return Job{}, fmt.Errorf("%w: %q", ErrBadOperation, p.Operation)
	}
	if p.RequestJSON == "" {
		// request_json is NOT NULL and always meaningful (it is the
		// operator's intent); an empty one is a caller bug.
		return Job{}, fmt.Errorf("deploy: request_json is empty")
	}

	job := Job{
		ID:          idgen.New("dj"),
		DeviceID:    p.DeviceID,
		Operation:   p.Operation,
		Status:      StatusPending,
		RequestJSON: p.RequestJSON,
		PlanJSON:    p.PlanJSON,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	switch {
	case p.TTL < 0:
		// explicit "no expiry" → leave ExpiresAt zero (NULL).
	case p.TTL == 0:
		job.ExpiresAt = now.Add(DefaultJobTTL)
	default:
		job.ExpiresAt = now.Add(p.TTL)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deployment_jobs(
			id, device_id, operation, status, request_json, plan_json,
			result_json, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)
	`,
		job.ID, job.DeviceID, string(job.Operation), string(job.Status),
		job.RequestJSON, nullable(job.PlanJSON),
		now.UnixMilli(), now.UnixMilli(), nullableTime(job.ExpiresAt),
	)
	if err != nil {
		return Job{}, fmt.Errorf("deploy: insert job: %w", err)
	}
	return job, nil
}

// Get loads a single job by id. Returns ErrJobNotFound when no row
// matches.
func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	return job, err
}

// ClaimNext atomically hands the oldest claimable pending job for
// deviceID to the caller, transitioning it pending → claimed. It returns
// (job, true, nil) on a successful claim, or (zero, false, nil) when the
// device has no claimable work. Any pending job whose expires_at has
// passed is marked expired (not returned) before the search continues,
// so a backlog of stale jobs can't block a fresh one.
//
// Single-winner guarantee: the SELECT + conditional UPDATE run in one
// write transaction. SQLite serialises writers, and the UPDATE carries
// "AND status = 'pending'", so if two agents race, the second's UPDATE
// matches zero rows and it loops to the next candidate (or returns
// empty). deviceID scoping means an agent can only ever claim its own
// device's jobs — the caller passes the HMAC-authenticated device id.
func (s *Store) ClaimNext(ctx context.Context, deviceID string, now time.Time) (Job, bool, error) {
	if deviceID == "" {
		return Job{}, false, ErrEmptyDeviceID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, fmt.Errorf("deploy: begin claim tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowMS := now.UnixMilli()

	// Lazily expire stale pending jobs for this device in one shot, so
	// they neither get claimed nor block the ORDER BY scan. A NULL
	// expires_at never expires.
	if _, err := tx.ExecContext(ctx, `
		UPDATE deployment_jobs
		SET status = 'expired', updated_at = ?
		WHERE device_id = ? AND status = 'pending'
		  AND expires_at IS NOT NULL AND expires_at <= ?
	`, nowMS, deviceID, nowMS); err != nil {
		return Job{}, false, fmt.Errorf("deploy: expire stale jobs: %w", err)
	}

	// Pick the oldest still-pending job for this device.
	row := tx.QueryRowContext(ctx, `
		SELECT id FROM deployment_jobs
		WHERE device_id = ? AND status = 'pending'
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, deviceID)
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No work; commit the expiry updates (if any) and report empty.
			if err := tx.Commit(); err != nil {
				return Job{}, false, fmt.Errorf("deploy: commit empty claim: %w", err)
			}
			committed = true
			return Job{}, false, nil
		}
		return Job{}, false, fmt.Errorf("deploy: select claimable: %w", err)
	}

	// Conditional claim. The "AND status = 'pending'" makes this the CAS:
	// a racing claimer that already flipped this row sees zero rows here.
	res, err := tx.ExecContext(ctx, `
		UPDATE deployment_jobs
		SET status = 'claimed', updated_at = ?
		WHERE id = ? AND status = 'pending'
	`, nowMS, id)
	if err != nil {
		return Job{}, false, fmt.Errorf("deploy: claim update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Job{}, false, fmt.Errorf("deploy: claim rows: %w", err)
	}
	if n == 0 {
		// Lost the race for this row. Commit what we have and let the
		// caller poll again; the next tick re-scans for another job.
		if err := tx.Commit(); err != nil {
			return Job{}, false, fmt.Errorf("deploy: commit lost claim: %w", err)
		}
		committed = true
		return Job{}, false, nil
	}

	// Re-read the now-claimed row to return a faithful projection.
	job, err := scanJob(tx.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id))
	if err != nil {
		return Job{}, false, fmt.Errorf("deploy: reload claimed job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, fmt.Errorf("deploy: commit claim: %w", err)
	}
	committed = true
	return job, true, nil
}

// MarkRunning transitions a claimed job to running. It is optional
// progress reporting; an agent that skips it and reports a terminal
// result directly is fine. Returns ErrNotClaimable if the job is not
// currently claimed, ErrJobNotFound if absent.
func (s *Store) MarkRunning(ctx context.Context, id string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE deployment_jobs SET status = 'running', updated_at = ?
		WHERE id = ? AND status = 'claimed'
	`, now.UnixMilli(), id)
	return s.checkTransition(ctx, id, res, err, "mark running")
}

// Complete records a terminal result on a job, transitioning it to
// status (which must be succeeded or failed) and storing resultJSON.
// The job must currently be claimed or running; reporting on a pending
// or already-terminal job returns ErrNotClaimable. now is injected.
func (s *Store) Complete(ctx context.Context, id string, status Status, resultJSON string, now time.Time) error {
	if status != StatusSucceeded && status != StatusFailed {
		return fmt.Errorf("deploy: complete with non-terminal status %q", status)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE deployment_jobs
		SET status = ?, result_json = ?, updated_at = ?
		WHERE id = ? AND status IN ('claimed', 'running')
	`, string(status), nullable(resultJSON), now.UnixMilli(), id)
	return s.checkTransition(ctx, id, res, err, "complete")
}

// checkTransition maps a conditional UPDATE's outcome to the right
// error: a DB fault, ErrJobNotFound if the id doesn't exist at all, or
// ErrNotClaimable if the row exists but wasn't in the required state
// (the WHERE matched zero rows).
func (s *Store) checkTransition(ctx context.Context, id string, res sql.Result, execErr error, what string) error {
	if execErr != nil {
		return fmt.Errorf("deploy: %s: %w", what, execErr)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deploy: %s rows: %w", what, err)
	}
	if n == 0 {
		// Distinguish "no such job" from "wrong state" for a clear error.
		var exists int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM deployment_jobs WHERE id = ?`, id).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrJobNotFound
			}
			return fmt.Errorf("deploy: %s existence check: %w", what, err)
		}
		return ErrNotClaimable
	}
	return nil
}

// ListFilter narrows List. Empty fields are ignored, so a zero filter
// lists every job. Status, when set, must be a valid Status.
type ListFilter struct {
	DeviceID  string
	SkillName string // matched against request_json's skill_name
	Status    Status
	Limit     int // 0 → default 100
}

// List returns jobs matching the filter, newest first. SkillName matches
// the install request's skill (rollback jobs carry no skill_name and are
// excluded when SkillName is set). This backs the WebUI's per-device /
// per-skill job views.
func (s *Store) List(ctx context.Context, f ListFilter) ([]Job, error) {
	q := selectColumns + ` WHERE 1 = 1`
	var args []any
	if f.DeviceID != "" {
		q += ` AND device_id = ?`
		args = append(args, f.DeviceID)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, string(f.Status))
	}
	if f.SkillName != "" {
		// request_json embeds "skill_name":"<name>"; a LIKE on the JSON
		// text avoids a json_extract dependency and is index-agnostic.
		// The name is exact-quoted to avoid matching a prefix.
		q += ` AND request_json LIKE ?`
		args = append(args, `%"skill_name":"`+f.SkillName+`"%`)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("deploy: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// selectColumns is the shared column list for every job read, so Get /
// ClaimNext / List project identically.
const selectColumns = `
	SELECT id, device_id, operation, status, request_json, plan_json,
	       result_json, created_at, updated_at, expires_at
	FROM deployment_jobs`

// scanner abstracts *sql.Row and *sql.Rows for scanJob.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(sc scanner) (Job, error) {
	var (
		job        Job
		opStr      string
		statusStr  string
		planJSON   sql.NullString
		resultJSON sql.NullString
		createdMS  int64
		updatedMS  int64
		expiresMS  sql.NullInt64
	)
	if err := sc.Scan(
		&job.ID, &job.DeviceID, &opStr, &statusStr, &job.RequestJSON,
		&planJSON, &resultJSON, &createdMS, &updatedMS, &expiresMS,
	); err != nil {
		return Job{}, err
	}
	job.Operation = Operation(opStr)
	job.Status = Status(statusStr)
	job.PlanJSON = planJSON.String
	job.ResultJSON = resultJSON.String
	job.CreatedAt = time.UnixMilli(createdMS)
	job.UpdatedAt = time.UnixMilli(updatedMS)
	if expiresMS.Valid {
		job.ExpiresAt = time.UnixMilli(expiresMS.Int64)
	}
	return job, nil
}

// nullable returns nil for empty strings so the column is written NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableTime returns nil for the zero time so the column is written
// NULL; otherwise the millisecond epoch.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
