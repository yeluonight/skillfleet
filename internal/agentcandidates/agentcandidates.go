// Package agentcandidates assembles the candidate-root discovery half of
// an inventory report: it runs every adapter's CandidateRoots, dedups
// the results by absolute path, and joins them against the agent's
// registered allowed_roots so the WebUI can offer one-click registration
// (Phase 11).
//
// It is the candidate-side mirror of agentscan: agentscan answers "what
// skills are installed where", this answers "where could the operator
// register a root". Both run agent-side and import only the read-only
// adapters + agentcfg, never the server registry — the agent binary must
// not link internal/registry (Phase 8.5).
//
// Dedup is the crux. Several adapters read or represent the SHARED
// ~/.agents/skills path, so agents, codex, and opencode all emit it as a
// candidate. The operator should see ONE row for that path (marked shared),
// not three, because registering it once is enough. We merge by absolute
// path, OR-ing the Shared/ToolDetected flags.
package agentcandidates

import (
	"os"
	"sort"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentscan"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

// Discover runs every registered adapter's CandidateRoots, dedups by
// absolute path, and joins with registered (the agent's allowed_roots)
// to mark which candidates are already registered. Roots that are
// registered but match no adapter candidate (e.g. an operator's custom
// path) are appended so the WebUI can still surface + remove them.
//
// homeDir feeds the adapters' ScanContext; empty falls back to
// os.UserHomeDir (matching agentscan.Scan), so a caller that does not
// already know the home dir can pass "". ProjectRoots are intentionally
// not passed — project-scope discovery is not wired this phase.
//
// The returned slice is sorted by (scope, path) for a deterministic
// report — the inventory replacement model re-stores it every run, and a
// stable order keeps the WebUI from reshuffling on each poll.
func Discover(homeDir string, registered []agentcfg.AllowedRoot) []inventory.RootCandidate {
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	sc := adapters.ScanContext{HomeDir: homeDir}

	// Index registered roots by absolute path for the existence join.
	// agentcfg paths are already absolute + ~-expanded (roots add /
	// agentroots resolve them before save), so a direct path key works.
	regByPath := make(map[string]agentcfg.AllowedRoot, len(registered))
	for _, r := range registered {
		regByPath[r.Path] = r
	}

	// Merge adapter candidates by path.
	byPath := make(map[string]*inventory.RootCandidate)

	for _, ad := range agentscan.All() {
		for _, c := range ad.CandidateRoots(sc) {
			if existing, ok := byPath[c.Path]; ok {
				// Same physical dir from another tool: OR the flags so a
				// shared/detected/unconsumed signal from any contributor sticks.
				existing.Shared = existing.Shared || c.Shared
				existing.ToolDetected = existing.ToolDetected || c.ToolDetected
				existing.Unconsumed = existing.Unconsumed || c.Unconsumed
				// Keep the first tool_key as the nominal owner but do not
				// lose that it is shared — Shared already conveys that.
				continue
			}
			rc := inventory.RootCandidate{
				ToolKey:      c.ToolKey,
				Scope:        string(c.Scope),
				Path:         c.Path,
				DisplayTmpl:  c.DisplayTmpl,
				Exists:       c.Exists,
				ToolDetected: c.ToolDetected,
				Shared:       c.Shared,
				Unconsumed:   c.Unconsumed,
			}
			if reg, ok := regByPath[c.Path]; ok {
				rc.Registered = true
				rc.RootID = reg.ID
			}
			byPath[c.Path] = &rc
		}
	}

	// Append registered roots that matched no adapter candidate (custom
	// operator paths) so the WebUI can list + remove them.
	for _, r := range registered {
		if _, ok := byPath[r.Path]; ok {
			continue
		}
		rc := inventory.RootCandidate{
			ToolKey:    r.Tool,
			Scope:      r.Scope,
			Path:       r.Path,
			Exists:     adapters.DirExists(r.Path),
			Registered: true,
			RootID:     r.ID,
		}
		byPath[r.Path] = &rc
	}

	out := make([]inventory.RootCandidate, 0, len(byPath))
	for _, root := range byPath {
		out = append(out, *root)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Path < out[j].Path
	})
	return out
}
