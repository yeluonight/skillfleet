// Package adapters defines the read-only contract every tool adapter
// implements during Phase 3 (v1.0 §10), plus the shared types and
// helpers each adapter builds on.
//
// Scope of Phase 3: discovery + read only. The adapters locate skill
// roots, walk them, parse SKILL.md, fingerprint the directory, and map
// the tool's native enable/disable vocabulary into a common
// EffectiveState. They do NOT install, modify, or plan changes — the
// PlanInstall / PlanStateChange halves of the v1.0 interface arrive in
// Phase 4+ and live in a separate write-side interface so the
// read-only adapters stay small and obviously side-effect-free.
//
// Each concrete adapter ships in its own subpackage
// (internal/adapters/<tool>) and is fixture-tested per v2.0 §5.8: the
// test feeds a fixtures/<tool>/ tree and asserts the discovered output
// against fixtures/<tool>/expected.json.
package adapters

import (
	"context"

	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// Scope is where a skill root lives relative to the machine / project.
type Scope string

const (
	// ScopeUser is a per-user root (e.g. ~/.claude/skills).
	ScopeUser Scope = "user"
	// ScopeProject is a per-project root (e.g. <repo>/.claude/skills).
	ScopeProject Scope = "project"
	// ScopeSystem is a machine-wide root (e.g. /etc/codex/skills).
	ScopeSystem Scope = "system"
)

// EffectiveState is the normalised enable/disable vocabulary across
// every tool (v1.0 §10.1 mapping table). Individual adapters map their
// native states into this set; not every adapter uses every value.
type EffectiveState string

const (
	// StateOn: skill is fully active (model-invocable + listed).
	StateOn EffectiveState = "on"
	// StateOff: skill is disabled.
	StateOff EffectiveState = "off"
	// StateNameOnly: name surfaced to the model but body withheld
	// until invoked (Claude Code "name_only").
	StateNameOnly EffectiveState = "name-only"
	// StateUserInvocableOnly: only user can invoke; model cannot
	// auto-trigger (Claude Code "manual_only").
	StateUserInvocableOnly EffectiveState = "user-invocable-only"
	// StateAsk: invocation requires confirmation (OpenCode "ask").
	StateAsk EffectiveState = "ask"
	// StateUnknown: adapter could not determine the state.
	StateUnknown EffectiveState = "unknown"
)

// SkillRoot is a directory an adapter scans for skills. The Path is
// absolute and already expanded (no leading ~). ID is a stable
// adapter-local identifier used in inventory rows.
type SkillRoot struct {
	ID    string // e.g. "claude_user"
	Tool  string // adapter Key(), denormalised for convenience
	Scope Scope
	Path  string // absolute, ~-expanded
}

// DiscoveredSkill is one skill found under a SkillRoot. It bundles the
// parsed SKILL.md, the directory fingerprint, the adapter-resolved
// effective state, and any warnings (from the parser or the adapter
// itself) so the inventory matrix can render the full picture.
type DiscoveredSkill struct {
	// Name is the authoritative skill name. Adapters use the folder
	// name (v1.0 §7.6: folder wins over frontmatter.name).
	Name string

	// RootID links back to the SkillRoot that contained this skill.
	RootID string

	// Path is the absolute skill directory.
	Path string

	// SkillMD is the parsed SKILL.md (may carry its own warnings).
	// Zero value when the directory has no readable SKILL.md.
	SkillMD skillmd.Result

	// HasSkillMD is false when no SKILL.md was found / readable.
	HasSkillMD bool

	// ContentSHA256 is the directory fingerprint (internal/fingerprint).
	ContentSHA256 string

	// FileCount / TotalBytes mirror the fingerprint summary.
	FileCount  int
	TotalBytes int64

	// EffectiveState is the adapter's read of the tool's native
	// enable/disable status for this skill.
	EffectiveState EffectiveState

	// NativeState is the raw, tool-specific status string the adapter
	// read (e.g. "manual_only", "ask", "disabled"). Preserved verbatim
	// for the WebUI's "native state" column.
	NativeState string

	// Warnings collects adapter-level findings (not SKILL.md parser
	// warnings, which live in SkillMD.Warnings).
	Warnings []Warning
}

// Warning is an adapter-level non-fatal finding.
type Warning struct {
	Code    string
	Message string
}

// ScanContext carries the inputs an adapter needs to resolve roots.
// HomeDir is the user's home (injected so tests don't depend on the
// runner's $HOME). ProjectRoots are absolute project directories the
// operator has registered for project-scope scanning; empty means
// "user/system scopes only".
type ScanContext struct {
	Ctx          context.Context
	HomeDir      string
	ProjectRoots []string
}

// ReadOnlyAdapter is the Phase 3 contract. The write-side methods
// (PlanInstall / PlanStateChange) from v1.0 §10 are intentionally
// excluded here and will be added as a separate interface in Phase 4.
type ReadOnlyAdapter interface {
	// Key returns the stable tool identifier (e.g. "claude-code").
	Key() string

	// DisplayName returns the human-friendly tool name.
	DisplayName() string

	// SkillRoots resolves the directories this adapter should scan for
	// the given context. Roots whose path does not exist are omitted
	// (a tool that isn't installed yields zero roots, not an error).
	SkillRoots(sc ScanContext) ([]SkillRoot, error)

	// ScanSkills walks one root and returns the skills it contains.
	// Per-skill errors are surfaced as Warnings on the DiscoveredSkill
	// rather than aborting the whole scan, so one malformed skill does
	// not blind the operator to its neighbours.
	ScanSkills(sc ScanContext, root SkillRoot) ([]DiscoveredSkill, error)
}
