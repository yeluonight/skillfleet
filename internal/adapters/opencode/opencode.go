// Package opencode implements the read-only OpenCode adapter
// (v1.0 §10.3).
//
// OpenCode is unusual in two ways:
//
//  1. It scans SIX roots — its own .opencode/skills plus the Claude
//     Code and Codex (.claude / .agents) locations — at both project
//     and user scope, because OpenCode deliberately reads skills
//     authored for those other tools.
//
//  2. Enable state comes from a JSON config's permission.skill map,
//     whose keys may be exact skill names OR globs:
//
//     {
//     "permission": {
//     "skill": {
//     "deploy-helper": "ask",
//     "dangerous-*":   "deny"
//     }
//     }
//     }
//
// Permission values map onto the shared vocabulary:
//
//	allow (or absent) -> on
//	ask               -> ask
//	deny              -> off
//
// Glob keys are matched with path.Match semantics. When both an exact
// key and a glob match a skill, the exact key wins; among competing
// globs the most specific (longest pattern) wins, with deny breaking
// ties so a safety-oriented "dangerous-*": "deny" is never silently
// overridden by a broader "*": "allow".
package opencode

import (
	"encoding/json"
	"os"
	"path"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "opencode"
	displayName = "OpenCode"

	defaultConfigPath = "~/.config/opencode/opencode.json"
)

// rootSpecs enumerate OpenCode's scan locations in deterministic order.
var rootSpecs = []adapters.RootSpec{
	{IDBase: "opencode_user_opencode", Scope: adapters.ScopeUser, Tmpl: "~/.config/opencode/skills"},
	{IDBase: "opencode_user_claude", Scope: adapters.ScopeUser, Tmpl: "~/.claude/skills"},
	{IDBase: "opencode_user_agents", Scope: adapters.ScopeUser, Tmpl: "~/.agents/skills", Shared: true},
	{IDBase: "opencode_project_opencode", Scope: adapters.ScopeProject, Tmpl: ".opencode/skills"},
	{IDBase: "opencode_project_claude", Scope: adapters.ScopeProject, Tmpl: ".claude/skills"},
	{IDBase: "opencode_project_agents", Scope: adapters.ScopeProject, Tmpl: ".agents/skills", Shared: true},
}

// Adapter is the read-only OpenCode adapter. ConfigPath overrides the
// permission config location (tests inject a fixture); empty uses the
// default ~/.config/opencode/opencode.json.
type Adapter struct {
	ConfigPath string
}

func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	return adapters.SkillRootsFromSpecs(sc, toolKey, rootSpecs)
}

// CandidateRoots suggests OpenCode's user skill roots for registration.
// Detected when its config dir exists or an `opencode` binary is on PATH.
func (a *Adapter) CandidateRoots(sc adapters.ScanContext) []adapters.CandidateRoot {
	detected := adapters.ConfigDirExists(sc.HomeDir, "~/.config/opencode") || adapters.BinaryOnPath("opencode")
	return adapters.BuildCandidateRootsFromRootSpecs(sc, toolKey, detected, rootSpecs)
}

func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	perms, cfgWarn := a.loadPermissions(sc)

	decode := func(skillName, _ string, _ skillmd.Result) (adapters.EffectiveState, string) {
		perm := resolvePermission(perms, skillName)
		switch perm {
		case "deny":
			return adapters.StateOff, "deny"
		case "ask":
			return adapters.StateAsk, "ask"
		default: // "allow" or unmatched
			return adapters.StateOn, "allow"
		}
	}

	skills, err := adapters.ScanStandardRoot(root, decode)
	if err != nil {
		return nil, err
	}
	if cfgWarn != nil && len(skills) > 0 {
		skills[0].Warnings = append(skills[0].Warnings, *cfgWarn)
	}
	return skills, nil
}

// permConfig is the subset of opencode.json the adapter reads.
type permConfig struct {
	Permission struct {
		Skill map[string]string `json:"skill"`
	} `json:"permission"`
}

// loadPermissions reads opencode.json and returns the skill→permission
// map. A missing config = empty map (every skill defaults to allow/on).
func (a *Adapter) loadPermissions(sc adapters.ScanContext) (map[string]string, *adapters.Warning) {
	cfgPath := a.ConfigPath
	if cfgPath == "" {
		expanded, err := adapters.ExpandHome(defaultConfigPath, sc.HomeDir)
		if err != nil {
			return nil, &adapters.Warning{Code: "config_path", Message: err.Error()}
		}
		cfgPath = expanded
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return map[string]string{}, &adapters.Warning{Code: "config_unreadable", Message: err.Error()}
	}

	var cfg permConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return map[string]string{}, &adapters.Warning{Code: "config_invalid_json", Message: err.Error()}
	}
	if cfg.Permission.Skill == nil {
		return map[string]string{}, nil
	}
	return cfg.Permission.Skill, nil
}

// resolvePermission returns the permission string for skillName.
//
// Precedence: an exact key always wins. Otherwise the matching glob
// with the longest pattern wins; if two globs of equal length match,
// "deny" beats "ask" beats "allow" so a safety rule is never weakened
// by an equally-specific permissive rule. No match -> "" (treated as
// allow by the caller).
func resolvePermission(perms map[string]string, skillName string) string {
	if v, ok := perms[skillName]; ok {
		return v
	}
	best := ""
	bestPat := ""
	for pattern, perm := range perms {
		// Skip exact (non-glob) keys — already handled above.
		ok, err := path.Match(pattern, skillName)
		if err != nil || !ok {
			continue
		}
		switch {
		case len(pattern) > len(bestPat):
			best, bestPat = perm, pattern
		case len(pattern) == len(bestPat) && permRank(perm) > permRank(best):
			best, bestPat = perm, pattern
		}
	}
	return best
}

// permRank orders permissions by restrictiveness for tie-breaking:
// deny (2) > ask (1) > allow/other (0).
func permRank(p string) int {
	switch p {
	case "deny":
		return 2
	case "ask":
		return 1
	default:
		return 0
	}
}
