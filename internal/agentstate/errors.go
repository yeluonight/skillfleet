package agentstate

import (
	"errors"
	"os"
)

// errors.go + shared read helper for the writers.

// ErrUnsupportedState is returned when a desired state cannot be
// expressed in a tool's native config (e.g. codex + "ask"). It mirrors
// deploy.ErrUnsupportedState's role but lives here so agentstate has no
// dependency on the planner's matrix — the writer is the second,
// independent line of defence (the planner rejects first; the writer
// refuses too rather than write something meaningless).
var ErrUnsupportedState = errors.New("agentstate: tool cannot represent state")

// ErrUnknownTool is returned for a tool key agentstate has no writer for.
var ErrUnknownTool = errors.New("agentstate: no state writer for tool")

// readFileOrEmpty reads path, returning (nil, nil) when it does not
// exist (a config the tool has never written yet). Any other read error
// is returned — we must not treat an unreadable file as empty and then
// overwrite it.
func readFileOrEmpty(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return raw, nil
}
