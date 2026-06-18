// Package deploy is the server side of the SkillFleet deployment
// pipeline (v1.0 §9, §12 deployment_jobs, §14): it owns the
// deployment_jobs table and turns an operator's install intent into the
// authoritative new-content spec an agent executes.
//
// Split of responsibility (the central Phase 8 decision):
//
//   - The SERVER (this package: jobs.go + planner.go) knows the registry
//     — which version, its archive sha, its file list — and records
//     intent + the resolved plan. For install/state-change work it
//     addresses targets only by {tool_key, scope, root_id}; register_root
//     is a narrow exception that carries RootPath so the agent can locally
//     validate and persist an allowed root.
//   - The AGENT (internal/agentinstall) resolves that target against its
//     own allowed_roots, and owns everything that needs to see the local
//     disk: the diff against the prior install marker, backup, staging,
//     atomic replace, and rollback. Only the agent can see local edits
//     and untracked extra files, so only the agent decides what to
//     delete and what to preserve (§9.4).
//
// types.go (this file) holds the wire types that cross that boundary:
// Plan / FileSpec / InstallMarker / Result, plus the Operation and
// Status enums. They are plain JSON structs with no behaviour, so the
// agent can import them for (de)serialisation without linking the
// server-only registry/database code. The jobs Store (jobs.go) uses
// database/sql, which the agent already links transitively via
// inventory — importing deploy for these types adds no new heavy
// dependency to the agent binary, mirroring how inventory's wire types
// are shared today.
package deploy

import "github.com/yeluonight/skillfleet/internal/adapters"

// Operation enumerates what a deployment job does. Mirrors the migration
// 0011 CHECK set exactly.
type Operation string

const (
	// OpInstall writes a registry version into a tool/scope root with
	// backup + atomic replace + auto-rollback on failure (§9.3).
	OpInstall Operation = "install"
	// OpRollback restores a prior install from its backup (§14.1
	// POST /api/deployments/:id/rollback).
	OpRollback Operation = "rollback"
	// OpStateChange flips a skill's native enable/disable state in the
	// tool's out-of-band config (Phase 9): claude-code skillOverrides,
	// codex config.toml [[skills.config]].enabled, opencode
	// permission.skill. Unlike install it touches no skill files and
	// downloads nothing — the plan is just the target state, and the
	// agent does a safe read-modify-write of one config file.
	OpStateChange Operation = "state_change"
	// OpRegisterRoot asks the agent to add a locally validated allowed_root.
	// This is the only operation whose request may carry RootPath.
	OpRegisterRoot Operation = "register_root"
	// OpRemoveRoot asks the agent to remove an allowed_root by Target.RootID.
	OpRemoveRoot Operation = "remove_root"
)

func (o Operation) valid() bool {
	switch o {
	case OpInstall, OpRollback, OpStateChange, OpRegisterRoot, OpRemoveRoot:
		return true
	}
	return false
}

// Status is the deployment_jobs lifecycle. Mirrors the migration 0008
// CHECK set exactly. See the package and migration comments for who
// writes each transition.
type Status string

const (
	// StatusPending: created by the server, awaiting an agent claim.
	StatusPending Status = "pending"
	// StatusClaimed: an agent atomically took the job (CAS); no other
	// agent may run it.
	StatusClaimed Status = "claimed"
	// StatusRunning: optional progress phase reported by the executor.
	StatusRunning Status = "running"
	// StatusSucceeded: terminal; result_json holds what was written.
	StatusSucceeded Status = "succeeded"
	// StatusFailed: terminal; result_json holds the error (and, when the
	// executor auto-rolled-back, rolled_back: true).
	StatusFailed Status = "failed"
	// StatusExpired: expires_at passed before any agent claimed the job;
	// marked lazily at dispatch (no reaper goroutine).
	StatusExpired Status = "expired"
)

// Target addresses an install destination WITHOUT an absolute path. The
// agent resolves it against its own allowed_roots (v1.0 §9.1): RootID is
// the preferred key (the stable id of an allowed root); ToolKey + Scope
// are the human-meaningful fallback the WebUI shows and the agent
// matches when RootID is absent. The server never knows the absolute
// path these resolve to — that is the root of "the agent only ever
// writes inside an allowed root".
type Target struct {
	ToolKey string `json:"tool_key"`
	Scope   string `json:"scope"`
	RootID  string `json:"root_id,omitempty"`
}

// Request is the operator's intent, stored as request_json at job
// creation. For an install it names the skill, the version, and the
// target; for a rollback it names the prior job to undo; for a state
// change it names the skill, the target, and the desired state; for root
// registration/removal it names the root target. The fields not relevant
// to an operation are simply empty.
type Request struct {
	Operation Operation `json:"operation"`

	// Install fields.
	SkillName string `json:"skill_name,omitempty"`
	VersionID string `json:"version_id,omitempty"`
	Target    Target `json:"target,omitempty"`

	// Rollback field: the deployment job whose install is being undone.
	RollsBackJobID string `json:"rolls_back_job_id,omitempty"`

	// State-change field: the enable/disable state to set. Reuses
	// SkillName + Target above to address the skill. Empty for other
	// operations.
	DesiredState adapters.EffectiveState `json:"desired_state,omitempty"`

	// Root-registration field: the absolute path the WebUI asked the agent
	// to register. This is the sole exception to the usual "server never
	// sends absolute paths" rule; the agent must still validate it locally
	// before writing allowed_roots.
	RootPath string `json:"root_path,omitempty"`

	// RequestedBy is the user id that created the job (provenance/audit).
	RequestedBy string `json:"requested_by,omitempty"`
}

