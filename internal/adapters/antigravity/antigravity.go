// Package antigravity implements the read-only Antigravity adapter
// (v1.0 §10.4).
//
// Roots scanned:
//
//	<workspace-root>/.agents/skills  (project scope)
//	~/.gemini/antigravity/skills     (user scope)
//
// Antigravity exposes no per-skill enable config in a file the agent
// can read; a discovered skill directory is simply "available". The
// spec's "disabled" / "unknown" native states arise only from
// out-of-band signals SkillFleet does not have at scan time, so this
// adapter reports available/on for every directory it finds and leaves
// the richer states to a future config integration.
package antigravity

import (
	"path/filepath"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "antigravity"
	displayName = "Antigravity"

	nativeAvailable = "available"
)

// Adapter is the read-only Antigravity adapter.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	var roots []adapters.SkillRoot

	userPath, err := adapters.ExpandHome("~/.gemini/antigravity/skills", sc.HomeDir)
	if err != nil {
		return nil, err
	}
	if adapters.DirExists(userPath) {
		roots = append(roots, adapters.SkillRoot{
			ID: "antigravity_user", Tool: toolKey, Scope: adapters.ScopeUser, Path: userPath,
		})
	}
	for i, proj := range sc.ProjectRoots {
		p := filepath.Join(proj, ".agents", "skills")
		if adapters.DirExists(p) {
			roots = append(roots, adapters.SkillRoot{
				ID:    adapters.ProjectRootID("antigravity_project", i),
				Tool:  toolKey,
				Scope: adapters.ScopeProject,
				Path:  p,
			})
		}
	}
	return roots, nil
}

func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	return adapters.ScanStandardRoot(root, available)
}

// available reports every discovered skill as available/on.
func available(_, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
	return adapters.StateOn, nativeAvailable
}
