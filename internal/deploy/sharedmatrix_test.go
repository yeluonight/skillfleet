package deploy

import "testing"

func TestSharedAgentReadersReturnsCopy(t *testing.T) {
	readers := SharedAgentReaders()
	if len(readers) == 0 {
		t.Fatal("want shared readers")
	}
	readers[0].ToolKey = "mutated"
	if SharedAgentReaders()[0].ToolKey == "mutated" {
		t.Fatal("SharedAgentReaders returned mutable package table")
	}
}

func TestReadsSharedAgents(t *testing.T) {
	for _, tool := range []string{"agents", "codex", "opencode", "pi", "antigravity-cli", "cursor", "gemini"} {
		if !ReadsSharedAgents(tool) {
			t.Errorf("ReadsSharedAgents(%q) = false, want true", tool)
		}
	}
	for _, tool := range []string{"claude-code", "antigravity", "unknown"} {
		if ReadsSharedAgents(tool) {
			t.Errorf("ReadsSharedAgents(%q) = true, want false", tool)
		}
	}
}
