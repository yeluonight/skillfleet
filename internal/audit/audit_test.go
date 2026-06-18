package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestWriteSyncRoundTrip(t *testing.T) {
	d := newDB(t)
	l := New(d, nil, func() time.Time { return time.UnixMilli(1700000000000) })

	err := l.WriteSync(context.Background(), Record{
		Actor:  Actor{Type: "user", ID: "usr_1"},
		Action: "auth.login.success",
		Target: Target{Type: "session", ID: "ses_1"},
		Detail: map[string]any{"ip": "127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		actorType, action, detail     string
		actorID, targetType, targetID sql.NullString
		createdAt                     int64
	)
	err = d.QueryRow(`
		SELECT actor_type, actor_id, action, target_type, target_id, detail_json, created_at
		  FROM audit_logs LIMIT 1
	`).Scan(&actorType, &actorID, &action, &targetType, &targetID, &detail, &createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if actorType != "user" || actorID.String != "usr_1" {
		t.Errorf("actor: %s/%s", actorType, actorID.String)
	}
	if action != "auth.login.success" {
		t.Errorf("action: %s", action)
	}
	if targetType.String != "session" || targetID.String != "ses_1" {
		t.Errorf("target: %s/%s", targetType.String, targetID.String)
	}
	if detail != `{"ip":"127.0.0.1"}` {
		t.Errorf("detail: %s", detail)
	}
	if createdAt != 1700000000000 {
		t.Errorf("createdAt: %d", createdAt)
	}
}

func TestWriteSwallowsErrors(t *testing.T) {
	d := newDB(t)
	_ = d.Close() // force every Exec to fail
	l := New(d, nil, time.Now)
	// Should NOT panic, NOT return.
	l.Write(context.Background(), Record{
		Actor: Actor{Type: "system"}, Action: "test.swallow",
	})
}

func TestWriteSyncRequiresAction(t *testing.T) {
	d := newDB(t)
	l := New(d, nil, time.Now)
	if err := l.WriteSync(context.Background(), Record{Actor: Actor{Type: "user"}}); err == nil {
		t.Error("expected error on empty action")
	}
}

func TestWriteNilDetail(t *testing.T) {
	d := newDB(t)
	l := New(d, nil, time.Now)
	if err := l.WriteSync(context.Background(), Record{
		Actor: Actor{Type: "system"}, Action: "test.nil_detail",
	}); err != nil {
		t.Fatal(err)
	}
	var detail sql.NullString
	if err := d.QueryRow(`SELECT detail_json FROM audit_logs WHERE action='test.nil_detail'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Valid {
		t.Errorf("detail should be NULL, got %q", detail.String)
	}
}

// --- List (phase 12 t1) ---

// seedEntries writes a fixed set of audit rows at increasing timestamps so
// List's ordering and filters have something deterministic to read back.
func seedEntries(t *testing.T, l *Logger) {
	t.Helper()
	ctx := context.Background()
	rows := []Record{
		{Actor: Actor{Type: "user", ID: "usr_1"}, Action: "auth.login.success", Target: Target{Type: "session", ID: "ses_1"}, Detail: map[string]any{"ip": "10.0.0.1"}},
		{Actor: Actor{Type: "user", ID: "usr_1"}, Action: "device.approved", Target: Target{Type: "device", ID: "dev_1"}},
		{Actor: Actor{Type: "agent", ID: "dev_1"}, Action: "device.heartbeat", Target: Target{Type: "device", ID: "dev_1"}},
		{Actor: Actor{Type: "system"}, Action: "gc.package", Target: Target{Type: "package", ID: "pkg_9"}},
	}
	for _, r := range rows {
		if err := l.WriteSync(ctx, r); err != nil {
			t.Fatalf("seed %q: %v", r.Action, err)
		}
	}
}

// fixedClock returns a now func advancing 1s per call from base, so seeded
// rows get strictly increasing created_at without colliding on the same ms.
func fixedClock(base int64) func() time.Time {
	n := base
	return func() time.Time {
		t := time.UnixMilli(n)
		n += 1000
		return t
	}
}

func TestList_NewestFirst(t *testing.T) {
	d := newDB(t)
	l := New(d, nil, fixedClock(1_700_000_000_000))
	seedEntries(t, l)

	got, err := l.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4", len(got))
	}
	// Last seeded (gc.package) has the newest created_at, so it leads.
	if got[0].Action != "gc.package" {
		t.Errorf("first action = %q, want gc.package (newest first)", got[0].Action)
	}
	if got[3].Action != "auth.login.success" {
		t.Errorf("last action = %q, want auth.login.success (oldest)", got[3].Action)
	}
	// Detail passes through verbatim.
	if string(got[3].Detail) != `{"ip":"10.0.0.1"}` {
		t.Errorf("detail = %s, want {\"ip\":\"10.0.0.1\"}", got[3].Detail)
	}
}

func TestList_ActionPrefix(t *testing.T) {
	d := newDB(t)
	l := New(d, nil, fixedClock(1_700_000_000_000))
	seedEntries(t, l)

	got, err := l.List(context.Background(), ListFilter{ActionPrefix: "device."})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d device.* entries, want 2", len(got))
	}
	for _, e := range got {
		if e.Action != "device.approved" && e.Action != "device.heartbeat" {
			t.Errorf("unexpected action %q in device.* filter", e.Action)
		}
	}
}

func TestList_ActorAndTarget(t *testing.T) {
	d := newDB(t)
	l := New(d, nil, fixedClock(1_700_000_000_000))
	seedEntries(t, l)

	agentRows, err := l.List(context.Background(), ListFilter{ActorType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(agentRows) != 1 || agentRows[0].Action != "device.heartbeat" {
		t.Fatalf("actor=agent → %+v, want one heartbeat", agentRows)
	}

	devRows, err := l.List(context.Background(), ListFilter{TargetID: "dev_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(devRows) != 2 {
		t.Fatalf("target=dev_1 → %d rows, want 2", len(devRows))
	}
}

func TestList_TimeWindowAndLimit(t *testing.T) {
	d := newDB(t)
	base := int64(1_700_000_000_000)
	l := New(d, nil, fixedClock(base))
	seedEntries(t, l) // rows at base, base+1s, base+2s, base+3s

	// since excludes the first row (>= base+1s keeps 3).
	got, err := l.List(context.Background(), ListFilter{Since: base + 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("since=base+1s → %d rows, want 3", len(got))
	}

	// until is exclusive: < base+3s drops the newest, keeping 3.
	got, err = l.List(context.Background(), ListFilter{Until: base + 3000})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("until=base+3s → %d rows, want 3", len(got))
	}

	// limit caps the page.
	got, err = l.List(context.Background(), ListFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("limit=2 → %d rows, want 2", len(got))
	}
}
