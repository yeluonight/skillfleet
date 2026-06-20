// candidates.go adds the "candidate root discovery" half of an adapter:
// where this tool COULD keep skills, listed whether or not the directory
// exists yet. SkillRoots (adapters.go) answers "what is here to scan";
// CandidateRoots answers "what could an operator register" so the agent
// can suggest roots instead of making the operator hand-type every
// `roots add` (Phase 11).
//
// The two are deliberately separate: SkillRoots filters to existing dirs
// (an uninstalled tool yields nothing to scan), whereas CandidateRoots
// returns the templates unconditionally and tags each with Exists +
// ToolDetected so the UI can say "you have Claude Code installed; its
// skills dir doesn't exist yet — create + register it?".

package adapters

// CandidateRoot is a location a tool COULD use for skills, surfaced to
// the operator as a registration suggestion. Unlike SkillRoot it is not
// filtered by existence: a non-existent path is still a valid candidate
// (the operator may want to create + register it).
type CandidateRoot struct {
	// ToolKey / Scope / Path mirror an eventual agentcfg.AllowedRoot —
	// Path is absolute and ~-expanded, ready to register as-is.
	ToolKey string
	Scope   Scope
	Path    string

	// DisplayTmpl is the un-expanded, human-readable form ("~/.claude/
	// skills") for the UI; Path is the resolved absolute form.
	DisplayTmpl string

	// Exists reports whether Path is currently a directory (DirExists).
	Exists bool

	// ToolDetected is a SOFT hint that the owning tool looks installed
	// (a sibling config dir exists, or its binary is on PATH). It is
	// advisory only — never a security gate — because a service-context
	// PATH differs from an interactive shell's. The registration
	// whitelist (internal/agentroots) never consults this.
	ToolDetected bool

	// Shared marks a multi-tool shared directory (~/.agents/skills):
	// installing a skill there exposes it to EVERY tool that reads the
	// shared location, so the UI must warn before registering it as an
	// install target. See the codex adapter (the canonical .agents
	// reader) for where this is set.
	Shared bool

	// Unconsumed marks a root the owning tool's CLI does not read — SkillFleet
	// manages it but the tool ignores it. The UI warns the operator, and the
	// scanner reports an "unknown" effective state rather than falsely "on".
	Unconsumed bool
}

// CandidateRootSpec is the per-tool template a concrete adapter declares:
// a scope + an un-expanded path pattern (~/… for user, an absolute path
// for system) + the Shared flag. BuildCandidateRoots turns specs into
// resolved CandidateRoots, so adapters only encode what is tool-specific.
type CandidateRootSpec struct {
	Scope  Scope
	Tmpl   string // "~/.claude/skills" or "/etc/codex/skills"
	Shared bool
	// Unconsumed mirrors RootSpec.Unconsumed for candidates built from specs.
	Unconsumed bool
}

// BuildCandidateRoots resolves a tool's user/system specs into
// CandidateRoots, expanding ~ against sc.HomeDir and stamping Exists.
// toolDetected is the adapter's installed-heuristic result, copied onto
// every candidate it produces (the hint is per-tool, not per-root).
// Project-scope candidates are intentionally omitted: project scanning
// is not wired in this phase (see agentscan.Scan, which passes no
// ProjectRoots), so suggesting project roots would mislead.
func BuildCandidateRoots(sc ScanContext, toolKey string, toolDetected bool, specs []CandidateRootSpec) []CandidateRoot {
	out := make([]CandidateRoot, 0, len(specs))
	for _, spec := range specs {
		path := spec.Tmpl
		if len(path) > 0 && path[0] == '~' {
			expanded, err := ExpandHome(path, sc.HomeDir)
			if err != nil {
				// A ~-path we cannot expand (no home dir) is not a usable
				// candidate; skip it rather than surface a broken path.
				continue
			}
			path = expanded
		}
		out = append(out, CandidateRoot{
			ToolKey:      toolKey,
			Scope:        spec.Scope,
			Path:         path,
			DisplayTmpl:  spec.Tmpl,
			Exists:       DirExists(path),
			ToolDetected: toolDetected,
			Shared:       spec.Shared,
			Unconsumed:   spec.Unconsumed,
		})
	}
	return out
}
