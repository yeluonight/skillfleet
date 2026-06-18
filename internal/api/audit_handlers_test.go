package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
)

// writeAudit drives the Logger directly so the handler tests have rows to read
// back without coupling to whichever write path happens to emit them.
func writeAudit(t *testing.T, l *audit.Logger, rec audit.Record) {
	t.Helper()
	if err := l.WriteSync(t.Context(), rec); err != nil {
		t.Fatalf("write audit %q: %v", rec.Action, err)
	}
}

// TestAudit_ListAndFilters seeds a handful of rows through the real audit
// logger, then exercises the GET /api/audit query surface: default newest-first
// ordering, the action prefix filter, the actor filter, and the limit cap.
func TestAudit_ListAndFilters(t *testing.T) {
	srv, d := newTestServer(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Seed via a logger sharing the same DB the router was built with. A
	// fixed, advancing clock starting *after* now guarantees the seeded rows
	// sort newest, ahead of the login row setupAndLogin already audited.
	n := time.Now().Add(time.Hour).UnixMilli()
	clock := func() time.Time {
		ts := time.UnixMilli(n)
		n += 1000
		return ts
	}
	l := audit.New(d, nil, clock)
	for _, rec := range []audit.Record{
		{Actor: audit.Actor{Type: "user", ID: "usr_1"}, Action: "device.approved", Target: audit.Target{Type: "device", ID: "dev_1"}},
		{Actor: audit.Actor{Type: "agent", ID: "dev_1"}, Action: "device.heartbeat", Target: audit.Target{Type: "device", ID: "dev_1"}},
		{Actor: audit.Actor{Type: "user", ID: "usr_1"}, Action: "skill.published", Target: audit.Target{Type: "skill", ID: "deploy"}, Detail: map[string]any{"version": "v2"}},
	} {
		writeAudit(t, l, rec)
	}

	get := func(t *testing.T, query string) auditResponse {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/audit"+query, nil)
		resp := authedDo(t, sc, cc, req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var out auditResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// No filter: newest-first. The login that setupAndLogin performed also
	// audited (auth.login.success), so there are at least the 3 seeded rows.
	all := get(t, "")
	if len(all.Entries) < 3 {
		t.Fatalf("got %d entries, want >= 3", len(all.Entries))
	}
	if all.Entries[0].Action != "skill.published" {
		t.Errorf("first action = %q, want skill.published (newest seeded)", all.Entries[0].Action)
	}
	// Detail passes through as raw JSON.
	if string(all.Entries[0].Detail) != `{"version":"v2"}` {
		t.Errorf("detail = %s, want {\"version\":\"v2\"}", all.Entries[0].Detail)
	}

	// Action prefix.
	dev := get(t, "?action=device.")
	if len(dev.Entries) != 2 {
		t.Fatalf("action=device. → %d entries, want 2", len(dev.Entries))
	}

	// Actor filter.
	agent := get(t, "?actor=agent")
	if len(agent.Entries) != 1 || agent.Entries[0].Action != "device.heartbeat" {
		t.Fatalf("actor=agent → %+v, want one heartbeat", agent.Entries)
	}

	// Target filter.
	tgt := get(t, "?target=dev_1")
	if len(tgt.Entries) != 2 {
		t.Fatalf("target=dev_1 → %d entries, want 2", len(tgt.Entries))
	}

	// Limit + cursor: ask for 1, expect a next_cursor pointing at the page's
	// oldest row so the client can page backwards.
	one := get(t, "?"+url.Values{"limit": {"1"}}.Encode())
	if len(one.Entries) != 1 {
		t.Fatalf("limit=1 → %d entries, want 1", len(one.Entries))
	}
	if one.NextCursor == 0 {
		t.Error("next_cursor = 0, want the page's oldest created_at")
	}
	if one.NextCursor != one.Entries[0].CreatedAt {
		t.Errorf("next_cursor = %d, want %d (oldest row on page)", one.NextCursor, one.Entries[0].CreatedAt)
	}
}

// TestAudit_RequiresAuth: the endpoint is auth-gated like every other /api read.
func TestAudit_RequiresAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/audit")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
