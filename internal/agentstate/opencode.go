package agentstate

import (
	"encoding/json"
	"fmt"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

// opencode.go: read-modify-write of opencode.json's permission.skill map.
// opencode understands allow/ask/deny, i.e. on/ask/off.

// opencodePermissionValue maps an EffectiveState onto opencode's
// permission string. on→allow, ask→ask, off→deny; name-only and
// user-invocable-only have no opencode equivalent and are refused. "on"
// deletes the key (restoring the default-allow) for the same reason
// claude deletes its override: the cleanest "no special rule" form.
func opencodePermissionValue(state adapters.EffectiveState) (value string, deleteKey bool, err error) {
	switch state {
	case adapters.StateOn:
		return "", true, nil
	case adapters.StateAsk:
		return "ask", false, nil
	case adapters.StateOff:
		return "deny", false, nil
	default:
		return "", false, fmt.Errorf("%w: opencode cannot be %q", ErrUnsupportedState, state)
	}
}

// writeOpencodePermission sets skillName's entry in opencode.json's
// permission.skill map (or deletes it for "on"), preserving every other
// key in the document and in the permission object.
func writeOpencodePermission(path, skillName string, state adapters.EffectiveState) error {
	value, deleteKey, err := opencodePermissionValue(state)
	if err != nil {
		return err
	}

	root, err := readJSONObject(path)
	if err != nil {
		return err
	}

	permission, _ := root["permission"].(map[string]any)
	if permission == nil {
		permission = map[string]any{}
	}
	skill, _ := permission["skill"].(map[string]any)
	if skill == nil {
		skill = map[string]any{}
	}

	if deleteKey {
		delete(skill, skillName)
	} else {
		skill[skillName] = value
	}

	// Prune empty containers so we don't leave "skill":{} / "permission":{}.
	if len(skill) == 0 {
		delete(permission, "skill")
	} else {
		permission["skill"] = skill
	}
	if len(permission) == 0 {
		delete(root, "permission")
	} else {
		root["permission"] = permission
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("agentstate: marshal opencode.json: %w", err)
	}
	out = append(out, '\n')
	return atomicWriteFile(path, out, 0o644)
}
