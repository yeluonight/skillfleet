package agentstate

import (
	"encoding/json"
	"fmt"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

// writers.go maps a desired EffectiveState onto each tool's native config
// representation and performs the read-modify-write. All three follow the
// same shape: read the whole file into a generic container, mutate ONE
// nested key, re-encode, atomic-write. Unknown keys are preserved because
// the container is a generic map, never a narrow struct.

// claudeOverrideValue maps an EffectiveState to the skillOverrides string
// claude-code understands. The vocabularies are identical by design (the
// scan side reads these very strings back), so this is a passthrough that
// also rejects states claude-code can't express. "on" is special: rather
// than write "on" we DELETE the override key, restoring the skill to its
// frontmatter default — the cleanest representation of "no override", and
// what the TUI does when you cycle a skill back to on.
func claudeOverrideValue(state adapters.EffectiveState) (value string, deleteKey bool, err error) {
	switch state {
	case adapters.StateOn:
		return "", true, nil
	case adapters.StateOff, adapters.StateNameOnly, adapters.StateUserInvocableOnly:
		return string(state), false, nil
	default:
		return "", false, fmt.Errorf("%w: claude-code cannot be %q", ErrUnsupportedState, state)
	}
}

// writeClaudeOverride sets skillName's entry in settings.json's
// skillOverrides map to the value for state (or deletes it for "on"),
// preserving every other settings key. A missing file starts from an
// empty object; a present file is decoded generically so permissions /
// env / hooks / other overrides survive.
func writeClaudeOverride(path, skillName string, state adapters.EffectiveState) error {
	value, deleteKey, err := claudeOverrideValue(state)
	if err != nil {
		return err
	}

	root, err := readJSONObject(path)
	if err != nil {
		return err
	}

	// Locate or create the skillOverrides object without disturbing the
	// rest of the document.
	overrides, _ := root["skillOverrides"].(map[string]any)
	if overrides == nil {
		overrides = map[string]any{}
	}

	if deleteKey {
		delete(overrides, skillName)
	} else {
		overrides[skillName] = value
	}

	// Drop the key entirely when empty so we don't litter "skillOverrides":{}.
	if len(overrides) == 0 {
		delete(root, "skillOverrides")
	} else {
		root["skillOverrides"] = overrides
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("agentstate: marshal settings.json: %w", err)
	}
	out = append(out, '\n')
	return atomicWriteFile(path, out, 0o644)
}

// readJSONObject reads a JSON object from path, returning an empty map
// when the file does not exist. A present-but-invalid file is an error
// (we must not clobber a config we failed to understand). A file whose
// top level is not an object (e.g. a JSON array) is likewise an error.
func readJSONObject(path string) (map[string]any, error) {
	raw, err := readFileOrEmpty(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("agentstate: parse %s: %w", path, err)
	}
	if obj == nil {
		return map[string]any{}, nil
	}
	return obj, nil
}
