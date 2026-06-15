package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yeluonight/skillfleet/internal/audit"
)

// audit_handlers.go serves the Audit Page (§13.8.17): a reverse-chronological
// timeline of audit_logs rows, prefix/actor/target/time filterable. Read-only
// (auth, no CSRF) — it only reads the append-only log the write paths already
// populate. The heavy lifting is in audit.List; this handler just parses the
// query string into an audit.ListFilter and shapes the JSON.

// auditEntry is one row in the timeline. Detail is the raw detail_json passed
// through untouched (json.RawMessage) so the UI renders the original object —
// re-marshalling here would reorder keys and lose fidelity. A null detail
// column becomes JSON null, which the UI treats as "no detail".
type auditEntry struct {
	ID         string          `json:"id"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id,omitempty"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type,omitempty"`
	TargetID   string          `json:"target_id,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
	CreatedAt  int64           `json:"created_at"` // ms epoch
}

// auditResponse is the GET /api/audit payload. NextCursor, when non-empty, is
// the created_at (ms) to pass back as `until` to fetch the next older page;
// it is set only when the page came back full (more rows may exist). The UI
// pages backwards in time with it.
type auditResponse struct {
	Entries    []auditEntry `json:"entries"`
	NextCursor int64        `json:"next_cursor,omitempty"`
}

// handleListAudit serves GET /api/audit. Query params (all optional):
//
//	action  — dotted-namespace prefix, e.g. "device." or "auth.login."
//	actor   — actor_type exactly: user | agent | system
//	target  — target_id exactly (one device/skill's history)
//	since   — created_at >= since (ms epoch, inclusive)
//	until   — created_at <  until (ms epoch, exclusive) — also the cursor
//	limit   — page size (default 50, capped at 500 by audit.List)
//
// Read-only (auth, no CSRF). The audit logger is always wired in production;
// a nil logger (some tests) yields 503 so the absence is explicit, not an
// empty list that looks like "no activity".
func (d Deps) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if d.Audit == nil {
		writeError(w, http.StatusServiceUnavailable, "audit_unavailable", "audit log not configured")
		return
	}

	q := r.URL.Query()
	filter := audit.ListFilter{
		ActionPrefix: q.Get("action"),
		ActorType:    q.Get("actor"),
		TargetID:     q.Get("target"),
		Since:        parseMSParam(q.Get("since")),
		Until:        parseMSParam(q.Get("until")),
		Limit:        parseIntParam(q.Get("limit")),
	}

	entries, err := d.Audit.List(r.Context(), filter)
	if err != nil {
		d.logErr("audit: list", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	out := make([]auditEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditEntry{
			ID:         e.ID,
			ActorType:  e.ActorType,
			ActorID:    e.ActorID,
			Action:     e.Action,
			TargetType: e.TargetType,
			TargetID:   e.TargetID,
			Detail:     e.Detail,
			CreatedAt:  e.CreatedAt.UnixMilli(),
		})
	}

	resp := auditResponse{Entries: out}
	// A full page means there may be older rows; hand back the oldest row's
	// timestamp as the exclusive `until` cursor for the next request. The
	// effective page size mirrors audit.List's own default/clamp so the
	// cursor only appears when the DB actually returned a full page.
	if eff := effectiveLimit(filter.Limit); len(out) == eff {
		resp.NextCursor = out[len(out)-1].CreatedAt
	}
	writeJSON(w, http.StatusOK, resp)
}

// effectiveLimit mirrors audit.List's limit defaulting/clamping so the
// handler can tell whether a returned page was "full".
func effectiveLimit(limit int) int {
	if limit <= 0 {
		return audit.DefaultListLimit
	}
	if limit > audit.MaxListLimit {
		return audit.MaxListLimit
	}
	return limit
}

// parseMSParam parses a ms-epoch query param, returning 0 (ignored by the
// filter) on empty or malformed input.
func parseMSParam(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseIntParam parses a small non-negative int query param, returning 0
// (→ filter default) on empty or malformed input.
func parseIntParam(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
