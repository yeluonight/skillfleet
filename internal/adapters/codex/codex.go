// Package codex implements the read-only Codex adapter (v1.0 §10.2).
//
// Roots scanned:
//
//	~/.agents/skills        (user scope)
//	<project>/.agents/skills (project scope)
//	/etc/codex/skills       (system scope)
//
// Native-state derivation. Unlike Claude Code, Codex stores enable
// state OUT of band, in a config.toml whose [[skills.config]] array
// keys each skill by the absolute path of its SKILL.md:
//
//	[[skills.config]]
//	path = "/home/me/.agents/skills/deploy/SKILL.md"
//	enabled = false
//
// The adapter loads that config once per scan (ConfigPath, default
// ~/.codex/config.toml), builds a path→enabled map, and looks each
// skill up by its SKILL.md path. A skill with no matching entry is
// enabled by default (available / on); an entry with enabled=false is
// disabled (off). Codex exposes no name-only / manual-only distinction
// in this file, so those effective states never arise here.
package codex

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

const (
	toolKey     = "codex"
	displayName = "Codex"

	nativeAvailable = "available"
	nativeDisabled  = "disabled"

	systemSkillsPath  = "/etc/codex/skills"
	defaultConfigPath = "~/.codex/config.toml"
)

// Adapter is the read-only Codex adapter. ConfigPath overrides the
// config.toml location (tests inject a fixture path); empty uses the
// default ~/.codex/config.toml.
type Adapter struct {
	ConfigPath string
}

// New returns an adapter using the default config path.
func New() *Adapter { return &Adapter{} }

var _ adapters.ReadOnlyAdapter = (*Adapter)(nil)

func (a *Adapter) Key() string         { return toolKey }
func (a *Adapter) DisplayName() string { return displayName }

// tomlConfig is the subset of Codex's config.toml the adapter reads.
type tomlConfig struct {
	Skills struct {
		Config []struct {
			Path    string `toml:"path"`
			Enabled *bool  `toml:"enabled"` // pointer so omitted != false
		} `toml:"config"`
	} `toml:"skills"`
}

// rootSpecs enumerates Codex's scan locations in deterministic order. The
// user root is the SHARED ~/.agents/skills (Codex is the canonical reader of
// the cross-tool shared directory), so it is flagged Shared for candidate UI.
var rootSpecs = []adapters.RootSpec{
	{IDBase: "codex_user", Scope: adapters.ScopeUser, Tmpl: "~/.agents/skills", Shared: true},
	{IDBase: "codex_project", Scope: adapters.ScopeProject, Tmpl: ".agents/skills", Shared: true},
	{IDBase: "codex_system", Scope: adapters.ScopeSystem, Tmpl: systemSkillsPath},
}

func (a *Adapter) SkillRoots(sc adapters.ScanContext) ([]adapters.SkillRoot, error) {
	return adapters.SkillRootsFromSpecs(sc, toolKey, rootSpecs)
}

// CandidateRoots suggests Codex's skill roots for registration. Detected
// when ~/.codex exists or a `codex` binary is on PATH (config dir is the
// more reliable signal; PATH is a fallback that may miss in a service
// context).
func (a *Adapter) CandidateRoots(sc adapters.ScanContext) []adapters.CandidateRoot {
	detected := adapters.ConfigDirExists(sc.HomeDir, "~/.codex") || adapters.BinaryOnPath("codex")
	return adapters.BuildCandidateRootsFromRootSpecs(sc, toolKey, detected, rootSpecs)
}

// ScanSkills loads the config-driven enable map once, then walks the
// root with a closure that resolves each skill's state by its
// SKILL.md path.
func (a *Adapter) ScanSkills(sc adapters.ScanContext, root adapters.SkillRoot) ([]adapters.DiscoveredSkill, error) {
	enabledByPath, cfgWarn := a.loadEnableMap(sc)

	decode := func(_, skillPath string, _ skillmd.Result) (adapters.EffectiveState, string) {
		mdPath := filepath.Join(skillPath, adapters.SkillFileName)
		if enabled, ok := enabledByPath[mdPath]; ok && !enabled {
			return adapters.StateOff, nativeDisabled
		}
		return adapters.StateOn, nativeAvailable
	}

	skills, err := adapters.ScanStandardRoot(root, decode)
	if err != nil {
		return nil, err
	}
	// Surface a config-read problem once, on the first skill, so the
	// operator sees it without spamming every row.
	if cfgWarn != nil && len(skills) > 0 {
		skills[0].Warnings = append(skills[0].Warnings, *cfgWarn)
	}
	return skills, nil
}

// loadEnableMap reads config.toml and returns path→enabled. A missing
// config is not an error (every skill defaults to enabled); a present
// but unparseable config yields a warning the caller attaches to the
// scan output.
func (a *Adapter) loadEnableMap(sc adapters.ScanContext) (map[string]bool, *adapters.Warning) {
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
			// No config = everything enabled by default. Not a warning.
			return map[string]bool{}, nil
		}
		return map[string]bool{}, &adapters.Warning{Code: "config_unreadable", Message: err.Error()}
	}

	var cfg tomlConfig
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return map[string]bool{}, &adapters.Warning{Code: "config_invalid_toml", Message: err.Error()}
	}

	out := make(map[string]bool, len(cfg.Skills.Config))
	for _, c := range cfg.Skills.Config {
		if c.Path == "" {
			continue
		}
		enabled := true // an entry without an explicit enabled is "on"
		if c.Enabled != nil {
			enabled = *c.Enabled
		}
		out[c.Path] = enabled
	}
	return out, nil
}
