package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	cases := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "1"},      // NORMAL == 1
		{"foreign_keys", "1"},     // ON  == 1
		{"busy_timeout", "5000"},
	}
	for _, c := range cases {
		var got string
		row := d.QueryRow("PRAGMA " + c.pragma)
		if err := row.Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", c.pragma, err)
		}
		if got != c.want {
			t.Errorf("PRAGMA %s = %q, want %q", c.pragma, got, c.want)
		}
	}
}

func TestOpenRejectsEmptyDSN(t *testing.T) {
	_, err := Open(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty DSN")
	}
}

// TestOpenPersistsAcrossReopens proves the file is a real on-disk WAL
// database: a row written through one handle survives close + reopen.
func TestOpenPersistsAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")

	d1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d1.Exec(`CREATE TABLE marker (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.Exec(`INSERT INTO marker (note) VALUES ('hi')`); err != nil {
		t.Fatal(err)
	}
	if err := d1.Close(); err != nil {
		t.Fatal(err)
	}

	d2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	var note string
	if err := d2.QueryRow(`SELECT note FROM marker WHERE id = 1`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note != "hi" {
		t.Errorf("note = %q, want %q", note, "hi")
	}
}
