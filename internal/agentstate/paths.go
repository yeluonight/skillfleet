package agentstate

import (
	"path/filepath"

	"github.com/yeluonight/skillfleet/internal/agentinstall"
)

// paths.go derives, per tool, the absolute path of the config file that
// governs a skill's enable state — and (for codex) the key the skill is
// addressed by inside that file. The derivation mirrors each adapter's
// READ path exactly, so the write side touches the same file the scan
// side reads back (read-write parity):
//
//   - claude-code: settings.json is a sibling of the skills directory
//     (the resolved allowed root). adapters/claudecode loadOverrides reads
//     filepath.Join(filepath.Dir(root.Path), "settings.json"); we write
//     the same path. This keeps user vs project scope correct: each scope
//     has its own root, hence its own settings.json.
//   - codex: config.toml is a fixed per-user file (~/.codex/config.toml),
//     independent of the skills root location; the [[skills.config]]
//     entries key on the SKILL.md ABSOLUTE path, so we compute
//     <root>/<skill>/SKILL.md as the entry key.
//   - opencode: opencode.json is a fixed per-user file
//     (~/.config/opencode/...). Its permission.skill map keys on the bare
//     skill name.
//
// claude-code and codex paths are derived from the resolved allowed root
// (so a job cannot point the write at an arbitrary directory); opencode's
// path is a fixed user-global location.

const (
	claudeSettingsFile = "settings.json"
	codexConfigRel     = ".codex/config.toml"
	opencodeConfigRel  = ".config/opencode/opencode.json"
)

// claudeSettingsPath returns the settings.json governing skills under the
// given resolved root (the skills dir's parent + settings.json).
func claudeSettingsPath(root agentinstall.AllowedRoot) string {
	return filepath.Join(filepath.Dir(root.Path), claudeSettingsFile)
}

// codexConfigPath returns the per-user codex config.toml path.
func codexConfigPath(homeDir string) string {
	return filepath.Join(homeDir, codexConfigRel)
}

// codexSkillKey returns the [[skills.config]] path key for a skill under
// the resolved root: <root>/<skill>/SKILL.md, absolute, matching what the
// codex adapter looks the enable state up by.
func codexSkillKey(root agentinstall.AllowedRoot, skillName string) string {
	return filepath.Join(root.Path, skillName, "SKILL.md")
}

// opencodeConfigPath returns the per-user opencode.json path.
func opencodeConfigPath(homeDir string) string {
	return filepath.Join(homeDir, opencodeConfigRel)
}
