// Package drift decides whether a skill installed on a device has been
// locally modified relative to the central registry (v1.0 §8.1–§8.2).
//
// The model has three layers — Upstream / Registry / Installation. This
// package answers one question about the bottom layer: is the copy a
// device currently has on disk identical to some version the registry
// knows, or has someone edited it locally?
//
// Correlation inference, not an installation record (ADR for Phase 7):
// we do NOT track which registry version a device "installed". Phase 7
// has no install action that could record one (that arrives in Phase 8
// with skill_installations + a server→agent downlink). Instead we infer
// state by content: the agent's inventory already reports a directory
// fingerprint (content_sha256, internal/fingerprint) for every skill it
// finds. We compare that against the set of content_sha256 the registry
// holds for the same skill name:
//
//   - the local sha matches one of them      → clean (running a known version)
//   - it matches none, but the name is known → local_modified (edited on disk)
//   - the name is unknown to the registry    → untracked (device-only skill)
//
// Comparing by content_sha256 (not by commit, path, or mtime) is the
// load-bearing guard: a skill whose bytes match a registry version is
// clean no matter how it got there, and noise that does not change bytes
// never produces a false local_modified.
package drift

// LocalState is an installed skill's status relative to the registry.
type LocalState string

const (
	// StateClean: the device's content_sha256 matches some registry
	// version of this skill — it is running a known, unmodified version.
	StateClean LocalState = "clean"

	// StateLocalModified: the registry knows this skill name but holds no
	// version with the device's content_sha256 — the on-disk copy was
	// edited locally (§8.2 local_modified).
	StateLocalModified LocalState = "local_modified"

	// StateUntracked: the registry has no version for this skill name, so
	// there is nothing to compare against — the skill exists only on the
	// device. (Also the fallback when no fingerprint was reported.)
	StateUntracked LocalState = "untracked"
)

// SkillDrift is the computed drift for one discovered skill on a device.
type SkillDrift struct {
	// Name / ToolKey / Scope identify the discovered_skills row this
	// drift was computed from (the device's tool × scope × name matrix).
	Name    string `json:"name"`
	ToolKey string `json:"tool_key"`
	Scope   string `json:"scope"`

	// LocalSHA is the device's reported directory fingerprint
	// (discovered_skills.content_sha256). Empty when the agent could not
	// fingerprint the directory.
	LocalSHA string `json:"local_sha,omitempty"`

	// LocalState is the classification (clean / local_modified / untracked).
	LocalState LocalState `json:"local_state"`

	// MatchedVersionID is the registry version whose content_sha256 equals
	// LocalSHA, set only when LocalState is clean. It tells the UI exactly
	// which central version the device is running.
	MatchedVersionID string `json:"matched_version_id,omitempty"`

	// RegistryVersionCount is how many versions the registry holds for
	// this skill name. Zero ⇒ untracked. Surfaced so the UI can say
	// "device-only skill" vs "edited copy of a tracked skill".
	RegistryVersionCount int `json:"registry_version_count"`
}

// Classify decides a skill's LocalState from its local fingerprint and
// the registry's known fingerprints for the same name.
//
//   - registrySHAs maps each known content_sha256 → the version id that
//     has it (one entry per distinct sha; callers dedupe).
//   - hasName reports whether the registry holds ANY version for this
//     skill name. It is passed separately from len(registrySHAs) because
//     a name can be known yet (pathologically) have no fingerprints; the
//     name being known is what distinguishes local_modified from
//     untracked.
//
// It returns the state and, when clean, the matched version id.
//
// The clean branch is the core guard: an exact content_sha256 match is
// clean regardless of how the bytes arrived. Mutation check — replace
// the `if id, ok := registrySHAs[localSHA]` hit with an unconditional
// StateLocalModified and TestClassify_ShaMatchesIsClean goes red,
// proving the guard is reached, not coincidentally satisfied.
func Classify(localSHA string, registrySHAs map[string]string, hasName bool) (LocalState, string) {
	// No fingerprint reported: we cannot assert the copy matches anything,
	// so we do not claim local_modified (which would be a false positive).
	// Treat as untracked — the UI shows "no fingerprint" rather than a
	// fabricated modification.
	if localSHA == "" {
		return StateUntracked, ""
	}

	// Exact content match ⇒ running a known version. This is the guard
	// that keeps commit/path/mtime noise from ever reading as a change.
	if id, ok := registrySHAs[localSHA]; ok {
		return StateClean, id
	}

	// No content match. If the registry tracks this name at all, the
	// on-disk copy diverged from every known version ⇒ local_modified.
	if hasName {
		return StateLocalModified, ""
	}

	// Registry has never heard of this skill name ⇒ untracked.
	return StateUntracked, ""
}
