// Package pi implements the read-only Pi Coding Agent adapter
// (v1.0 §10.6).
//
// Roots scanned:
//
//	~/.pi/agent/skills      (user scope)
//	~/.agents/skills        (user scope)
//	<project>/.pi/skills    (project scope)
//	<project>/.agents/skills (project scope)
//	<project>/skills        (project scope)
//
// The package.json -> pi.skills and settings.json -> skills[] indirection
// from the spec are deferred: Phase 3 covers the directory-convention
// roots, which are the common case. A discovered skill directory is
// reported available/on; Pi has no file-readable disable signal at
// scan time.
package pi

import (
	"path/filepath"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "pi"
	displayName = "Pi Coding Agent"

	nativeAvailable = "available"
)

// Adapter is the read-only Pi adapter.
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
		{"pi_user_agent", "~/.pi/agent/skills"},
		{"pi_user_agents", "~/.agents/skills"},
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

	// Project-scope conventions, in spec order.
	projectRels := []struct {
		idBase string
		rel    string
	}{
		{"pi_project_pi", ".pi/skills"},
		{"pi_project_agents", ".agents/skills"},
		{"pi_project_skills", "skills"},
	}
	for i, proj := range sc.ProjectRoots {
		for _, spec := range projectRels {
			p := filepath.Join(proj, filepath.FromSlash(spec.rel))
			if adapters.DirExists(p) {
				roots = append(roots, adapters.SkillRoot{
					ID:    adapters.ProjectRootID(spec.idBase, i),
					Tool:  toolKey,
					Scope: adapters.ScopeProject,
					Path:  p,
				})
			}
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
