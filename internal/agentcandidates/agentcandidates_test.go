package agentcandidates

import (
	"path/filepath"
	"testing"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

// indexByPath indexes a result slice for assertions.
func indexByPath(cands []inventory.RootCandidate) map[string]inventory.RootCandidate {
	m := make(map[string]inventory.RootCandidate, len(cands))
	for _, c := range cands {
		m[c.Path] = c
	}
	return m
}

func TestDiscover_DedupsSharedAgentsRoot(t *testing.T) {
	home := t.TempDir()
	got := Discover(home, nil)
	m := indexByPath(got)

	// agents, codex and opencode all emit ~/.agents/skills; it must appear
	// exactly once, flagged shared.
	agentsPath := filepath.Join(home, ".agents", "skills")
	count := 0
	for _, c := range got {
		if c.Path == agentsPath {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("~/.agents/skills appears %d times, want 1 (dedup failed)", count)
	}
	shared := m[agentsPath]
	if !shared.Shared {
		t.Error("merged ~/.agents/skills should be marked Shared")
	}
	if shared.Scope != "user" {
		t.Errorf("scope = %q, want user", shared.Scope)
	}
}

func TestDiscover_ClaudeRootPresentAndNotShared(t *testing.T) {
	home := t.TempDir()
	m := indexByPath(Discover(home, nil))
	claudePath := filepath.Join(home, ".claude", "skills")
	c, ok := m[claudePath]
	if !ok {
		t.Fatalf("~/.claude/skills candidate missing")
	}
	// claude-code never reads the shared dir; even though opencode also
	// lists ~/.claude/skills, neither flags it Shared.
	if c.Shared {
		t.Error("~/.claude/skills should not be Shared")
	}
}

func TestDiscover_JoinsRegistered(t *testing.T) {
	home := t.TempDir()
	claudePath := filepath.Join(home, ".claude", "skills")
	registered := []agentcfg.AllowedRoot{
		{ID: "claude_user", Tool: "claude-code", Scope: "user", Path: claudePath},
	}
	m := indexByPath(Discover(home, registered))
	c := m[claudePath]
	if !c.Registered {
		t.Error("registered candidate should have Registered=true")
	}
	if c.RootID != "claude_user" {
		t.Errorf("RootID = %q, want claude_user", c.RootID)
	}
	// A non-registered candidate stays unregistered.
	agentsPath := filepath.Join(home, ".agents", "skills")
	if m[agentsPath].Registered {
		t.Error("unregistered candidate should have Registered=false")
	}
}

func TestDiscover_AppendsCustomRegisteredPath(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "my", "custom", "skills")
	registered := []agentcfg.AllowedRoot{
		{ID: "custom_1", Tool: "claude-code", Scope: "user", Path: custom},
	}
	m := indexByPath(Discover(home, registered))
	c, ok := m[custom]
	if !ok {
		t.Fatalf("custom registered path not surfaced for removal")
	}
	if !c.Registered || c.RootID != "custom_1" {
		t.Errorf("custom path join = registered:%v id:%q", c.Registered, c.RootID)
	}
}

func TestDiscover_SortedByScopeThenPath(t *testing.T) {
	home := t.TempDir()
	got := Discover(home, nil)
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Scope > cur.Scope {
			t.Errorf("not sorted by scope: %q before %q", prev.Scope, cur.Scope)
		}
		if prev.Scope == cur.Scope && prev.Path > cur.Path {
			t.Errorf("not sorted by path within scope: %q before %q", prev.Path, cur.Path)
		}
	}
}

// TestDiscover_CodexUnconsumedRoot verifies the codex dedicated
// ~/.codex/skills candidate — which the owning CLI does not read — is
// surfaced through Discover with Unconsumed=true (and not conflated with
// the shared ~/.agents/skills codex also lists).
func TestDiscover_CodexUnconsumedRoot(t *testing.T) {
	home := t.TempDir()
	m := indexByPath(Discover(home, nil))

	codexDedicated := filepath.Join(home, ".codex", "skills")
	c, ok := m[codexDedicated]
	if !ok {
		t.Fatalf("~/.codex/skills unconsumed candidate missing")
	}
	if !c.Unconsumed {
		t.Error("~/.codex/skills should be Unconsumed (codex CLI does not read it)")
	}
	if c.Shared {
		t.Error("~/.codex/skills must not be Shared (distinct from ~/.agents/skills)")
	}
	if c.Scope != "user" {
		t.Errorf("scope = %q, want user", c.Scope)
	}

	// Sanity: the shared ~/.agents/skills stays Shared and NOT Unconsumed.
	agentsPath := filepath.Join(home, ".agents", "skills")
	if s := m[agentsPath]; s.Unconsumed {
		t.Error("~/.agents/skills must not be Unconsumed (codex does read the shared dir)")
	}
}
