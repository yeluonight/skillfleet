-- SkillFleet deployment_jobs table (phase 8 t1).
--
-- Scope:
--   * deployment_jobs -- the server→agent downlink work queue (v1.0 §12,
--     §9.3 install flow, §14.2 GET /agent/jobs).
--
-- This is the table that finally gives the agent something to PULL. Up
-- to phase 7 the agent was upload-only (enroll / heartbeat / inventory);
-- a deployment_jobs row is a unit of downlink work — "install version X
-- of skill Y onto tool T / scope S on this device", or "roll job J
-- back". The agent claims a pending row addressed to its own device_id,
-- executes it against the local filesystem (internal/agentinstall), and
-- reports the outcome back; the server only ever records intent and
-- result, never touches the device's disk.
--
-- Conventions inherited from 0002 / 0004 / 0005 / 0006 / 0007:
--   * Millisecond unix timestamps as INTEGER, suffix "_at".
--   * Application-assigned text IDs (no AUTOINCREMENT), prefix "dj".
--   * STRICT so column-affinity typos fail at INSERT, not at read.
--   * Enumerated columns guarded by CHECK so a typo can't produce an
--     un-dispatchable / un-recognisable job.
--
-- operation enumerates what the job does:
--   * install  -- write a registry version into a tool/scope root, with
--                 backup + atomic replace + auto-rollback on failure.
--   * rollback -- restore a prior install from its backup (manual undo,
--                 v1.0 §14.1 POST /api/deployments/:id/rollback).
-- The "remove" (full uninstall) operation is intentionally NOT in the
-- CHECK set: §17 Phase 8 does not require it, and a narrow enum keeps an
-- unimplemented operation from being dispatched. A later phase can add
-- it to the CHECK set when the executor learns to perform it.
--
-- status is the job lifecycle (see internal/deploy state machine):
--   * pending   -- created by the server, awaiting an agent claim.
--   * claimed   -- an agent has atomically taken the job (CAS in
--                  GET /agent/jobs); no other agent may run it.
--   * running   -- optional progress phase reported by the executor.
--   * succeeded -- terminal; result_json holds what was written.
--   * failed    -- terminal; result_json holds the error (and, when the
--                  executor auto-rolled-back, rolled_back: true).
--   * expired   -- the job's expires_at passed before any agent claimed
--                  it; marked lazily at dispatch time (no reaper).
--
-- request_json (NOT NULL) is the operator's intent, written at creation:
-- which skill / version / target {tool_key, scope, root_id} (for
-- install) or which prior job to undo (for rollback). plan_json is the
-- server-resolved authoritative new-content spec (version_id,
-- content_sha256, archive sha + size, the §9.2 marker, the file list);
-- the planner fills it so the agent does not re-derive what to write.
-- result_json is the agent's outcome (files written/deleted, extras
-- preserved, backup path, rescan sha, error). Both are nullable: a
-- freshly created job has request_json only; plan_json lands at creation
-- for installs (the planner runs server-side) and result_json lands when
-- the agent reports.
--
-- device_id has a real FK to devices(id) with ON DELETE CASCADE: a job
-- is meaningless without its target device, and deleting a device should
-- not strand orphan jobs. (Contrast skill_versions.source_id in 0007,
-- which stays an app-guarded nullable column because skill_versions is
-- immutable and content-addressed; deployment_jobs is mutable workflow
-- state, so the standard FK is the honest model here.)
CREATE TABLE deployment_jobs (
    id           TEXT PRIMARY KEY,
    device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    operation    TEXT NOT NULL CHECK (operation IN ('install', 'rollback')),
    status       TEXT NOT NULL CHECK (status IN
                     ('pending', 'claimed', 'running',
                      'succeeded', 'failed', 'expired')),
    request_json TEXT NOT NULL,
    plan_json    TEXT,
    result_json  TEXT,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    expires_at   INTEGER
) STRICT;

-- The agent's claim query filters by (device_id, status) and orders by
-- created_at (oldest pending first); this index serves both that hot
-- path and the WebUI's per-device / per-status job listing.
CREATE INDEX idx_deployment_jobs_device_status
    ON deployment_jobs(device_id, status, created_at);
