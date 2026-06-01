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
		actorType, action, detail string
		actorID, targetType, targetID sql.NullString
		createdAt                  int64
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
