package deploy

// sharedmatrix.go is the server-side source of truth for the cross-tool
// .agents/skills convention. The "agents" adapter represents the shared
// directory as a deployable content target; concrete tools that read that
// directory may still expose their own state overlays (codex/opencode).

// SharedReader describes one tool known to read .agents/skills. HasAdapter is
// true when SkillFleet can also scan that tool directly today; false means the
// entry is advisory UI copy only.
type SharedReader struct {
	ToolKey    string `json:"tool_key"`
	Name       string `json:"name"`
	HasAdapter bool   `json:"has_adapter"`
}

var sharedAgentReaders = []SharedReader{
	{ToolKey: "codex", Name: "Codex", HasAdapter: true},
	{ToolKey: "opencode", Name: "OpenCode", HasAdapter: true},
	{ToolKey: "pi", Name: "Pi Coding Agent", HasAdapter: true},
	{ToolKey: "antigravity-cli", Name: "Antigravity CLI", HasAdapter: true},
	{ToolKey: "cursor", Name: "Cursor"},
	{ToolKey: "copilot", Name: "GitHub Copilot"},
	{ToolKey: "vscode", Name: "VS Code"},
	{ToolKey: "windsurf", Name: "Windsurf"},
	{ToolKey: "roo", Name: "Roo"},
	{ToolKey: "kilo", Name: "Kilo"},
	{ToolKey: "gemini", Name: "Gemini CLI"},
}

// SharedAgentReaders returns tools known to read .agents/skills. The returned
// slice is a copy so callers cannot mutate the package table.
func SharedAgentReaders() []SharedReader {
	out := make([]SharedReader, len(sharedAgentReaders))
	copy(out, sharedAgentReaders)
	return out
}

// ReadsSharedAgents reports whether toolKey reads .agents/skills. The dedicated
// "agents" adapter is included so callers can treat the shared content view as
// a shared target too.
func ReadsSharedAgents(toolKey string) bool {
	if toolKey == "agents" {
		return true
	}
	for _, r := range sharedAgentReaders {
		if r.ToolKey == toolKey {
			return true
		}
	}
	return false
}
