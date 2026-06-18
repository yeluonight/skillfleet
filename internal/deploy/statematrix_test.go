package deploy

import (
	"errors"
	"testing"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

// TestSupportedStates locks the per-tool capability matrix (v1.0 §10,
// §17 Phase 9). Each tool's exact set and display order is asserted; a
// regression that silently widens or narrows a tool's vocabulary (e.g.
// letting codex claim "ask") fails here.
func TestSupportedStates(t *testing.T) {
	cases := []struct {
		tool string
		want []adapters.EffectiveState
	}{
		{"claude-code", []adapters.EffectiveState{
			adapters.StateOn, adapters.StateNameOnly,
			adapters.StateUserInvocableOnly, adapters.StateOff,
		}},
		{"codex", []adapters.EffectiveState{
			adapters.StateOn, adapters.StateOff,
		}},
		{"opencode", []adapters.EffectiveState{
			adapters.StateOn, adapters.StateAsk, adapters.StateOff,
		}},
		{"antigravity", nil},
		{"antigravity-cli", nil},
		{"pi", nil},
		{"nonexistent", nil},
	}
	for _, c := range cases {
		got := SupportedStates(c.tool)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.tool, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s[%d]: got %q, want %q", c.tool, i, got[i], c.want[i])
			}
		}
	}
}

// TestSupportedStates_ReturnsCopy proves callers cannot corrupt the
// shared table by mutating a returned slice.
func TestSupportedStates_ReturnsCopy(t *testing.T) {
	got := SupportedStates("claude-code")
	if len(got) == 0 {
		t.Fatal("claude-code should have states")
	}
	got[0] = adapters.StateUnknown // mutate the returned slice
	again := SupportedStates("claude-code")
	if again[0] != adapters.StateOn {
		t.Errorf("table was mutated through returned slice: got[0]=%q", again[0])
	}
}

func TestSupportsStateChange(t *testing.T) {
	yes := []string{"claude-code", "codex", "opencode"}
	no := []string{"antigravity", "antigravity-cli", "pi", "", "unknown"}
	for _, k := range yes {
		if !SupportsStateChange(k) {
			t.Errorf("%s: want supported", k)
		}
	}
	for _, k := range no {
		if SupportsStateChange(k) {
			t.Errorf("%s: want unsupported", k)
		}
	}
}

// TestValidateStateChange walks every (tool, state) cell: the supported
// combinations return nil; an unsupported state on a known tool returns
// ErrUnsupportedState; any state on an unknown tool returns ErrUnknownTool.
func TestValidateStateChange(t *testing.T) {
	all := []adapters.EffectiveState{
		adapters.StateOn, adapters.StateOff, adapters.StateNameOnly,
		adapters.StateUserInvocableOnly, adapters.StateAsk,
	}

	type cell struct {
		tool    string
		state   adapters.EffectiveState
		wantErr error // nil, ErrUnsupportedState, or ErrUnknownTool
	}
	var cells []cell

	// Known tools: supported states → nil; the rest → ErrUnsupportedState.
	for _, tool := range []string{"claude-code", "codex", "opencode"} {
		supported := map[adapters.EffectiveState]bool{}
		for _, s := range SupportedStates(tool) {
			supported[s] = true
		}
		for _, s := range all {
			want := error(ErrUnsupportedState)
			if supported[s] {
				want = nil
			}
			cells = append(cells, cell{tool, s, want})
		}
	}
	// Unknown tools: every state → ErrUnknownTool.
	for _, tool := range []string{"antigravity", "pi", "unknown"} {
		for _, s := range all {
			cells = append(cells, cell{tool, s, ErrUnknownTool})
		}
	}

	for _, c := range cells {
		err := ValidateStateChange(c.tool, c.state)
		switch {
		case c.wantErr == nil && err != nil:
			t.Errorf("%s/%s: want ok, got %v", c.tool, c.state, err)
		case c.wantErr != nil && !errors.Is(err, c.wantErr):
			t.Errorf("%s/%s: want %v, got %v", c.tool, c.state, c.wantErr, err)
		}
	}
}

// TestValidateStateChange_SpotChecks pins the headline cross-tool
// asymmetries the matrix exists to enforce, in case the generated table
// above is ever loosened.
func TestValidateStateChange_SpotChecks(t *testing.T) {
	// codex has no "ask" — the opencode-only state.
	if err := ValidateStateChange("codex", adapters.StateAsk); !errors.Is(err, ErrUnsupportedState) {
		t.Errorf("codex+ask: want ErrUnsupportedState, got %v", err)
	}
	// opencode has no "name-only" — a claude-only state.
	if err := ValidateStateChange("opencode", adapters.StateNameOnly); !errors.Is(err, ErrUnsupportedState) {
		t.Errorf("opencode+name-only: want ErrUnsupportedState, got %v", err)
	}
	// claude-code is the only tool with user-invocable-only.
	if err := ValidateStateChange("claude-code", adapters.StateUserInvocableOnly); err != nil {
		t.Errorf("claude+user-invocable-only: want ok, got %v", err)
	}
	if err := ValidateStateChange("codex", adapters.StateUserInvocableOnly); !errors.Is(err, ErrUnsupportedState) {
		t.Errorf("codex+user-invocable-only: want ErrUnsupportedState, got %v", err)
	}
	// on/off are the universal pair every supported tool shares.
	for _, tool := range []string{"claude-code", "codex", "opencode"} {
		for _, s := range []adapters.EffectiveState{adapters.StateOn, adapters.StateOff} {
			if err := ValidateStateChange(tool, s); err != nil {
				t.Errorf("%s+%s: want ok, got %v", tool, s, err)
			}
		}
	}
}
