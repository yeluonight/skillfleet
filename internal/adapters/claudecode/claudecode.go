// Package claudecode implements the read-only Claude Code adapter
// (v1.0 §10.1).
//
// Roots scanned:
//
//	~/.claude/skills/<name>/SKILL.md          (user scope)
//	<project>/.claude/skills/<name>/SKILL.md  (project scope)
//
// Native-state derivation. Claude Code resolves a skill's state from TWO
// sources, in priority order:
//
//  1. skillOverrides (settings.json) — the authoritative, out-of-band
//     operator override. A `.claude/settings.json` sibling of the skills
//     dir carries a `skillOverrides` map keyed by skill name, whose
//     values are exactly the shared EffectiveState vocabulary
//     ("on" / "name-only" / "user-invocable-only" / "off"). This is the
//     mechanism SkillFleet writes when it remotely enables/disables a
//     skill (Phase 9): it changes no SKILL.md bytes, so it never disturbs
//     the content fingerprint. An override, when present, WINS.
//
//  2. SKILL.md frontmatter — the skill author's default intent, read only
//     when no override covers the skill. Two booleans:
//
//	disable-model-invocation  (default false) — model auto-trigger off
//	user-invocable            (default true)  — user can invoke
//
// Reading the override first is what makes the scan side agree with the
// write side: Phase 9 writes skillOverrides, so the scan must read it back
// or a just-disabled skill would still report "on". The 2×2 frontmatter
// grid maps onto the four native states the spec requires (available /
// manual_only / name_only / disabled) and from there onto the shared
// EffectiveState vocabulary:
//
//	model? user? | native       | effective
//	  on    on   | available    | on
//	  off   on   | manual_only  | user-invocable-only
//	  off   off  | disabled     | off
//	  on    off  | name_only    | name-only
//
// A skill with no SKILL.md (or an unparseable one) and no override
// defaults to "available"/on with the parser's warning attached, matching
// Claude Code's own lenient behaviour of listing any directory under
// skills/.
package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// Key / DisplayName identify the tool.
const (
	toolKey     = "claude-code"
	displayName = "Claude Code"
)

// Native state strings (kept verbatim for the WebUI "native" column).
const (
	nativeAvailable  = "available"
	nativeManualOnly = "manual_only"
	nativeNameOnly   = "name_only"
	nativeDisabled   = "disabled"
)

// settingsFileName is the per-scope config file holding skillOverrides,
// a sibling of the skills/ directory (~/.claude/settings.json at user
// scope, <project>/.claude/settings.json at project scope).
const settingsFileName = "settings.json"

// Adapter is the read-only Claude Code adapter. SettingsPath overrides
// the settings.json location (tests inject a fixture path); empty derives
// it per-root from the skills directory's parent (the normal case).
type Adapter struct {
	SettingsPath string
}

// New returns a ready adapter.
func New() *Adapter { return &Adapter{} }

// Compile-time assertion that Adapter satisfies the contract.
var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

// SkillRoots resolves ~/.claude/skills (user) plus a .claude/skills
// under each registered project root. Roots whose directory does not
// exist are omitted so an uninstalled tool yields nothing.
func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	var roots []adapters.SkillRoot

	userPath, err := adapters.ExpandHome("~/.claude/skills", sc.HomeDir)
	if err != nil {
		return nil, err
	}
	if adapters.DirExists(userPath) {
		roots = append(roots, adapters.SkillRoot{
			ID:    "claude_user",
			Tool:  toolKey,
			Scope: adapters.ScopeUser,
			Path:  userPath,
		})
	}

	for i, proj := range sc.ProjectRoots {
		projPath := filepath.Join(proj, ".claude", "skills")
		if adapters.DirExists(projPath) {
			roots = append(roots, adapters.SkillRoot{
				ID:    "claude_project_" + strconv.Itoa(i),
				Tool:  toolKey,
				Scope: adapters.ScopeProject,
				Path:  projPath,
			})
		}
	}
	return roots, nil
}

