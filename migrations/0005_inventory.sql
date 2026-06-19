-- SkillFleet inventory tables (phase 3 t8).
--
-- Scope:
--   * projects           -- device-registered project roots scanned at project scope
--   * tool_instances     -- one row per (device, tool, scope-root) the agent found
--   * inventory_runs     -- metadata for each /agent/inventory submission
--   * discovered_skills  -- the device x tool x scope x skill matrix rows
--
-- Conventions inherited from 0002 / 0004:
--   * Millisecond unix timestamps as INTEGER, suffix "_at".
--   * Application-assigned text IDs (no AUTOINCREMENT).
--   * STRICT so column-affinity typos fail at INSERT, not at read.
--
-- Replacement model: an inventory run is the unit of freshness. Each
-- run inserts one inventory_runs row, then replaces this device's
-- tool_instances + discovered_skills wholesale (delete-by-device then
-- insert) inside one transaction. There is no per-skill diff at the DB
-- layer in Phase 3 — the latest run is the truth; drift detection that
-- compares against installed versions arrives in later phases.

-- projects: a path on a device the operator registered so its
-- project-scope skill roots get scanned. device_id references the
-- enrolled device; ON DELETE CASCADE keeps projects from outliving
-- their device.
CREATE TABLE projects (
    id         TEXT PRIMARY KEY,
    device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    path       TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_projects_device ON projects(device_id);
-- A device shouldn't register the same path twice.
CREATE UNIQUE INDEX idx_projects_device_path ON projects(device_id, path);

-- inventory_runs: one row per accepted /agent/inventory submission.
-- skill_count / root_count are denormalised summaries so the WebUI
-- device list can show "42 skills across 6 roots" without a join.
CREATE TABLE inventory_runs (
    id            TEXT PRIMARY KEY,
    device_id     TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    started_at    INTEGER NOT NULL,
    skill_count   INTEGER NOT NULL,
    root_count    INTEGER NOT NULL,
    agent_version TEXT,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_inventory_runs_device ON inventory_runs(device_id, created_at);

-- tool_instances: one row per (device, tool, scope-root) discovered.
-- root_id is the adapter-local id (e.g. "claude_user"); the pair
-- (device_id, root_id) is unique within the latest run. config_json
-- holds any adapter-specific summary the WebUI may render later.
CREATE TABLE tool_instances (
    id              TEXT PRIMARY KEY,
    device_id       TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    run_id          TEXT NOT NULL REFERENCES inventory_runs(id) ON DELETE CASCADE,
    tool_key        TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    scope           TEXT NOT NULL CHECK (scope IN ('user', 'project', 'system')),
    root_id         TEXT NOT NULL,
    root_path       TEXT NOT NULL,
    last_scanned_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_tool_instances_device ON tool_instances(device_id);
CREATE INDEX idx_tool_instances_run    ON tool_instances(run_id);

-- discovered_skills: the matrix. One row per skill found under a tool
-- instance's root. effective_state uses the shared adapters vocabulary;
-- native_state preserves the tool's own string. content_sha256 is the
-- directory fingerprint (internal/fingerprint). warnings_json is a
-- JSON array of {code,message} so the WebUI can surface parser /
-- adapter findings inline.
CREATE TABLE discovered_skills (
    id               TEXT PRIMARY KEY,
    device_id        TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    run_id           TEXT NOT NULL REFERENCES inventory_runs(id) ON DELETE CASCADE,
    tool_instance_id TEXT NOT NULL REFERENCES tool_instances(id) ON DELETE CASCADE,
    tool_key         TEXT NOT NULL,
    scope            TEXT NOT NULL CHECK (scope IN ('user', 'project', 'system')),
    name             TEXT NOT NULL,
    skill_path       TEXT NOT NULL,
    has_skill_md     INTEGER NOT NULL CHECK (has_skill_md IN (0, 1)),
    description      TEXT,
    effective_state  TEXT NOT NULL CHECK (effective_state IN
                         ('on', 'off', 'name-only', 'user-invocable-only', 'ask', 'unknown')),
    native_state     TEXT,
    content_sha256   TEXT,
    file_count       INTEGER NOT NULL DEFAULT 0,
    total_bytes      INTEGER NOT NULL DEFAULT 0,
    warnings_json    TEXT,
    created_at       INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_discovered_skills_device ON discovered_skills(device_id);
CREATE INDEX idx_discovered_skills_run    ON discovered_skills(run_id);
CREATE INDEX idx_discovered_skills_tool   ON discovered_skills(device_id, tool_key, scope);
