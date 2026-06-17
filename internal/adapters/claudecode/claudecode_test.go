package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// mkResult builds a skillmd.Result carrying just the frontmatter map,
// the only field deriveState reads.
func mkResult(fm map[string]any) skillmd.Result {
	return skillmd.Result{Frontmatter: fm}
}

// expectedSkill mirrors the stable subset of a DiscoveredSkill that
// fixtures/claude-code/expected.json pins. content_sha256 / file_count
// are intentionally excluded — they're asserted to be non-empty, not
// fixed, so editing fixture prose doesn't churn the golden file.
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

// fixtureRoot is the absolute path to fixtures/claude-code.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	// test runs from the package dir; fixtures live at repo root.
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "claude-code"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestAdapter_Identity(t *testing.T) {
	a := New()
	if a.Key() != "claude-code" {
		t.Errorf("Key = %q", a.Key())
	}
	if a.DisplayName() != "Claude Code" {
		t.Errorf("DisplayName = %q", a.DisplayName())
	}
}

func TestScanSkills_MatchesFixtureExpected(t *testing.T) {
	root := fixtureRoot(t)

	// Load the golden file.
	raw, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want expectedDoc
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	// Scan the user-skills fixture directory directly as a root.
	sr := adapters.SkillRoot{
		ID:    want.RootID,
		Tool:  want.Tool,
		Scope: adapters.Scope(want.Scope),
		Path:  filepath.Join(root, "user-skills"),
	}
	a := New()
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()}, sr)
	if err != nil {
		t.Fatal(err)
	}

	// Sort both sides by name for a stable comparison.
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })

	if len(got) != len(want.Skills) {
		t.Fatalf("got %d skills, want %d", len(got), len(want.Skills))
	}
	for i, w := range want.Skills {
		g := got[i]
		if g.Name != w.Name {
			t.Errorf("[%d] name = %q, want %q", i, g.Name, w.Name)
		}
		if g.HasSkillMD != w.HasSkillMD {
			t.Errorf("[%s] HasSkillMD = %v, want %v", w.Name, g.HasSkillMD, w.HasSkillMD)
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
		// content fingerprint is non-empty + populated for real dirs.
		if g.ContentSHA256 == "" {
			t.Errorf("[%s] ContentSHA256 empty", w.Name)
		}
		if g.FileCount == 0 {
			t.Errorf("[%s] FileCount = 0", w.Name)
		}
		if g.RootID != want.RootID {
			t.Errorf("[%s] RootID = %q, want %q", w.Name, g.RootID, want.RootID)
		}
	}
}

func TestSkillRoots_UserScope(t *testing.T) {
	// Point HomeDir at the fixture's parent so ~/.claude/skills resolves
	// to a real directory. We build a temp home with a .claude/skills
	// symlink-free copy by pointing at a temp tree.
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".claude", "skills", "x")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"),
		[]byte("---\nname: x\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{Ctx: context.Background(), HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(roots))
	}
	if roots[0].ID != "claude_user" || roots[0].Scope != adapters.ScopeUser {
		t.Errorf("root = %+v", roots[0])
	}
}