// ScanSkills walks one root using the shared standard-layout scanner.
// It first loads any skillOverrides from the root's settings.json, then
// resolves each skill with an override-first decoder: a covered skill
// takes the override verbatim; an uncovered one falls back to the
// frontmatter 2×2 grid.
func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	overrides, cfgWarn := a.loadOverrides(root)

	decode := func(skillName, skillPath string, md skillmd.Result) (adapters.EffectiveState, string) {
		if raw, ok := overrides[skillName]; ok {
			if eff, native, valid := overrideState(raw); valid {
				return eff, native
			}
			// An override key present but with a value Claude Code does not
			// recognise: fall through to frontmatter rather than guess.
		}
		return deriveState(skillName, skillPath, md)
	}

	skills, err := adapters.ScanStandardRoot(root, decode)
	if err != nil {
		return nil, err
	}
	// Surface a settings-read problem once, on the first skill, so the
	// operator sees it without spamming every row (mirrors codex/opencode).
	if cfgWarn != nil && len(skills) > 0 {
		skills[0].Warnings = append(skills[0].Warnings, *cfgWarn)
	}
	return skills, nil
}

// settingsConfig is the subset of settings.json the adapter reads. Only
// skillOverrides matters here; every other settings key (permissions,
// env, hooks, …) is ignored on read and — critically — preserved on
// write by the agent's read-modify-write (internal/agentstate).
type settingsConfig struct {
	SkillOverrides map[string]string `json:"skillOverrides"`
}

// loadOverrides reads the root's settings.json and returns the
// skillOverrides map (skill name → override value). A missing file is not
// an error (no overrides); a present but unparseable file yields a
// warning the caller attaches to the scan output. An explicit
// SettingsPath (tests) wins; otherwise the path is the skills dir's
// parent + settings.json (e.g. <root>/.claude/settings.json given a
// <root>/.claude/skills root).
func (a *Adapter) loadOverrides(root adapters.SkillRoot) (map[string]string, *adapters.Warning) {
	cfgPath := a.SettingsPath
	if cfgPath == "" {
		cfgPath = filepath.Join(filepath.Dir(root.Path), settingsFileName)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return map[string]string{}, &adapters.Warning{Code: "settings_unreadable", Message: err.Error()}
	}

	var cfg settingsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return map[string]string{}, &adapters.Warning{Code: "settings_invalid_json", Message: err.Error()}
	}
	if cfg.SkillOverrides == nil {
		return map[string]string{}, nil
	}
	return cfg.SkillOverrides, nil
}

// overrideState maps a skillOverrides value onto the effective + native
// state. The override vocabulary is identical to EffectiveState by
// design, so the mapping is direct; the native column records the
// equivalent frontmatter-world native name for display consistency. The
// bool is false for an unrecognised value (caller falls back to
// frontmatter).
func overrideState(v string) (adapters.EffectiveState, string, bool) {
	switch adapters.EffectiveState(v) {
	case adapters.StateOn:
		return adapters.StateOn, nativeAvailable, true
	case adapters.StateUserInvocableOnly:
		return adapters.StateUserInvocableOnly, nativeManualOnly, true
	case adapters.StateNameOnly:
		return adapters.StateNameOnly, nativeNameOnly, true
	case adapters.StateOff:
		return adapters.StateOff, nativeDisabled, true
	default:
		return adapters.StateUnknown, "", false
	}
}

// deriveState maps the two frontmatter booleans onto the native +
// effective state. Defaults: model-invocation enabled, user-invocable.
func deriveState(_, _ string, md skillmd.Result) (adapters.EffectiveState, string) {
	modelDisabled := boolField(md.Frontmatter, "disable-model-invocation", false)
	userInvocable := boolField(md.Frontmatter, "user-invocable", true)

	switch {
	case !modelDisabled && userInvocable:
		return adapters.StateOn, nativeAvailable
	case modelDisabled && userInvocable:
		return adapters.StateUserInvocableOnly, nativeManualOnly
	case modelDisabled && !userInvocable:
		return adapters.StateOff, nativeDisabled
	default: // !modelDisabled && !userInvocable
		return adapters.StateNameOnly, nativeNameOnly
	}
}

// boolField reads a boolean frontmatter value, returning def when the
// key is absent or not a bool. A nil map (no frontmatter) yields def
// for every key, which is why a SKILL.md-less directory lands on the
// "available/on" default.
func boolField(fm map[string]any, key string, def bool) bool {
	if fm == nil {
		return def
	}
	if v, ok := fm[key].(bool); ok {
		return v
	}
	return def
}
