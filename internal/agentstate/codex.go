package agentstate

import (
	"bytes"
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

// codex.go: read-modify-write of ~/.codex/config.toml's [[skills.config]]
// array. codex only knows enabled/disabled, so only on/off are valid.

// codexEnabledValue maps an EffectiveState to codex's boolean. codex has
// no name-only / user-invocable-only / ask concept, so anything but
// on/off is refused.
func codexEnabledValue(state adapters.EffectiveState) (enabled bool, err error) {
	switch state {
	case adapters.StateOn:
		return true, nil
	case adapters.StateOff:
		return false, nil
	default:
		return false, fmt.Errorf("%w: codex cannot be %q", ErrUnsupportedState, state)
	}
}

// writeCodexEnabled sets the enabled flag for the [[skills.config]] entry
// whose path == skillKey (the skill's absolute SKILL.md path), inserting
// a new entry when none matches, and preserving every other config
// section and skill entry. The whole file is decoded into a generic map
// so unrelated tables (model config, etc.) survive the round-trip.
//
// Limitation (documented, acceptable): BurntSushi/toml does not preserve
// comments or key ordering, so a rewritten config.toml loses those. This
// matches how the rest of SkillFleet treats agent-managed config (the
// claude/opencode writers reformat JSON too); the data is preserved, the
// presentation is normalised.
func writeCodexEnabled(path, skillKey string, state adapters.EffectiveState) error {
	enabled, err := codexEnabledValue(state)
	if err != nil {
		return err
	}

	root, err := readTOMLObject(path)
	if err != nil {
		return err
	}

	// Navigate root -> skills (table) -> config ([]table). Create the
	// nesting if absent. We work in []any / map[string]any because that is
	// what toml.Unmarshal produces for arbitrary documents.
	skills, _ := root["skills"].(map[string]any)
	if skills == nil {
		skills = map[string]any{}
	}
	configList, _ := skills["config"].([]map[string]any)
	if configList == nil {
		// toml may decode an array of tables as []map[string]any already;
		// when the key is absent we start fresh.
		configList = []map[string]any{}
	}

	// Upsert the entry keyed by path.
	found := false
	for _, entry := range configList {
		p, _ := entry["path"].(string)
		if p == skillKey {
			entry["enabled"] = enabled
			found = true
			break
		}
	}
	if !found {
		configList = append(configList, map[string]any{
			"path":    skillKey,
			"enabled": enabled,
		})
	}

	skills["config"] = configList
	root["skills"] = skills

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("agentstate: marshal config.toml: %w", err)
	}
	return atomicWriteFile(path, buf.Bytes(), 0o644)
}

// readTOMLObject reads a TOML document from path into a generic map,
// returning an empty map when the file does not exist. Array-of-tables
// (`[[skills.config]]`) decode to []map[string]any, which writeCodexEnabled
// relies on. A present-but-invalid file is an error (don't clobber).
func readTOMLObject(path string) (map[string]any, error) {
	raw, err := readFileOrEmpty(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := toml.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("agentstate: parse %s: %w", path, err)
	}
	if obj == nil {
		return map[string]any{}, nil
	}
	return obj, nil
}
