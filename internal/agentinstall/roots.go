// Package agentinstall is the agent-side executor of deployment jobs:
// it takes a deploy.Plan (the server's authoritative new-content spec),
// resolves the target against the agent's own allowed roots, and writes
// the skill to disk through the safefs primitives with backup + atomic
// replace + automatic rollback on failure (v1.0 §9.3). It is the only
// component that mutates a managed skill directory.
//
// Security posture (this is the highest-risk code in the system):
//   - The server never sends an absolute path. roots.go resolves the
//     plan's {tool_key, scope, root_id} against the agent's configured
//     allowed roots; a target that matches none is REFUSED. This is the
//     enforcement point for "the agent only ever writes inside an
//     allowed root".
//   - Every filesystem write goes through safefs (*os.Root contained).
//   - The downloaded package is verified by archive sha256 before it is
//     unpacked, and the installed tree is verified by a content-sha
//     rescan before the swap is committed; either mismatch aborts (and,
//     post-swap, auto-rolls-back).
//
// roots.go: resolving an install target to an absolute path, and
// refusing anything outside the allowed set.
package agentinstall

import (
	"errors"
	"fmt"

	"github.com/yeluonight/skillfleet/internal/deploy"
)

// ErrRootNotAllowed is returned when an install target resolves to no
// configured allowed root. It is a hard refusal — the agent will not
// write outside a root the operator registered.
var ErrRootNotAllowed = errors.New("agentinstall: target resolves to no allowed root")

// AllowedRoot is one filesystem location the agent may install into
// (v1.0 §9.1). Path must be absolute and exist; ID is the stable
// identifier the server addresses it by, Tool/Scope the human-meaningful
// fallback. This mirrors the structured agentcfg.AllowedRoot t5 wires;
// agentinstall takes the slice directly so it stays independently
// testable.
type AllowedRoot struct {
	ID    string
	Tool  string
	Scope string
	Path  string
}

// ResolveTarget maps a deploy.Target to the absolute path of a matching
// allowed root. RootID is the preferred key (an exact id match); when it
// is empty or unmatched, a Tool+Scope match is the fallback. No match is
// ErrRootNotAllowed. A target that names a RootID which exists but whose
// Tool/Scope disagree with the target is also refused (the ids must be
// consistent), so a stale id can't redirect an install to the wrong
// tool's directory.
func ResolveTarget(roots []AllowedRoot, target deploy.Target) (AllowedRoot, error) {
	if target.RootID != "" {
		for _, r := range roots {
			if r.ID == target.RootID {
				// If the target also carries tool/scope, they must agree
				// with the matched root — a mismatched pair is suspicious.
				if target.ToolKey != "" && r.Tool != target.ToolKey {
					return AllowedRoot{}, fmt.Errorf("%w: root_id %q is tool %q, target says %q",
						ErrRootNotAllowed, target.RootID, r.Tool, target.ToolKey)
				}
				if target.Scope != "" && r.Scope != target.Scope {
					return AllowedRoot{}, fmt.Errorf("%w: root_id %q is scope %q, target says %q",
						ErrRootNotAllowed, target.RootID, r.Scope, target.Scope)
				}
				return r, nil
			}
		}
		// A RootID that matches nothing is a hard refusal, not a
		// silent fallback to tool/scope (the operator named a specific
		// root that no longer exists).
		return AllowedRoot{}, fmt.Errorf("%w: root_id %q", ErrRootNotAllowed, target.RootID)
	}

	// Fallback: match on tool + scope.
	if target.ToolKey == "" || target.Scope == "" {
		return AllowedRoot{}, fmt.Errorf("%w: target has neither root_id nor tool+scope", ErrRootNotAllowed)
	}
	for _, r := range roots {
		if r.Tool == target.ToolKey && r.Scope == target.Scope {
			return r, nil
		}
	}
	return AllowedRoot{}, fmt.Errorf("%w: tool %q scope %q", ErrRootNotAllowed, target.ToolKey, target.Scope)
}
