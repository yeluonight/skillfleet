package deploy

import (
	"errors"
	"fmt"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

// statematrix.go is the single source of truth for "which enable/disable
// states can a given tool actually represent" (v1.0 §10, §17 Phase 9
// acceptance). A state-change job is only worth dispatching if the target
// tool can natively express the requested state; asking Codex (which only
// knows enabled/disabled) for "ask" or "name-only" can never succeed, so
// the planner rejects it up front (422) rather than minting a job destined
// to fail on the agent.
//
// The matrix lives here, on the server side of the downlink, because the
// planner needs it to validate intent before creating a job. The agent
// re-derives the same answer implicitly (its writer only knows how to
// express its own tool's states), so this is a fail-fast guard, not the
// only line of defence — agentstate writers reject an impossible mapping
// too (defence in depth, mirroring "trust, but verify" on the install
// path).
//
// The vocabulary is adapters.EffectiveState, reused verbatim rather than
// redefined: the scan side already maps each tool's native config into
// these six values (adapters.go §10.1 table), so a state-change request
// speaks the same words the inventory matrix displays. Sharing the type
// is safe for the agent binary — it already links adapters directly for
// scanning, and adapters pulls in no registry/database code, so deploy
// importing adapters leaks no server-only dependency into the agent (the
// same constraint Phase 8.5 t5 enforced for the other wire types).

// ErrUnsupportedState is returned when a tool cannot natively represent a
// requested target state. Wrapped with the tool key + state for the 422.
var ErrUnsupportedState = errors.New("deploy: tool does not support requested state")

// ErrUnknownTool is returned for a tool key with no state-change support
// at all (antigravity / antigravity-cli / pi this phase — no writable
// native enable signal, v1.0 §10.4-§10.6).
var ErrUnknownTool = errors.New("deploy: tool does not support state changes")

// supportedStates is the per-tool capability table. A tool absent from
// the map supports no state changes (the WebUI disables the control and
// the planner rejects any request).
//
//   - claude-code: the full four-state vocabulary, written via the
//     skillOverrides map in settings.json (on / name-only /
//     user-invocable-only / off). This is the only tool with a native
//     name-only and user-invocable-only distinction.
//   - codex: binary enabled/disabled in config.toml's [[skills.config]];
//     no name-only / ask concept, so only on / off.
//   - opencode: permission.skill values allow / ask / deny, i.e. on / ask
//     / off; no name-only / user-invocable-only.
var supportedStates = map[string][]adapters.EffectiveState{
	"claude-code": {
		adapters.StateOn,
		adapters.StateNameOnly,
		adapters.StateUserInvocableOnly,
		adapters.StateOff,
	},
	"codex": {
		adapters.StateOn,
		adapters.StateOff,
	},
	"opencode": {
		adapters.StateOn,
		adapters.StateAsk,
		adapters.StateOff,
	},
}

// SupportedStates returns the target states toolKey can natively
// represent, in a stable display order (the order the WebUI offers them).
// An unsupported tool returns nil — callers treat nil as "state changes
// not available for this tool".
func SupportedStates(toolKey string) []adapters.EffectiveState {
	states := supportedStates[toolKey]
	if states == nil {
		return nil
	}
	// Return a copy so callers can't mutate the table.
	out := make([]adapters.EffectiveState, len(states))
	copy(out, states)
	return out
}

// SupportsStateChange reports whether toolKey supports any state change at
// all. The WebUI uses this to enable/disable the matrix control.
func SupportsStateChange(toolKey string) bool {
	return supportedStates[toolKey] != nil
}

// ValidateStateChange checks that toolKey can natively represent state.
// It returns ErrUnknownTool when the tool supports no state changes, and
// ErrUnsupportedState when the tool is known but cannot express that
// particular state (e.g. codex + "ask"). A nil return means the planner
// may mint the job.
func ValidateStateChange(toolKey string, state adapters.EffectiveState) error {
	allowed, ok := supportedStates[toolKey]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTool, toolKey)
	}
	for _, s := range allowed {
		if s == state {
			return nil
		}
	}
	return fmt.Errorf("%w: %s cannot be %q", ErrUnsupportedState, toolKey, state)
}
