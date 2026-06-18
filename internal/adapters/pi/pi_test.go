package pi

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

func TestAdapter_Identity(t *testing.T) {
	a := New()
	if a.Key() != "pi" || a.DisplayName() != "Pi Coding Agent" {
		t.Errorf("identity = %q / %q", a.Key(), a.DisplayName())
	}
}

func TestScanSkills_MatchesFixtureExpected(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "pi"))
	if err != nil {
		t.Fatal(err)
	}
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

func TestSkillRoots_UserScopes(t *testing.T) {
	home := t.TempDir()
	for _, rel := range []string{".pi/agent/skills", ".agents/skills"} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].ID != "pi_user_agent" {
		t.Fatalf("got roots %+v, want only pi_user_agent", roots)
	}
}

func TestSkillRoots_ProjectScopes(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	// Create Pi's two project conventions plus .agents/skills, which is owned
	// by the dedicated agents adapter and intentionally ignored here.
	for _, rel := range []string{".pi/skills", ".agents/skills", "skills"} {
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
