package agentroots

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

func testConfig() agentcfg.Config {
	return agentcfg.Config{
		ServerURL:       "https://sf.example",
		DeviceID:        "dev_x",
		DeviceSecret:    "sec",
		HeartbeatIntSec: 30,
		InventoryIntSec: 300,
		JobsIntSec:      15,
	}
}

func TestRegisterConfigAddsRoot(t *testing.T) {
	rootDir := t.TempDir()
	cfg, res, err := RegisterConfig(testConfig(), Spec{Tool: "claude-code", Scope: "user", Path: rootDir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Added || res.Root.ID != "claude-code_user" {
		t.Fatalf("result = %+v", res)
	}
	if len(cfg.AllowedRoots) != 1 || cfg.AllowedRoots[0].Path != rootDir {
		t.Fatalf("roots = %+v", cfg.AllowedRoots)
	}
}

func TestRegisterConfigDedupesAutoID(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	cfg, _, err := RegisterConfig(testConfig(), Spec{Tool: "codex", Scope: "user", Path: a})
	if err != nil {
		t.Fatal(err)
	}
	_, res, err := RegisterConfig(cfg, Spec{Tool: "codex", Scope: "user", Path: b})
	if err != nil {
		t.Fatal(err)
	}
	if res.Root.ID != "codex_user_2" {
		t.Fatalf("id = %q", res.Root.ID)
	}
}

func TestRegisterConfigIdempotentByPath(t *testing.T) {
	rootDir := t.TempDir()
	cfg, first, err := RegisterConfig(testConfig(), Spec{Tool: "codex", Scope: "user", Path: rootDir})
	if err != nil {
		t.Fatal(err)
	}
	cfg, second, err := RegisterConfig(cfg, Spec{Tool: "opencode", Scope: "user", Path: rootDir}, WithIdempotent())
	if err != nil {
		t.Fatal(err)
	}
	if second.Added || second.Root.ID != first.Root.ID {
		t.Fatalf("second = %+v, first = %+v", second, first)
	}
	if len(cfg.AllowedRoots) != 1 {
		t.Fatalf("roots = %+v", cfg.AllowedRoots)
	}
}

func TestRegisterConfigRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "missing tool", spec: Spec{Scope: "user", Path: dir}, want: "requires"},
		{name: "bad scope", spec: Spec{Tool: "codex", Scope: "bogus", Path: dir}, want: "invalid -scope"},
		{name: "missing dir", spec: Spec{Tool: "codex", Scope: "user", Path: filepath.Join(dir, "missing")}, want: "no such file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := RegisterConfig(testConfig(), tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsCandidateSystemRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "etc", "codex", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Validate(testConfig(), []inventory.RootCandidate{
		{ToolKey: "codex", Scope: "system", Path: root},
	}, Spec{Tool: "codex", Scope: "system", Path: root}, RemotePolicy{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("path = %q, want %q", got, root)
	}
}

func TestValidateAcceptsHomeChildCustomRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "custom", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Validate(testConfig(), nil, Spec{Tool: "claude-code", Scope: "user", Path: root},
		RemotePolicy{HomeDir: home, AllowHomeChild: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("path = %q, want %q", got, root)
	}
}

func TestValidateRejectsHomeChildUnlessAllowed(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "custom", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Validate(testConfig(), nil, Spec{Tool: "claude-code", Scope: "user", Path: root},
		RemotePolicy{HomeDir: home})
	if err == nil {
		t.Fatal("want home child rejection without AllowHomeChild")
	}
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeRootOutsidePolicy {
		t.Fatalf("err = %#v", err)
	}
}

func TestValidateRejectsHomeItselfAndOutside(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	for _, tc := range []struct {
		name string
		path string
		code string
	}{
		{name: "home itself", path: home, code: CodeRootOutsidePolicy},
		{name: "outside", path: outside, code: CodeRootOutsidePolicy},
		{name: "missing", path: filepath.Join(home, "missing"), code: CodeRootPathInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(testConfig(), nil, Spec{Tool: "claude-code", Scope: "user", Path: tc.path},
				RemotePolicy{HomeDir: home, AllowHomeChild: true})
			if err == nil {
				t.Fatal("want error")
			}
			var coded *Error
			if !errors.As(err, &coded) || coded.Code != tc.code {
				t.Fatalf("err = %#v, want code %s", err, tc.code)
			}
		})
	}
}

func TestValidateRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(home, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Validate(testConfig(), nil, Spec{Tool: "claude-code", Scope: "user", Path: link},
		RemotePolicy{HomeDir: home, AllowHomeChild: true})
	if err == nil {
		t.Fatal("want symlink escape rejection")
	}
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeRootOutsidePolicy {
		t.Fatalf("err = %#v", err)
	}
}

func TestValidateIdempotentRegisteredRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "custom", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.AllowedRoots = []agentcfg.AllowedRoot{{ID: "r1", Tool: "claude-code", Scope: "user", Path: root}}
	if _, err := Validate(cfg, nil, Spec{Tool: "claude-code", Scope: "user", Path: root}, RemotePolicy{HomeDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsProjectScope(t *testing.T) {
	root := t.TempDir()
	_, err := Validate(testConfig(), nil, Spec{Tool: "claude-code", Scope: "project", Path: root}, RemotePolicy{HomeDir: t.TempDir()})
	if err == nil {
		t.Fatal("want project scope rejection")
	}
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeRootPathInvalid {
		t.Fatalf("err = %#v", err)
	}
}

func TestRemoveConfig(t *testing.T) {
	rootDir := t.TempDir()
	cfg, res, err := RegisterConfig(testConfig(), Spec{Tool: "pi", Scope: "user", Path: rootDir})
	if err != nil {
		t.Fatal(err)
	}
	cfg, removed, err := RemoveConfig(cfg, res.Root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != res.Root.ID || len(cfg.AllowedRoots) != 0 {
		t.Fatalf("removed=%+v roots=%+v", removed, cfg.AllowedRoots)
	}
}