func TestSkillRoots_ProjectScope(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{
		Ctx:          context.Background(),
		HomeDir:      home,
		ProjectRoots: []string{proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	// home has no ~/.claude/skills so only the project root shows.
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1 (project only)", len(roots))
	}
	if roots[0].Scope != adapters.ScopeProject || roots[0].ID != "claude_project_0" {
		t.Errorf("root = %+v", roots[0])
	}
}

func TestSkillRoots_NoneWhenUninstalled(t *testing.T) {
	a := New()
	roots, err := a.SkillRoots(adapters.ScanContext{
		Ctx:     context.Background(),
		HomeDir: t.TempDir(), // empty home, no .claude
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("got %d roots, want 0 for uninstalled tool", len(roots))
	}
}

func TestDeriveState_AllQuadrants(t *testing.T) {
	cases := []struct {
		name          string
		modelDisabled any
		userInvocable any
		wantEff       adapters.EffectiveState
		wantNative    string
	}{
		{"defaults", nil, nil, adapters.StateOn, nativeAvailable},
		{"manual", true, true, adapters.StateUserInvocableOnly, nativeManualOnly},
		{"disabled", true, false, adapters.StateOff, nativeDisabled},
		{"name-only", false, false, adapters.StateNameOnly, nativeNameOnly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fm := map[string]any{}
			if c.modelDisabled != nil {
				fm["disable-model-invocation"] = c.modelDisabled
			}
			if c.userInvocable != nil {
				fm["user-invocable"] = c.userInvocable
			}
			eff, native := deriveState("x", "/p", mkResult(fm))
			if eff != c.wantEff {
				t.Errorf("eff = %q, want %q", eff, c.wantEff)
			}
			if native != c.wantNative {
				t.Errorf("native = %q, want %q", native, c.wantNative)
			}
		})
	}
}

func TestDeriveState_NilFrontmatterDefaultsAvailable(t *testing.T) {
	eff, native := deriveState("x", "/p", mkResult(nil))
	if eff != adapters.StateOn || native != nativeAvailable {
		t.Errorf("nil frontmatter: eff=%q native=%q, want on/available", eff, native)
	}
}

// writeSkill creates <root>/<name>/SKILL.md with the given frontmatter
// body and returns the root dir. Used by the skillOverrides tests, which
// need a real on-disk tree (ScanSkills walks the filesystem).
func writeSkill(t *testing.T, root, name, frontmatter string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: d\n" + frontmatter + "---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanOne scans a skills root with the given adapter and returns the
// discovered skill of the given name.
func scanOne(t *testing.T, a *Adapter, skillsDir, name string) adapters.DiscoveredSkill {
	t.Helper()
	sr := adapters.SkillRoot{
		ID: "claude_user", Tool: toolKey, Scope: adapters.ScopeUser, Path: skillsDir,
	}
	got, err := a.ScanSkills(adapters.ScanContext{Ctx: context.Background()}, sr)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("skill %q not found in scan of %d skills", name, len(got))
	return adapters.DiscoveredSkill{}
}

// writeSettings writes a settings.json with the given skillOverrides map
// (and an unrelated key, to prove the adapter ignores everything else).
func writeSettings(t *testing.T, path string, overrides map[string]string) {
	t.Helper()
	doc := map[string]any{
		"permissions":    map[string]any{"deny": []string{"X"}}, // unrelated key
		"skillOverrides": overrides,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanSkills_OverrideWinsOverFrontmatter is the read-write-parity
// guard: a skill whose SKILL.md frontmatter says "fully on" but whose
// settings.json override says "off" must report off — otherwise a skill
// SkillFleet just disabled (Phase 9 writes the override) would still scan
// as on. Each of the four override values is checked against an
// on-by-frontmatter skill so the override is always the deciding factor.
func TestScanSkills_OverrideWinsOverFrontmatter(t *testing.T) {
	cases := []struct {
		override   string
		wantEff    adapters.EffectiveState
		wantNative string
	}{
		{"off", adapters.StateOff, nativeDisabled},
		{"on", adapters.StateOn, nativeAvailable},
		{"name-only", adapters.StateNameOnly, nativeNameOnly},
		{"user-invocable-only", adapters.StateUserInvocableOnly, nativeManualOnly},
	}
	for _, c := range cases {
		t.Run(c.override, func(t *testing.T) {
			home := t.TempDir()
			claude := filepath.Join(home, ".claude")
			skills := filepath.Join(claude, "skills")
			// Frontmatter says on/available; override should override it.
			writeSkill(t, skills, "deploy", "")
			writeSettings(t, filepath.Join(claude, settingsFileName),
				map[string]string{"deploy": c.override})

			a := New() // SettingsPath empty → derived from root parent
			g := scanOne(t, a, skills, "deploy")
			if g.EffectiveState != c.wantEff {
				t.Errorf("override %q: eff = %q, want %q", c.override, g.EffectiveState, c.wantEff)
			}
			if g.NativeState != c.wantNative {
				t.Errorf("override %q: native = %q, want %q", c.override, g.NativeState, c.wantNative)
			}
		})
	}
}

// TestScanSkills_NoOverrideUsesFrontmatter proves the fallback: with no
// matching override, the frontmatter 2×2 still decides (so the existing
// expected.json behaviour is untouched). Also covers a present-but-empty
// skillOverrides and a skill not listed in a non-empty map.
func TestScanSkills_NoOverrideUsesFrontmatter(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	skills := filepath.Join(claude, "skills")
	// disabled-by-frontmatter skill, NOT in the override map.
	writeSkill(t, skills, "manual", "disable-model-invocation: true\nuser-invocable: true\n")
	writeSettings(t, filepath.Join(claude, settingsFileName),
		map[string]string{"some-other-skill": "off"})

	a := New()
	g := scanOne(t, a, skills, "manual")
	// Frontmatter (model off, user on) → user-invocable-only / manual_only.
	if g.EffectiveState != adapters.StateUserInvocableOnly || g.NativeState != nativeManualOnly {
		t.Errorf("uncovered skill: eff=%q native=%q, want user-invocable-only/manual_only",
			g.EffectiveState, g.NativeState)
	}
}

// TestScanSkills_NoSettingsFileUsesFrontmatter proves a missing
// settings.json is silent (no warning, frontmatter decides).
func TestScanSkills_NoSettingsFileUsesFrontmatter(t *testing.T) {
	home := t.TempDir()
	skills := filepath.Join(home, ".claude", "skills")
	writeSkill(t, skills, "plain", "") // defaults → on/available

	a := New()
	g := scanOne(t, a, skills, "plain")
	if g.EffectiveState != adapters.StateOn || g.NativeState != nativeAvailable {
		t.Errorf("no settings: eff=%q native=%q, want on/available", g.EffectiveState, g.NativeState)
	}
	if len(g.Warnings) != 0 {
		t.Errorf("missing settings.json should be silent, got warnings %+v", g.Warnings)
	}
}

// TestScanSkills_UnknownOverrideValueFallsBack proves a garbage override
// value is ignored (falls back to frontmatter), not blindly trusted.
func TestScanSkills_UnknownOverrideValueFallsBack(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	skills := filepath.Join(claude, "skills")
	writeSkill(t, skills, "deploy", "") // frontmatter → on
	writeSettings(t, filepath.Join(claude, settingsFileName),
		map[string]string{"deploy": "bogus-value"})

	a := New()
	g := scanOne(t, a, skills, "deploy")
	if g.EffectiveState != adapters.StateOn {
		t.Errorf("unknown override value should fall back to frontmatter on, got %q", g.EffectiveState)
	}
}

// TestScanSkills_InvalidSettingsJSONWarns proves an unparseable
// settings.json surfaces a warning (once) and falls back to frontmatter
// rather than aborting the scan.
func TestScanSkills_InvalidSettingsJSONWarns(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	skills := filepath.Join(claude, "skills")
	writeSkill(t, skills, "deploy", "")
	if err := os.WriteFile(filepath.Join(claude, settingsFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	g := scanOne(t, a, skills, "deploy")
	if g.EffectiveState != adapters.StateOn {
		t.Errorf("invalid settings should fall back to frontmatter on, got %q", g.EffectiveState)
	}
	if len(g.Warnings) == 0 {
		t.Error("invalid settings.json should attach a warning")
	}
}

// TestOverrideState_Mapping locks the override-value → (effective, native)
// table directly, including the invalid sentinel.
func TestOverrideState_Mapping(t *testing.T) {
	cases := []struct {
		in         string
		wantEff    adapters.EffectiveState
		wantNative string
		wantValid  bool
	}{
		{"on", adapters.StateOn, nativeAvailable, true},
		{"off", adapters.StateOff, nativeDisabled, true},
		{"name-only", adapters.StateNameOnly, nativeNameOnly, true},
		{"user-invocable-only", adapters.StateUserInvocableOnly, nativeManualOnly, true},
		{"", adapters.StateUnknown, "", false},
		{"ask", adapters.StateUnknown, "", false}, // opencode-only value, invalid here
	}
	for _, c := range cases {
		eff, native, valid := overrideState(c.in)
		if eff != c.wantEff || native != c.wantNative || valid != c.wantValid {
			t.Errorf("overrideState(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, eff, native, valid, c.wantEff, c.wantNative, c.wantValid)
		}
	}
}

func TestCandidateRoots(t *testing.T) {
	home := t.TempDir()
	// ~/.claude present (tool detected) but skills/ absent (not Exists).
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	cands := New().CandidateRoots(adapters.ScanContext{HomeDir: home})
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	c := cands[0]
	if c.ToolKey != "claude-code" || c.Scope != adapters.ScopeUser {
		t.Errorf("candidate identity = %q/%q", c.ToolKey, c.Scope)
	}
	if c.DisplayTmpl != "~/.claude/skills" {
		t.Errorf("displayTmpl = %q", c.DisplayTmpl)
	}
	if c.Path != filepath.Join(home, ".claude", "skills") {
		t.Errorf("path = %q", c.Path)
	}
	if c.Exists {
		t.Error("skills/ does not exist; Exists should be false")
	}
	if !c.ToolDetected {
		t.Error("~/.claude present; ToolDetected should be true")
	}
	if c.Shared {
		t.Error("Claude Code never reads the shared dir; Shared should be false")
	}
}
