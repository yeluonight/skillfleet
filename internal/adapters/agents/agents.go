// Package agents implements the Shared Agent Skills adapter.
//
// Shared Agent Skills is the cross-tool .agents/skills directory used by
// multiple IDEs / CLIs. Unlike tool-specific adapters, this adapter is not a
// state authority: it reports content and deployment targets, but per-tool
// enable/disable state still belongs to concrete readers such as Codex or
// OpenCode.
package agents

import (
	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "agents"
	displayName = "Shared Agent Skills"

	nativeUnknown = "unknown"
)

// rootSpecs enumerates the shared .agents/skills locations in deterministic
// order. The user root is a deploy target this phase; project scope stays scan
// only until project roots are wired through the agent inventory loop.
var rootSpecs = []adapters.RootSpec{
	{IDBase: "agents_user", Scope: adapters.ScopeUser, Tmpl: "~/.agents/skills", Shared: true},
	{IDBase: "agents_project", Scope: adapters.ScopeProject, Tmpl: ".agents/skills", Shared: true},
}

// Adapter is the read-only Shared Agent Skills adapter.
type Adapter struct{}

// New returns a ready adapter.
func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

// SkillRoots resolves existing shared roots. Missing directories are omitted so
// an unconfigured shared directory yields no inventory rows.
func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	return adapters.SkillRootsFromSpecs(sc, toolKey, rootSpecs)
}

// CandidateRoots suggests the user shared directory for registration. Detection
// is only a soft hint; the shared directory is useful even when no specific
// reader's config directory is present yet.
func (a *Adapter) CandidateRoots(sc adapters.ScanContext) []adapters.CandidateRoot {
	detected := adapters.ConfigDirExists(sc.HomeDir, "~/.agents")
	return adapters.BuildCandidateRootsFromRootSpecs(sc, toolKey, detected, rootSpecs)
}

// ScanSkills walks a standard skill root but reports unknown state: this adapter
// is a content/deployment view, not a tool-specific enable/disable overlay.
func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	return adapters.ScanStandardRoot(root, unknown)
}

func unknown(_, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
	return adapters.StateUnknown, nativeUnknown
}
