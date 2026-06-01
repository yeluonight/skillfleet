package agentcfg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	want := Config{
		ServerURL:       "https://sf.example.com",
		DeviceID:        "dev_abc",
		DeviceSecret:    "s3cret",
		HeartbeatIntSec: 45,
		InventoryIntSec: 600,
		JobsIntSec:      15,
		AllowedRoots:    []AllowedRoot{{ID: "claude_user", Tool: "claude-code", Scope: "user", Path: "/home/me/.claude/skills"}},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	// Permissions must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerURL != want.ServerURL || got.DeviceID != want.DeviceID ||
		got.DeviceSecret != want.DeviceSecret ||
		got.HeartbeatIntSec != 45 || got.InventoryIntSec != 600 {
		t.Errorf("round trip mismatch: %+v vs %+v", got, want)
	}
	if len(got.AllowedRoots) != 1 || got.AllowedRoots[0].Path != "/home/me/.claude/skills" ||
		got.AllowedRoots[0].ID != "claude_user" || got.AllowedRoots[0].Tool != "claude-code" {
		t.Errorf("AllowedRoots = %v", got.AllowedRoots)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(filepath.Join(dir, "nope.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestSaveRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	cfg := Config{
		ServerURL: "https://x", DeviceID: "d", DeviceSecret: "s",
		HeartbeatIntSec: 1, InventoryIntSec: 1, JobsIntSec: 1,
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	err := Save(path, cfg)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("err = %v, want refusing-to-overwrite", err)
	}
}

// TestSaveForceOverwrites proves SaveForce replaces an existing config
// in place (unlike Save), keeps 0600, and leaves no temp file behind.
func TestSaveForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	base := Config{
		ServerURL: "https://x", DeviceID: "d", DeviceSecret: "s",
		HeartbeatIntSec: 1, InventoryIntSec: 1, JobsIntSec: 1,
	}
	if err := Save(path, base); err != nil {
		t.Fatal(err)
	}
	// Overwrite with an added root.
	base.AllowedRoots = []AllowedRoot{{ID: "r1", Tool: "codex", Scope: "user", Path: "/abs/skills"}}
	if err := SaveForce(path, base); err != nil {
		t.Fatalf("SaveForce: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AllowedRoots) != 1 || got.AllowedRoots[0].ID != "r1" {
		t.Errorf("AllowedRoots not persisted: %v", got.AllowedRoots)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	// No leftover temp files in the dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("dir should contain only agent.json, got %d entries", len(entries))
	}
}

// TestSaveForceRejectsInvalid proves SaveForce validates before writing
// (a malformed config must not clobber a good one).
func TestSaveForceRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	bad := Config{ServerURL: "x"} // missing device id/secret/intervals
	if err := SaveForce(path, bad); err == nil {
		t.Fatal("want validation error, got nil")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Error("invalid SaveForce created a file")
	}
}

func TestLoadAppliesIntervalDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	// Minimal valid file: intervals omitted on disk.
	body := `{"server_url":"x","device_id":"d","device_secret":"s"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HeartbeatIntSec != DefaultHeartbeatSec || cfg.InventoryIntSec != DefaultInventorySec {
		t.Errorf("defaults not applied: heartbeat=%d inventory=%d", cfg.HeartbeatIntSec, cfg.InventoryIntSec)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	body := `{"server_url":"x","device_id":"d","device_secret":"s","extra":"bad"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error on unknown field")
	}
}

func TestValidateRequiresFields(t *testing.T) {
	cases := []Config{
		{},
		{ServerURL: "x"},
		{ServerURL: "x", DeviceID: "d"},
		{ServerURL: "x", DeviceID: "d", DeviceSecret: "s"},                     // intervals 0
		{ServerURL: "x", DeviceID: "d", DeviceSecret: "s", HeartbeatIntSec: 1}, // inventory 0
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: %+v should fail validation", i, c)
		}
	}
}

func TestExpandHomeRejectsBareTilde(t *testing.T) {
	if _, err := ExpandHome("~foo/bar"); err == nil {
		t.Error("~user form should error")
	}
}
