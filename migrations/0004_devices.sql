-- SkillFleet device-tier tables (phase 2 t3).
--
-- Scope:
--   * devices         -- one row per enrolled edge agent
--   * device_secrets  -- 1:1 with devices; stores sha256(secret), not the secret itself
--   * agent_nonces    -- (device_id, nonce) replay-protection table (v2.0 §5.7)
--
-- Conventions inherited from 0002_auth_core:
--   * Millisecond unix timestamps as INTEGER, suffix "_at".
--   * Application-assigned text IDs (no AUTOINCREMENT).
--   * STRICT so column-affinity typos fail at INSERT, not at read.
--
-- Status machine for devices:
--   pending  -- enrolled with a token but not yet approved by an admin
--   approved -- HMAC requests accepted
--   revoked  -- HMAC requests rejected; row kept for audit / re-enroll path
-- Enforced via CHECK so application code cannot drift the vocabulary.

CREATE TABLE devices (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    hostname      TEXT,
    os            TEXT,
    arch          TEXT,
    agent_version TEXT,
    status        TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'revoked')),
    last_seen_at  INTEGER,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_devices_status        ON devices(status);
CREATE INDEX idx_devices_last_seen_at  ON devices(last_seen_at);

-- device_secrets stores sha256(secret) only. The plaintext secret is
-- returned exactly once in the /agent/enroll response; the agent
-- persists it locally and the server forgets it forever after.
-- ON DELETE CASCADE so deleting a device row cleans up the secret in
-- the same transaction; orphaned secrets would be a quiet leak.
CREATE TABLE device_secrets (
    device_id   TEXT PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    secret_hash TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    rotated_at  INTEGER
) STRICT;

-- agent_nonces is the HMAC replay-protection table (v2.0 §5.7).
-- Composite primary key (device_id, nonce) means a duplicate
-- INSERT — i.e. a replay — fails with a UNIQUE constraint violation
-- that the middleware turns into a 401. CASCADE deletes here so
-- revoked devices don't keep nonces lingering forever.
CREATE TABLE agent_nonces (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    nonce     TEXT NOT NULL,
    used_at   INTEGER NOT NULL,
    PRIMARY KEY (device_id, nonce)
) STRICT;

CREATE INDEX idx_agent_nonces_used_at ON agent_nonces(used_at);
