package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
)

// seedEnrolledConfig writes a minimal valid (enrolled, no roots) config
// at a temp path and returns the path. roots commands take -config so we
// never touch the real ~/.skillfleet.
func seedEnrolledConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.json")
	cfg := agentcfg.Config{
		ServerURL: "https://sf.example", DeviceID: "dev_x", DeviceSecret: "sec",
		HeartbeatIntSec: 30, InventoryIntSec: 300, JobsIntSec: 15,
	}
	if err := agentcfg.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRoots_AddListRm(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	skills := t.TempDir() // an existing dir for -path

	// add
	if err := runRoots([]string{"add", "-config", cfgPath, "-tool", "claude-code", "-scope", "user", "-path", skills}); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg, err := agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedRoots) != 1 {
		t.Fatalf("want 1 root, got %d", len(cfg.AllowedRoots))
	}
	r := cfg.AllowedRoots[0]
	if r.Tool != "claude-code" || r.Scope != "user" || r.Path != skills {
		t.Errorf("root = %+v", r)
	}
	if r.ID != "claude-code_user" {
		t.Errorf("auto id = %q, want claude-code_user", r.ID)
	}

	// list (smoke: must not error)
	if err := runRoots([]string{"list", "-config", cfgPath}); err != nil {
		t.Fatalf("list: %v", err)
	}

	// rm
	if err := runRoots([]string{"rm", "-config", cfgPath, "claude-code_user"}); err != nil {
		t.Fatalf("rm: %v", err)
	}
	cfg, _ = agentcfg.Load(cfgPath)
	if len(cfg.AllowedRoots) != 0 {
		t.Errorf("root not removed: %v", cfg.AllowedRoots)
	}
}

func TestRoots_AddDedupesAutoID(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	a, b := t.TempDir(), t.TempDir()
	if err := runRoots([]string{"add", "-config", cfgPath, "-tool", "codex", "-scope", "user", "-path", a}); err != nil {
		t.Fatal(err)
	}
	// Second add with same tool/scope but different path → id deduped.
	if err := runRoots([]string{"add", "-config", cfgPath, "-tool", "codex", "-scope", "user", "-path", b}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := agentcfg.Load(cfgPath)
	if len(cfg.AllowedRoots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(cfg.AllowedRoots))
	}
	if cfg.AllowedRoots[0].ID == cfg.AllowedRoots[1].ID {
		t.Errorf("ids not deduped: both %q", cfg.AllowedRoots[0].ID)
	}
	if cfg.AllowedRoots[1].ID != "codex_user_2" {
		t.Errorf("second id = %q, want codex_user_2", cfg.AllowedRoots[1].ID)
	}
}

func TestRoots_AddExplicitIDCollision(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	d := t.TempDir()
	if err := runRoots([]string{"add", "-config", cfgPath, "-id", "x", "-tool", "codex", "-scope", "user", "-path", d}); err != nil {
		t.Fatal(err)
	}
	err := runRoots([]string{"add", "-config", cfgPath, "-id", "x", "-tool", "opencode", "-scope", "user", "-path", d})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want collision error, got %v", err)
	}
}

func TestRoots_AddRejectsRelativePath(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	// A relative path that doesn't exist → rejected (either by abs+stat or
	// validate). We assert it errors and writes nothing.
	err := runRoots([]string{"add", "-config", cfgPath, "-tool", "codex", "-scope", "user", "-path", "does/not/exist"})
	if err == nil {
		t.Fatal("want error for non-existent path, got nil")
	}
	cfg, _ := agentcfg.Load(cfgPath)
	if len(cfg.AllowedRoots) != 0 {
		t.Errorf("rejected add still wrote a root: %v", cfg.AllowedRoots)
	}
}

func TestRoots_AddRejectsBadScope(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	d := t.TempDir()
	err := runRoots([]string{"add", "-config", cfgPath, "-tool", "codex", "-scope", "bogus", "-path", d})
	if err == nil || !strings.Contains(err.Error(), "invalid -scope") {
		t.Errorf("want invalid-scope error, got %v", err)
	}
}

func TestRoots_RmUnknownID(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	err := runRoots([]string{"rm", "-config", cfgPath, "ghost"})
	if err == nil || !strings.Contains(err.Error(), "no root with id") {
		t.Errorf("want no-such-id error, got %v", err)
	}
}

func TestRoots_RequiresEnrolledConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	err := runRoots([]string{"list", "-config", missing})
	if err == nil || !strings.Contains(err.Error(), "enroll") {
		t.Errorf("want enroll-first hint, got %v", err)
	}
}

func TestParseRootSelection(t *testing.T) {
	got, err := parseRootSelection("1,3-4 3", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selection = %v, want %v", got, want)
	}
	if _, err := parseRootSelection("2-1", 5); err == nil || !strings.Contains(err.Error(), "descending") {
		t.Fatalf("want descending range error, got %v", err)
	}
	if _, err := parseRootSelection("6", 5); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("want out-of-range error, got %v", err)
	}
}

func TestRootsScanRegistersExistingCandidate(t *testing.T) {
	cfgPath := seedEnrolledConfig(t)
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Setenv("HOME", home)
	if oldHome == "" {
		t.Setenv("USERPROFILE", home)
	}
	candidate := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runRootsScanInteractive([]string{"-config", cfgPath}, strings.NewReader("all\n"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	cfg, err := agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedRoots) != 1 {
		t.Fatalf("roots = %+v", cfg.AllowedRoots)
	}
	if cfg.AllowedRoots[0].Path != candidate || cfg.AllowedRoots[0].Tool != "claude-code" {
		t.Fatalf("root = %+v", cfg.AllowedRoots[0])
	}
	if !strings.Contains(out.String(), candidate) || !strings.Contains(errOut.String(), "added root") {
		t.Fatalf("out=%q err=%q", out.String(), errOut.String())
	}
}
