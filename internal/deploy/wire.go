package deploy

import "github.com/yeluonight/skillfleet/internal/adapters"

// wire.go holds the HTTP envelope types for the server↔agent downlink
// (v1.0 §14.2: GET /agent/jobs, POST /agent/jobs/{id}/result). These were
// previously defined twice — once on the server (agentapi) and once on the
// agent client (agentclient) — and had to stay byte-identical by hand.
// Defining them once here, in the package both sides already import for
// the Plan/Result content types, removes that drift risk. The agent binary
// links deploy for these structs without pulling in the server-only
// registry/database code (the same rationale as types.go).

// ClaimedJob is the response body of GET /agent/jobs when a job is
// claimed. Operation is a deploy.Operation string; RequestJSON is the
// marshalled Request (operation + target the agent resolves); PlanJSON is
// the marshalled Plan, passed through verbatim and omitted for jobs that
// carry no plan (e.g. rollback, which uses request/plan_json differently).
type ClaimedJob struct {
	ID          string `json:"id"`
	Operation   string `json:"operation"`
	RequestJSON string `json:"request_json"`
	PlanJSON    string `json:"plan_json,omitempty"`
}

// JobResult is the request body of POST /agent/jobs/{id}/result. Status is
// "succeeded" or "failed" (a terminal deploy.Status); ResultJSON is the
// marshalled Result the agent produced.
type JobResult struct {
	Status     string `json:"status"`
	ResultJSON string `json:"result_json"`
}

// RollbackPlan is the plan_json the server writes for a rollback job and
// the agent unmarshals to restore a prior install's backup. The server
// fills Target/SkillName/BackupDir from the original install job's request
// + result; the agent reads them (and BackupWasEmpty, which the executor
// sets) to drive safefs.RestoreBackup. Previously the server hand-built
// this as a map[string]any while the agent decoded it into an
// agentinstall.RollbackSpec — two implicit copies of one shape; this is
// the shared definition.
type RollbackPlan struct {
	// Target addresses the same allowed root the original install used.
	Target Target `json:"target"`
	// SkillName is the skill directory under the root to restore.
	SkillName string `json:"skill_name"`
	// BackupDir is the absolute path of the original install's backup
	// (Result.BackupPath). May be empty when the original was a first
	// install with nothing to back up → restoring "empty" removes the skill.
	BackupDir string `json:"backup_dir"`
	// BackupWasEmpty records that the original backup captured an empty
	// install (the skill didn't exist before). Restoring it uninstalls.
	// omitempty so a rollback plan built without it (the common case —
	// the server doesn't set it) is byte-identical to the prior
	// hand-built map[string]any that omitted the key entirely.
	BackupWasEmpty bool `json:"backup_was_empty,omitempty"`
}

// StateChangePlan is the plan_json the server writes for a state_change
// job and the agent unmarshals to flip a skill's native enable state
// (Phase 9). It deliberately does NOT reuse Plan: a state change carries
// no archive, no file list, no install marker, no content sha — it is
// just "set skill X under this tool/scope/root to state S". The agent
// resolves Target against its allowed_roots (so the server still never
// sends an absolute path), locates the tool's out-of-band config file,
// and does a safe read-modify-write that preserves every unrelated key.
//
// DesiredState is the same adapters.EffectiveState vocabulary the scan
// side reports, validated against the per-tool matrix (statematrix.go)
// before the job is minted; the agent's writer maps it to the tool's
// native value (e.g. opencode on→"allow", ask→"ask", off→"deny").
type StateChangePlan struct {
	// Target addresses the allowed root whose config governs this skill.
	Target Target `json:"target"`
	// SkillName is the skill the state change applies to. For codex this
	// also identifies the SKILL.md path the config entry keys on; for
	// claude-code / opencode it is the override / permission map key.
	SkillName string `json:"skill_name"`
	// DesiredState is the target enable/disable state (validated against
	// SupportedStates(Target.ToolKey) at plan time).
	DesiredState adapters.EffectiveState `json:"desired_state"`
}
