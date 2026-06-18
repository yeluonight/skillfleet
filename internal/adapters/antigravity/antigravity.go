// Package antigravity implements the read-only Antigravity adapter
// (v1.0 §10.4).
//
// Roots scanned:
//
//	~/.gemini/antigravity/skills  (user scope)
//
// Antigravity exposes no per-skill enable config in a file the agent
// can read; a discovered skill directory is simply "available". The
// spec's "disabled" / "unknown" native states arise only from
// out-of-band signals SkillFleet does not have at scan time, so this
// adapter reports available/on for every directory it finds and leaves
// the richer states to a future config integration. Shared .agents/skills
// content is represented by the dedicated agents adapter, not duplicated here.
package antigravity

import (
	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "antigravity"
	displayName = "Antigravity"

	nativeAvailable = "available"
)

// rootSpecs enumerates Antigravity's scan locations in deterministic order.
var rootSpecs = []adapters.RootSpec{
	{IDBase: "antigravity_user", Scope: adapters.ScopeUser, Tmpl: "~/.gemini/antigravity/skills"},
}

// Adapter is the read-only Antigravity adapter.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	return adapters.SkillRootsFromSpecs(sc, toolKey, rootSpecs)
}

// CandidateRoots suggests Antigravity's user skill root for registration.
// Detected when ~/.gemini exists.
func (a *Adapter) CandidateRoots(sc adapters.ScanContext) []adapters.CandidateRoot {
	detected := adapters.ConfigDirExists(sc.HomeDir, "~/.gemini")
	return adapters.BuildCandidateRootsFromRootSpecs(sc, toolKey, detected, rootSpecs)
}

func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	return adapters.ScanStandardRoot(root, available)
}

// available reports every discovered skill as available/on.
func available(_, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
	return adapters.StateOn, nativeAvailable
}
