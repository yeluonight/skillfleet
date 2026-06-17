package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/db"
)

// TestAuthCoreSchema applies the embedded migrations against a fresh
// database and asserts the four phase-1-t3 tables, their key columns,
// and their indexes landed correctly. The test exists because
// migrations are immutable once shipped; a CI failure here is the
// signal to write a follow-up migration rather than edit 0002.
func TestAuthCoreSchema(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := Apply(ctx, d, Embedded()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	t.Run("tables", func(t *testing.T) {
		for _, table := range []string{"users", "sessions", "enrollment_tokens", "audit_logs"} {
			var got string
			err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
				table,
			).Scan(&got)
			if err != nil {
				t.Errorf("table %q missing: %v", table, err)
			}
		}
	})

	t.Run("strict_mode", func(t *testing.T) {
		// STRICT tables reject mismatched affinity. Sanity check by
		// trying to insert a non-integer into users.created_at.
		_, err := d.ExecContext(ctx,
			`INSERT INTO users(id, username, password_hash, created_at) VALUES (?,?,?,?)`,
			"u1", "alice", "h", "not-a-number",
		)
		if err == nil || !strings.Contains(err.Error(), "cannot store TEXT") {
			t.Errorf("STRICT not enforced on users.created_at: err=%v", err)
		}
	})

	t.Run("username_unique", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO users(id, username, password_hash, created_at) VALUES ('u_a','dup','h',1)`)
		_, err := d.ExecContext(ctx,
			`INSERT INTO users(id, username, password_hash, created_at) VALUES ('u_b','dup','h',1)`,
		)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Errorf("username UNIQUE not enforced: err=%v", err)
		}
	})

	t.Run("session_cascade", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO users(id, username, password_hash, created_at) VALUES ('u_c','bob','h',1)`)
		mustExec(t, d,
			`INSERT INTO sessions(id, user_id, session_hash, created_at, last_seen_at, expires_at)
			 VALUES ('s1','u_c','sh',1,1,9999)`)

		mustExec(t, d, `DELETE FROM users WHERE id='u_c'`)

		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE user_id='u_c'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("expected ON DELETE CASCADE to remove sessions, got %d row(s)", n)
		}
	})

	t.Run("enrollment_token_hash_unique", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO enrollment_tokens(id, token_hash, status, created_at, expires_at) VALUES ('e1','h1','pending',1,9999)`)
		_, err := d.ExecContext(ctx,
			`INSERT INTO enrollment_tokens(id, token_hash, status, created_at, expires_at) VALUES ('e2','h1','pending',1,9999)`,
		)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Errorf("token_hash UNIQUE not enforced: err=%v", err)
		}
	})

	t.Run("indexes", func(t *testing.T) {
		want := []string{
			"idx_sessions_user_id",
			"idx_sessions_expires_at",
			"idx_enrollment_tokens_expires_at",
			"idx_audit_logs_created_at",
			"idx_audit_logs_action",
		}
		for _, idx := range want {
			var got string
			err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
				idx,
			).Scan(&got)
			if err != nil {
				t.Errorf("index %q missing: %v", idx, err)
			}
		}
	})

	t.Run("schema_migrations_recorded", func(t *testing.T) {
		var n int
		if err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version=2 AND name='auth_core'`,
		).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("schema_migrations missing 2_auth_core row, got count=%d", n)
		}
	})
}

func mustExec(t *testing.T, d *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