// FileSpec describes one file the agent must write, copied from the
// registry version's manifest (skill.File). The agent verifies the
// staged tree against this list (count + per-file sha) before it touches
// the live install directory (§9.3 step 8).
type FileSpec struct {
	Path   string `json:"path"`   // forward-slash, package-relative (safefs-clean)
	SHA256 string `json:"sha256"` // lowercase hex of the file bytes
	Size   int64  `json:"size"`
	Exec   bool   `json:"exec"`
	Binary bool   `json:"binary"`
}

// InstallMarker is the content of the .skillfleet-install.json file the
// agent writes into every managed install directory (v1.0 §9.2). It is
// the record of "what SkillFleet put here", which the NEXT install reads
// to know which files it is allowed to delete (§9.4) — the single source
// of truth for the managed file set.
//
// The file name is fixed (".skillfleet-install.json", leading dot) so
// that fingerprint.Compute skips it: the post-install rescan must hash
// only the skill's own content to match plan.ContentSHA256.
type InstallMarker struct {
	ManagedBy          string        `json:"managed_by"` // always "skillfleet"
	SkillName          string        `json:"skill_name"`
	InstalledVersionID string        `json:"installed_version_id"`
	BaseVersionID      string        `json:"base_version_id,omitempty"`
	Source             *MarkerSource `json:"source,omitempty"`
	ContentSHA256      string        `json:"content_sha256"`
	Files              []string      `json:"files"` // managed file paths, sorted
	InstalledAt        string        `json:"installed_at"`
}

// MarkerSource records the upstream provenance in the install marker
// (v1.0 §9.2). All fields optional — a skill created in the WebUI with
// no upstream has no source block.
type MarkerSource struct {
	Type    string `json:"type,omitempty"`
	URL     string `json:"url,omitempty"`
	RefType string `json:"ref_type,omitempty"`
	RefName string `json:"ref_name,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Subdir  string `json:"subdir,omitempty"`
}

// SharedHint carries advisory notes for a target that participates in the
// cross-tool .agents/skills convention. AlreadyCovered means the same skill
// content is already visible through the shared agents root on that device.
type SharedHint struct {
	Readers         []SharedReader `json:"readers,omitempty"`
	AlreadyCovered  bool           `json:"already_covered,omitempty"`
	CoveredByRootID string         `json:"covered_by_root_id,omitempty"`
}

// PlanHint carries advisory install-planning notes. It never affects agent
// execution; it exists so the WebUI can explain shared-directory coverage and
// duplicate-content cases before the operator queues a job.
type PlanHint struct {
	Shared *SharedHint `json:"shared,omitempty"`
}

// Plan is the server-resolved authoritative new-content spec, stored as
// plan_json. The planner fills it from a registry version so the agent
// does not re-derive what to write; the agent trusts it because it
// arrives over the HMAC-signed downlink. The agent still independently
// verifies the downloaded archive's sha against ArchiveSHA256 and the
// post-install rescan against ContentSHA256 — trust, but verify.
type Plan struct {
	VersionID     string `json:"version_id"`
	SkillName     string `json:"skill_name"`
	ContentSHA256 string `json:"content_sha256"` // rescan target (tree hash)
	ArchiveSHA256 string `json:"archive_sha256"` // download integrity check
	ArchiveBytes  int64  `json:"archive_bytes"`
	// DownloadPath is the agent-relative URL to fetch the package from,
	// e.g. "/agent/packages/sv_xxx". The agent prepends its server URL.
	DownloadPath string `json:"download_path"`
	// Marker is the install marker to write after a successful swap. The
	// agent fills its Files + InstalledAt (the agent owns the final file
	// list and the install timestamp); the rest is server-supplied.
	Marker InstallMarker `json:"marker"`
	// Files is the expected file set (from the version manifest), used to
	// verify the staged tree before replacing the live directory.
	Files []FileSpec `json:"files"`
}

// Result is the agent's execution outcome, stored as result_json when it
// reports. files_written / files_deleted / extra_files give the operator
// the full picture (§9.4 surfaces extras for manual handling);
// RescanContentSHA256 is what the agent measured after install (it must
// equal Plan.ContentSHA256 for success); RolledBack records whether an
// auto-rollback fired.
type Result struct {
	ResolvedRootPath    string   `json:"resolved_root_path,omitempty"`
	FilesWritten        []string `json:"files_written,omitempty"`
	FilesDeleted        []string `json:"files_deleted,omitempty"`
	ExtraFiles          []string `json:"extra_files,omitempty"`
	BackupPath          string   `json:"backup_path,omitempty"`
	RescanContentSHA256 string   `json:"rescan_content_sha256,omitempty"`
	RolledBack          bool     `json:"rolled_back,omitempty"`
	ErrorCode           string   `json:"error_code,omitempty"`
	ErrorMessage        string   `json:"error_message,omitempty"`
	DurationMS          int64    `json:"duration_ms,omitempty"`
}
