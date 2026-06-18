package setup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestEnsureCode_FirstBoot(t *testing.T) {
	d := newTestDB(t)
	code, ok, err := EnsureCode(context.Background(), d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok = false on empty users")
	}
	if !strings.HasPrefix(code, CodePrefix) {
		t.Errorf("code missing prefix: %s", code)
	}
	// Format: SF-SETUP-XXXX-XXXX → total 18 chars.
	if len(code) != len(CodePrefix)+codeGroups*codeGroupSize+(codeGroups-1) {
		t.Errorf("code wrong length: %s (len=%d)", code, len(code))
	}
	parts := strings.Split(code, "-")
	if len(parts) != 4 || parts[0] != "SF" || parts[1] != "SETUP" {
		t.Errorf("code shape wrong: %v", parts)
	}
}

func TestEnsureCode_Rotates(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	a, _, _ := EnsureCode(ctx, d, time.Now())
	b, _, _ := EnsureCode(ctx, d, time.Now())
	if a == b {
		t.Errorf("expected rotation, got identical codes")
	}
	// Only the latest hash survives.
	var hash sql.NullString
	if err := d.QueryRow(`SELECT code_hash FROM setup_state WHERE id=1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash.String != hashCode(b) {
		t.Errorf("stored hash is not the latest")
	}
}

func TestEnsureCode_NoopAfterAdmin(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	code, _, _ := EnsureCode(ctx, d, time.Now())
	if _, err := Consume(ctx, d, code, "alice", "correcthorsebatterystaple", time.Now()); err != nil {
		t.Fatal(err)
	}
	code2, ok, err := EnsureCode(ctx, d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ok || code2 != "" {
		t.Errorf("EnsureCode after admin: ok=%v code=%q (want false/empty)", ok, code2)
	}
}

func TestConsume_HappyPath(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	code, _, _ := EnsureCode(ctx, d, time.Now())

	uid, err := Consume(ctx, d, code, "alice", "correcthorsebatterystaple", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uid, "usr_") {
		t.Errorf("user id shape wrong: %s", uid)
	}

	var name, pwHash string
	if err := d.QueryRow(`SELECT username, password_hash FROM users WHERE id=?`, uid).Scan(&name, &pwHash); err != nil {
		t.Fatal(err)
	}
	if name != "alice" {
		t.Errorf("username = %q", name)
	}
	if !strings.HasPrefix(pwHash, "$argon2id$") {
		t.Errorf("password not argon2id: %s", pwHash)
	}

	var consumedAt sql.NullInt64
	var consumedBy sql.NullString
	if err := d.QueryRow(`SELECT consumed_at, consumed_by_user_id FROM setup_state WHERE id=1`).Scan(&consumedAt, &consumedBy); err != nil {
		t.Fatal(err)
	}
	if !consumedAt.Valid || consumedBy.String != uid {
		t.Errorf("consumed row not finalised: at=%v by=%v", consumedAt, consumedBy)
	}
}

func TestConsume_NormalisesInput(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	code, _, _ := EnsureCode(ctx, d, time.Now())
	// Lower-case + extra whitespace + no dashes → must still match.
	normalised := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	if _, err := Consume(ctx, d, " "+normalised+" ", "alice", "correcthorsebatterystaple", time.Now()); err != nil {
		t.Errorf("Consume on normalised input: %v", err)
	}
}

func TestConsume_WrongCode(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	if _, _, err := EnsureCode(ctx, d, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := Consume(ctx, d, "SF-SETUP-AAAA-BBBB", "alice", "correcthorsebatterystaple", time.Now())
	if !errors.Is(err, ErrCodeMismatch) {
		t.Errorf("err = %v, want ErrCodeMismatch", err)
	}
}

func TestConsume_TwiceRejected(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	code, _, _ := EnsureCode(ctx, d, time.Now())
	if _, err := Consume(ctx, d, code, "alice", "correcthorsebatterystaple", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := Consume(ctx, d, code, "bob", "anothergoodpassword!", time.Now())
	if !errors.Is(err, ErrAlreadyConsumed) {
		t.Errorf("err = %v, want ErrAlreadyConsumed", err)
	}
}

func TestConsume_NoPending(t *testing.T) {
	d := newTestDB(t)
	_, err := Consume(context.Background(), d, "SF-SETUP-AAAA-BBBB", "alice", "correcthorsebatterystaple", time.Now())
	if !errors.Is(err, ErrNoPending) {
		t.Errorf("err = %v, want ErrNoPending", err)
	}
}

func TestConsume_RejectsBadUsername(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	code, _, _ := EnsureCode(ctx, d, time.Now())
	cases := map[string]string{
		"too short":       "ab",
		"bad char":        "alice!",
		"space":           "alice user",
		"all whitespace":  "   ",
	}
	for name, u := range cases {
		_, err := Consume(ctx, d, code, u, "correcthorsebatterystaple", time.Now())
		if err == nil || !strings.Contains(err.Error(), "username") {
			t.Errorf("%s: err = %v, want username error", name, err)
		}
	}
}

func TestConsume_RejectsShortPassword(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	code, _, _ := EnsureCode(ctx, d, time.Now())
	_, err := Consume(ctx, d, code, "alice", "short", time.Now())
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("err = %v, want password length error", err)
	}
}

func TestCurrentStatus(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	s, err := CurrentStatus(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Required {
		t.Errorf("Required = false on fresh DB")
	}
	code, _, _ := EnsureCode(ctx, d, time.Now())
	if _, err := Consume(ctx, d, code, "alice", "correcthorsebatterystaple", time.Now()); err != nil {
		t.Fatal(err)
	}
	s, _ = CurrentStatus(ctx, d)
	if s.Required {
		t.Errorf("Required = true after consume")
	}
}
