// Package antigravitycli implements the read-only Antigravity CLI
// adapter (v1.0 §10.5).
//
// Roots scanned:
//
//	~/.gemini/antigravity-cli/skills   (user scope)
//	~/.gemini/skills                   (user scope)
//
// Like the Antigravity GUI adapter, the CLI exposes no file-readable
// per-skill enable config, so every discovered directory is reported
// available/on. Shared .agents/skills content is represented by the
// dedicated agents adapter, not duplicated here.
package antigravitycli

import (
	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "antigravity-cli"
	displayName = "Antigravity CLI"

	nativeAvailable = "available"
)

// rootSpecs enumerates Antigravity CLI's scan locations in deterministic order.
var rootSpecs = []adapters.RootSpec{
	{IDBase: "antigravitycli_user_cli", Scope: adapters.ScopeUser, Tmpl: "~/.gemini/antigravity-cli/skills"},
	{IDBase: "antigravitycli_user_gemini", Scope: adapters.ScopeUser, Tmpl: "~/.gemini/skills"},
}

// Adapter is the read-only Antigravity CLI adapter.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	return adapters.SkillRootsFromSpecs(sc, toolKey, rootSpecs)
}

// CandidateRoots suggests Antigravity CLI's user skill roots for
// registration. Detected when ~/.gemini exists.
func (a *Adapter) CandidateRoots(sc adapters.ScanContext) []adapters.CandidateRoot {
	detected := adapters.ConfigDirExists(sc.HomeDir, "~/.gemini")
	return adapters.BuildCandidateRootsFromRootSpecs(sc, toolKey, detected, rootSpecs)
}

func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	return adapters.ScanStandardRoot(root, available)
}

func available(_, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
	return adapters.StateOn, nativeAvailable
}
