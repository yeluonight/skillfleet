// Package antigravitycli implements the read-only Antigravity CLI
// adapter (v1.0 §10.5).
//
// Roots scanned:
//
//	<workspace-root>/.agents/skills    (project scope)
//	~/.gemini/antigravity-cli/skills   (user scope)
//	~/.gemini/skills                   (user scope)
//
// Like the Antigravity GUI adapter, the CLI exposes no file-readable
// per-skill enable config, so every discovered directory is reported
// available/on.
package antigravitycli

import (
	"path/filepath"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "antigravity-cli"
	displayName = "Antigravity CLI"

	nativeAvailable = "available"
)

// Adapter is the read-only Antigravity CLI adapter.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	var roots []adapters.SkillRoot

	userSpecs := []struct {
		id  string
		rel string
	}{
		{"antigravitycli_user_cli", "~/.gemini/antigravity-cli/skills"},
		{"antigravitycli_user_gemini", "~/.gemini/skills"},
	}
	for _, spec := range userSpecs {
		p, err := adapters.ExpandHome(spec.rel, sc.HomeDir)
		if err != nil {
			return nil, err
		}
		if adapters.DirExists(p) {
			roots = append(roots, adapters.SkillRoot{
				ID: spec.id, Tool: toolKey, Scope: adapters.ScopeUser, Path: p,
			})
		}
	}

	for i, proj := range sc.ProjectRoots {
		p := filepath.Join(proj, ".agents", "skills")
		if adapters.DirExists(p) {
			roots = append(roots, adapters.SkillRoot{
				ID:    adapters.ProjectRootID("antigravitycli_project", i),
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

func available(_, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
	return adapters.StateOn, nativeAvailable
}
