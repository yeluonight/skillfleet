package codex

import (
	"context"
	"encoding/json"
	"fmt"
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
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestAdapter_Identity(t *testing.T) {
	a := New()
	if a.Key() != "codex" || a.DisplayName() != "Codex" {
		t.Errorf("identity = %q / %q", a.Key(), a.DisplayName())
	}
}

func TestScanSkills_MatchesFixtureExpected(t *testing.T) {
	root := fixtureRoot(t)
	skillsDir := filepath.Join(root, "user-skills")

	// Build a config.toml dynamically: legacy-deploy disabled,
	// test-gen explicitly enabled, build-runner absent (default on).
	// Paths must be the absolute SKILL.md paths the adapter will look
	// up, so we point them at the live fixture tree.
	cfg := fmt.Sprintf(`
[[skills.config]]
path = %q
enabled = false

[[skills.config]]
path = %q
enabled = true
`,
		filepath.Join(skillsDir, "legacy-deploy", "SKILL.md"),
		filepath.Join(skillsDir, "test-gen", "SKILL.md"),
	)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load golden file.
	raw, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want expectedDoc
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{ConfigPath: cfgPath}
	sr := adapters.SkillRoot{
		ID:    want.RootID,
		Tool:  want.Tool,
		Scope: adapters.Scope(want.Scope),
		Path:  skillsDir,
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

func TestScanSkills_MissingConfigDefaultsAllEnabled(t *testing.T) {
	root := filepath.Join(fixtureRoot(t), "user-skills")
	a := &Adapter{ConfigPath: filepath.Join(t.TempDir(), "does-not-exist.toml")}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()},
		adapters.SkillRoot{ID: "codex_user", Tool: "codex", Scope: adapters.ScopeUser, Path: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g.EffectiveState != adapters.StateOn {
			t.Errorf("[%s] state = %q, want on (no config = all enabled)", g.Name, g.EffectiveState)
		}
		if len(g.Warnings) != 0 {
			t.Errorf("[%s] missing config should not warn, got %+v", g.Name, g.Warnings)
		}
	}
}

func TestScanSkills_InvalidTOMLWarns(t *testing.T) {
	root := filepath.Join(fixtureRoot(t), "user-skills")
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("this is = = not valid toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{ConfigPath: cfgPath}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()},
		adapters.SkillRoot{ID: "codex_user", Tool: "codex", Scope: adapters.ScopeUser, Path: root})
	if err != nil {
		t.Fatal(err)
	}
	// The config warning is attached to the first skill.
	var found bool
	for _, w := range got[0].Warnings {
		if w.Code == "config_invalid_toml" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected config_invalid_toml warning, got %+v", got[0].Warnings)
	}
}

func TestSkillRoots_UserAndSystem(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	// At least the user root resolves; /etc/codex/skills may or may not
	// exist on the test host, so assert the user root is present.
	var hasUser bool
	for _, r := range roots {
		if r.ID == "codex_user" && r.Scope == adapters.ScopeUser {
			hasUser = true
		}
	}
	if !hasUser {
		t.Errorf("user root missing: %+v", roots)
	}
}

func TestSkillRoots_ProjectScope(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{
		Ctx: context.Background(), HomeDir: home, ProjectRoots: []string{proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	var hasProject bool
	for _, r := range roots {
		if r.Scope == adapters.ScopeProject && r.ID == "codex_project_0" {
			hasProject = true
		}
	}
	if !hasProject {
		t.Errorf("project root missing: %+v", roots)
	}
}

func TestCandidateRoots(t *testing.T) {
	home := t.TempDir()
	cands := New().CandidateRoots(adapters.ScanContext{HomeDir: home})
	// Two specs: shared ~/.agents/skills (user) + /etc/codex/skills (system).
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}

	var shared, system *adapters.CandidateRoot
	for i := range cands {
		switch cands[i].Scope {
		case adapters.ScopeUser:
			shared = &cands[i]
		case adapters.ScopeSystem:
			system = &cands[i]
		}
	}
	if shared == nil || system == nil {
		t.Fatalf("expected one user + one system candidate, got %+v", cands)
	}
	if shared.DisplayTmpl != "~/.agents/skills" || !shared.Shared {
		t.Errorf("user candidate should be the shared ~/.agents/skills, got %q shared=%v", shared.DisplayTmpl, shared.Shared)
	}
	if system.Path != "/etc/codex/skills" || system.Shared {
		t.Errorf("system candidate = %q shared=%v", system.Path, system.Shared)
	}
}
