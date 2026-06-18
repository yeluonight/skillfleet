// Package pi implements the read-only Pi Coding Agent adapter
// (v1.0 §10.6).
//
// Roots scanned:
//
//	~/.pi/agent/skills   (user scope)
//	<project>/.pi/skills (project scope)
//	<project>/skills     (project scope)
//
// The package.json -> pi.skills and settings.json -> skills[] indirection
// from the spec are deferred: Phase 3 covers the directory-convention
// roots, which are the common case. A discovered skill directory is
// reported available/on; Pi has no file-readable disable signal at
// scan time. Shared .agents/skills content is represented by the
// dedicated agents adapter, not duplicated here.
package pi

import (
	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "pi"
	displayName = "Pi Coding Agent"

	nativeAvailable = "available"
)

// rootSpecs enumerates Pi's scan locations in deterministic order.
var rootSpecs = []adapters.RootSpec{
	{IDBase: "pi_user_agent", Scope: adapters.ScopeUser, Tmpl: "~/.pi/agent/skills"},
	{IDBase: "pi_project_pi", Scope: adapters.ScopeProject, Tmpl: ".pi/skills"},
	{IDBase: "pi_project_skills", Scope: adapters.ScopeProject, Tmpl: "skills"},
}

// Adapter is the read-only Pi adapter.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	return adapters.SkillRootsFromSpecs(sc, toolKey, rootSpecs)
}

// CandidateRoots suggests Pi's user skill roots for registration.
// Detected when ~/.pi exists or a `pi` binary is on PATH.
func (a *Adapter) CandidateRoots(sc adapters.ScanContext) []adapters.CandidateRoot {
	detected := adapters.ConfigDirExists(sc.HomeDir, "~/.pi") || adapters.BinaryOnPath("pi")
	return adapters.BuildCandidateRootsFromRootSpecs(sc, toolKey, detected, rootSpecs)
}

func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	return adapters.ScanStandardRoot(root, available)
}

func available(_, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
	return adapters.StateOn, nativeAvailable
}
