package adapters

import "path/filepath"

// RootSpec describes one tool-specific skill root template. Concrete
// adapters keep one ordered slice of these specs and use it to build both
// scan roots and candidate-registration suggestions.
type RootSpec struct {
	// IDBase is the root id for user/system specs and the prefix for project
	// specs (ProjectRootID appends the project index).
	IDBase string
	Scope  Scope
	// Tmpl is a ~-relative user path, an absolute system path, or a project
	// relative path using slash separators (e.g. ".agents/skills").
	Tmpl string
	// Shared marks the cross-tool .agents/skills root for candidate display.
	Shared bool
}

// SkillRootsFromSpecs resolves existing scan roots from specs. User/system
// specs become at most one root; project specs are resolved once per
// ScanContext.ProjectRoots entry.
func SkillRootsFromSpecs(sc ScanContext, toolKey string, specs []RootSpec) ([]SkillRoot, error) {
	var roots []SkillRoot
	for _, spec := range specs {
		switch spec.Scope {
		case ScopeProject:
			for i, proj := range sc.ProjectRoots {
				p := filepath.Join(proj, filepath.FromSlash(spec.Tmpl))
				if DirExists(p) {
					roots = append(roots, SkillRoot{
						ID:    ProjectRootID(spec.IDBase, i),
						Tool:  toolKey,
						Scope: spec.Scope,
						Path:  p,
					})
				}
			}
		default:
			p := spec.Tmpl
			if len(p) > 0 && p[0] == '~' {
				expanded, err := ExpandHome(p, sc.HomeDir)
				if err != nil {
					return nil, err
				}
				p = expanded
			}
			if DirExists(p) {
				roots = append(roots, SkillRoot{ID: spec.IDBase, Tool: toolKey, Scope: spec.Scope, Path: p})
			}
		}
	}
	return roots, nil
}

// CandidateSpecsFromRootSpecs turns user/system root specs into candidate
// specs. Project roots are intentionally omitted until project-scope scanning
// is wired through the agent inventory loop.
func CandidateSpecsFromRootSpecs(specs []RootSpec) []CandidateRootSpec {
	out := make([]CandidateRootSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.Scope == ScopeProject {
			continue
		}
		out = append(out, CandidateRootSpec{Scope: spec.Scope, Tmpl: spec.Tmpl, Shared: spec.Shared})
	}
	return out
}

// BuildCandidateRootsFromRootSpecs resolves candidate roots from the same
// root spec list an adapter uses for SkillRoots.
func BuildCandidateRootsFromRootSpecs(sc ScanContext, toolKey string, toolDetected bool, specs []RootSpec) []CandidateRoot {
	return BuildCandidateRoots(sc, toolKey, toolDetected, CandidateSpecsFromRootSpecs(specs))
}
