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

// TestScanSkills_UnconsumedCodexRoot verifies that skills found under the
// codex_user_codex root (~/.codex/skills) are reported as "unknown" with a
// codex_unconsumed_root warning, not "on/available": Codex's CLI does not read
// that directory, so claiming it would be enabled is a lie.
func TestScanSkills_UnconsumedCodexRoot(t *testing.T) {
	root := filepath.Join(fixtureRoot(t), "user-skills")
	a := &Adapter{ConfigPath: filepath.Join(t.TempDir(), "does-not-exist.toml")}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()},
		adapters.SkillRoot{ID: "codex_user_codex", Tool: "codex", Scope: adapters.ScopeUser, Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected skills under the unconsumed root, got none")
	}
	for _, g := range got {
		if g.EffectiveState != adapters.StateUnknown {
			t.Errorf("[%s] state = %q, want unknown (unconsumed root)", g.Name, g.EffectiveState)
		}
		if g.NativeState != "unconsumed_by_tool" {
			t.Errorf("[%s] native state = %q, want unconsumed_by_tool", g.Name, g.NativeState)
		}
		var found bool
		for _, w := range g.Warnings {
			if w.Code == "codex_unconsumed_root" {
				found = true
			}
		}
		if !found {
			t.Errorf("[%s] missing codex_unconsumed_root warning, got %+v", g.Name, g.Warnings)
		}
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
	// Three specs: shared ~/.agents/skills (user) + unconsumed ~/.codex/skills
	// (user) + /etc/codex/skills (system).
	if len(cands) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(cands), cands)
	}

	var shared, unconsumed, system *adapters.CandidateRoot
	for i := range cands {
		c := &cands[i]
		switch {
		case c.Scope == adapters.ScopeUser && c.Shared:
			shared = c
		case c.Scope == adapters.ScopeUser && c.Unconsumed:
			unconsumed = c
		case c.Scope == adapters.ScopeSystem:
			system = c
		}
	}
	if shared == nil || unconsumed == nil || system == nil {
		t.Fatalf("expected shared user + unconsumed user + system, got %+v", cands)
	}
	if shared.DisplayTmpl != "~/.agents/skills" || !shared.Shared || shared.Unconsumed {
		t.Errorf("shared user candidate = %q shared=%v unconsumed=%v", shared.DisplayTmpl, shared.Shared, shared.Unconsumed)
	}
	if unconsumed.DisplayTmpl != "~/.codex/skills" || unconsumed.Shared || !unconsumed.Unconsumed {
		t.Errorf("unconsumed user candidate = %q shared=%v unconsumed=%v", unconsumed.DisplayTmpl, unconsumed.Shared, unconsumed.Unconsumed)
	}
	if system.Path != "/etc/codex/skills" || system.Shared || system.Unconsumed {
		t.Errorf("system candidate = %q shared=%v unconsumed=%v", system.Path, system.Shared, system.Unconsumed)
	}
}
