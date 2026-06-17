-- SkillFleet deployment_jobs operation set extension (phase 9 t1).
--
-- Scope: widen deployment_jobs.operation's CHECK set to admit the new
-- 'state_change' operation (Phase 9 enable/disable management) alongside
-- the existing 'install' / 'rollback' from migration 0008.
--
-- Why a table rebuild. SQLite cannot ALTER a CHECK constraint in place
-- (no ALTER TABLE ... DROP/ADD CONSTRAINT). The supported, durable recipe
-- (https://sqlite.org/lang_altertable.html §7, "otherwise") is:
--   1. CREATE a new table with the desired schema.
--   2. INSERT INTO new SELECT * FROM old  -- carry every existing row.
--   3. DROP the old table.
--   4. ALTER TABLE new RENAME TO old.
--   5. Recreate indexes (they are dropped with the old table).
-- The migration runner wraps this whole file in one transaction
-- (migrations.applyOne), so it is all-or-nothing: a crash mid-rebuild
-- leaves the original table intact.
--
-- foreign_keys note. The runner runs each migration inside a transaction;
-- PRAGMA foreign_keys cannot be toggled mid-transaction, and the official
-- recipe's alternative path (foreign_keys=OFF) is therefore unavailable
-- here. That is fine because nothing REFERENCES deployment_jobs (verified:
-- no inbound FK in any migration), so dropping/renaming the table breaks
-- no child rows. deployment_jobs' own OUTBOUND FK to devices(id) is
-- re-declared identically on the new table and re-validated as rows are
-- copied in; every existing job already has a live device_id, so the
-- INSERT...SELECT cannot violate it.
--
-- Data preservation. Every column is copied 1:1; existing install /
-- rollback jobs (any status, including terminal succeeded/failed rows
-- the WebUI still lists) survive byte-for-byte. Only the operation CHECK
-- set changes; all other columns, types, STRICT, and the device FK with
-- ON DELETE CASCADE are reproduced exactly as in 0008.

CREATE TABLE deployment_jobs_new (
    id           TEXT PRIMARY KEY,
    device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    operation    TEXT NOT NULL CHECK (operation IN
                     ('install', 'rollback', 'state_change')),
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

-- Recreate the (device_id, status, created_at) index the claim hot path
-- and the WebUI listing both rely on — identical to 0008.
CREATE INDEX idx_deployment_jobs_device_status
    ON deployment_jobs(device_id, status, created_at);
