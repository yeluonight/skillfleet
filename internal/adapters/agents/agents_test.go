package agents

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
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestAdapter_Identity(t *testing.T) {
	a := New()
	if a.Key() != "agents" || a.DisplayName() != "Shared Agent Skills" {
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

	a := New()
	sr := adapters.SkillRoot{
		ID: want.RootID, Tool: want.Tool, Scope: adapters.Scope(want.Scope),
		Path: filepath.Join(root, "user-skills"),
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
		if g.ContentSHA256 == "" {
			t.Errorf("[%s] ContentSHA256 empty", w.Name)
		}
	}
}

func TestSkillRoots_UserScope(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots, err := New().SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].ID != "agents_user" || roots[0].Scope != adapters.ScopeUser {
		t.Fatalf("roots = %+v", roots)
	}
}

func TestSkillRoots_ProjectScope(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots, err := New().SkillRoots(adapters.ScanContext{
		Ctx: context.Background(), HomeDir: home, ProjectRoots: []string{proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].ID != "agents_project_0" || roots[0].Scope != adapters.ScopeProject {
		t.Fatalf("roots = %+v", roots)
	}
}

func TestSkillRoots_NoneWhenUninstalled(t *testing.T) {
	roots, err := New().SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("got %d roots, want 0", len(roots))
	}
}

func TestCandidateRoots(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	cands := New().CandidateRoots(adapters.ScanContext{HomeDir: home})
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	c := cands[0]
	if c.ToolKey != "agents" || c.Scope != adapters.ScopeUser {
		t.Errorf("candidate identity = %q/%q", c.ToolKey, c.Scope)
	}
	if c.DisplayTmpl != "~/.agents/skills" {
		t.Errorf("displayTmpl = %q", c.DisplayTmpl)
	}
	if c.Path != filepath.Join(home, ".agents", "skills") {
		t.Errorf("path = %q", c.Path)
	}
	if c.Exists {
		t.Error("skills/ does not exist; Exists should be false")
	}
	if !c.ToolDetected {
		t.Error("~/.agents present; ToolDetected should be true")
	}
	if !c.Shared {
		t.Error("shared directory candidate should be Shared")
	}
}
