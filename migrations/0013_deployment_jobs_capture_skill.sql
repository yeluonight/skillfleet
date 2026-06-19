-- SkillFleet deployment_jobs capture-skill operation (mgmt-refactor track A).
--
-- Scope: widen deployment_jobs.operation's CHECK set to admit the new
-- 'capture_skill' operation. A capture_skill job asks an agent to read a
-- discovered skill's real files from one of its local roots and upload the
-- bytes so the server can adopt them into the registry (device -> registry,
-- the inverse of an install). This is what closes the gap where a skill that
-- only exists on a device could never be edited/versioned because it was
-- never in the registry.
--
-- Why a table rebuild. SQLite cannot ALTER a CHECK constraint in place, so
-- this follows the same create/copy/drop/rename recipe as 0009/0011. The
-- migration runner wraps the file in one transaction, and no table references
-- deployment_jobs, so the rebuild preserves data without FK gymnastics.
--
-- Data preservation. Every column is copied 1:1; existing install, rollback,
-- state_change, register_root, and remove_root jobs survive byte-for-byte.
-- Only the operation CHECK set changes; all other columns, types, STRICT, and
-- the device FK with ON DELETE CASCADE are reproduced exactly as in 0011.

CREATE TABLE deployment_jobs_new (
    id           TEXT PRIMARY KEY,
    device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    operation    TEXT NOT NULL CHECK (operation IN
                     ('install', 'rollback', 'state_change',
                      'register_root', 'remove_root', 'capture_skill')),
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

INSERT INTO deployment_jobs_new
    (id, device_id, operation, status, request_json, plan_json,
     result_json, created_at, updated_at, expires_at)
SELECT
    id, device_id, operation, status, request_json, plan_json,
    result_json, created_at, updated_at, expires_at
FROM deployment_jobs;

DROP TABLE deployment_jobs;

ALTER TABLE deployment_jobs_new RENAME TO deployment_jobs;

CREATE INDEX idx_deployment_jobs_device_status
    ON deployment_jobs(device_id, status, created_at);
