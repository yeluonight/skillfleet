package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

type expectedSkill struct {
	Name           string   `json:"name"`
	HasSkillMD     bool     `json:"has_skill_md"`
	Description    string   `json:"description"`
	EffectiveState string   `json:"effective_state"`
	NativeState    string   `json:"native_state"`
	Warnings       []string `json:"warnings"`
}

type expectedDoc struct {
	RootID string          `json:"root_id"`
	Tool   string          `json:"tool"`
	Scope  string          `json:"scope"`
	Skills []expectedSkill `json:"skills"`
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "opencode"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestAdapter_Identity(t *testing.T) {
	a := New()
	if a.Key() != "opencode" || a.DisplayName() != "OpenCode" {
		t.Errorf("identity = %q / %q", a.Key(), a.DisplayName())
	}
}

func TestScanSkills_MatchesFixtureExpected(t *testing.T) {
	root := fixtureRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want expectedDoc
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{ConfigPath: filepath.Join(root, "opencode.json")}
	sr := adapters.SkillRoot{
		ID:    want.RootID,
		Tool:  want.Tool,
		Scope: adapters.Scope(want.Scope),
		Path:  filepath.Join(root, "user-skills"),
	}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()}, sr)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })

	if len(got) != len(want.Skills) {
		t.Fatalf("got %d skills, want %d", len(got), len(want.Skills))
	}
	for i, w := range want.Skills {
		g := got[i]
		if g.Name != w.Name {
			t.Errorf("[%d] name = %q, want %q", i, g.Name, w.Name)
		}
		if g.SkillMD.Description != w.Description {
			t.Errorf("[%s] description = %q, want %q", w.Name, g.SkillMD.Description, w.Description)
		}
		if string(g.EffectiveState) != w.EffectiveState {
			t.Errorf("[%s] effective = %q, want %q", w.Name, g.EffectiveState, w.EffectiveState)
		}
		if g.NativeState != w.NativeState {
			t.Errorf("[%s] native = %q, want %q", w.Name, g.NativeState, w.NativeState)
		}
		if len(g.Warnings) != len(w.Warnings) {
			t.Errorf("[%s] warnings = %+v, want %d", w.Name, g.Warnings, len(w.Warnings))
		}
		if g.ContentSHA256 == "" {
			t.Errorf("[%s] ContentSHA256 empty", w.Name)
		}
	}
}

func TestScanSkills_MissingConfigDefaultsAllow(t *testing.T) {
	root := filepath.Join(fixtureRoot(t), "user-skills")
	a := &Adapter{ConfigPath: filepath.Join(t.TempDir(), "nope.json")}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()},
		adapters.SkillRoot{ID: "opencode_user_opencode", Tool: "opencode", Scope: adapters.ScopeUser, Path: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g.EffectiveState != adapters.StateOn {
			t.Errorf("[%s] state = %q, want on (no config = allow)", g.Name, g.EffectiveState)
		}
		if len(g.Warnings) != 0 {
			t.Errorf("[%s] missing config should not warn", g.Name)
		}
	}
}

func TestScanSkills_InvalidJSONWarns(t *testing.T) {
	root := filepath.Join(fixtureRoot(t), "user-skills")
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{ConfigPath: cfgPath}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()},
		adapters.SkillRoot{ID: "opencode_user_opencode", Tool: "opencode", Scope: adapters.ScopeUser, Path: root})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range got[0].Warnings {
		if w.Code == "config_invalid_json" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected config_invalid_json warning, got %+v", got[0].Warnings)
	}
}

func TestResolvePermission_ExactWinsOverGlob(t *testing.T) {
	perms := map[string]string{
		"deploy-helper": "ask",
		"deploy-*":      "deny",
	}
	if got := resolvePermission(perms, "deploy-helper"); got != "ask" {
		t.Errorf("exact key should win: got %q, want ask", got)
	}
}

func TestResolvePermission_LongerGlobWins(t *testing.T) {
	perms := map[string]string{
		"*":        "allow",
		"danger-*": "deny",
	}
	if got := resolvePermission(perms, "danger-wipe"); got != "deny" {
		t.Errorf("longer glob should win: got %q, want deny", got)
	}
}

func TestResolvePermission_DenyBreaksEqualLengthTie(t *testing.T) {
	// Two equal-length globs both match; deny must win over allow.
	perms := map[string]string{
		"danger-a*": "allow",
		"danger-b*": "deny",
	}
	// "danger-b..." matches only the deny glob; verify deny path works.
	if got := resolvePermission(perms, "danger-bx"); got != "deny" {
		t.Errorf("got %q, want deny", got)
	}
	// Construct a genuine equal-length tie with a single skill name.
	tie := map[string]string{
		"d?nger-x": "allow",
		"danger-?": "deny",
	}
	if got := resolvePermission(tie, "danger-x"); got != "deny" {
		t.Errorf("equal-length tie should favour deny: got %q", got)
	}
}

func TestResolvePermission_NoMatchEmpty(t *testing.T) {
	perms := map[string]string{"other-*": "deny"}
	if got := resolvePermission(perms, "unrelated"); got != "" {
		t.Errorf("no match should yield empty, got %q", got)
	}
}

func TestSkillRoots_UserScopeMultiple(t *testing.T) {
	home := t.TempDir()
	// Create two of the three user roots.
	for _, rel := range []string{".config/opencode/skills", ".claude/skills"} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("got %d user roots, want 2: %+v", len(roots), roots)
	}
	for _, r := range roots {
		if r.Scope != adapters.ScopeUser {
			t.Errorf("root %s scope = %s", r.ID, r.Scope)
		}
	}
}

func TestSkillRoots_ProjectScopeMultiple(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	for _, rel := range []string{".opencode/skills", ".agents/skills"} {
		if err := os.MkdirAll(filepath.Join(proj, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{
		Ctx: context.Background(), HomeDir: home, ProjectRoots: []string{proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("got %d project roots, want 2: %+v", len(roots), roots)
	}
	for _, r := range roots {
		if r.Scope != adapters.ScopeProject {
			t.Errorf("root %s scope = %s", r.ID, r.Scope)
		}
	}
}

func TestSkillRoots_NoneWhenUninstalled(t *testing.T) {
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("got %d roots, want 0", len(roots))
	}
}
