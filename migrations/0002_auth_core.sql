-- SkillFleet auth-core tables (phase 1 t3).
--
-- Scope: only the four tables required by Phase 1 auth/audit flow.
-- Device / skill / deployment tables arrive in later phases.
--
-- Conventions:
--   * Timestamps are unix milliseconds, stored as INTEGER. The "_at"
--     suffix implies millis everywhere in the schema so callers don't
--     have to remember unit per column.
--   * IDs are application-assigned ULIDs (text). No autoincrement.
--   * STRICT enforces declared column types — typos surface at INSERT
--     time instead of silently storing the wrong affinity.
--   * Indexes are added only for query paths the auth code actually
--     uses. Adding more later is cheap; over-indexing now is not.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,            -- argon2id encoded, see §5.2
    created_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    id            TEXT PRIMARY KEY,         -- opaque session id (cookie value carries this)
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_hash  TEXT NOT NULL,            -- HMAC over (id, server_secret); guards against id-only theft
    ip            TEXT,
    user_agent    TEXT,
    created_at    INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    revoked_at    INTEGER                   -- set when explicitly logged out / password changed
) STRICT;

CREATE INDEX idx_sessions_user_id     ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at  ON sessions(expires_at);

CREATE TABLE enrollment_tokens (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,       -- sha256(token); raw token never stored
    status      TEXT NOT NULL,              -- pending | used | revoked
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    used_at     INTEGER
) STRICT;

CREATE INDEX idx_enrollment_tokens_expires_at ON enrollment_tokens(expires_at);

CREATE TABLE audit_logs (
    id           TEXT PRIMARY KEY,
    actor_type   TEXT NOT NULL,             -- user | agent | system
    actor_id     TEXT,                      -- nullable: system actions have no actor row
    action       TEXT NOT NULL,             -- dotted namespace, e.g. auth.login.success
    target_type  TEXT,
    target_id    TEXT,
    detail_json  TEXT,                      -- arbitrary JSON; queried via WebUI, not joined
    created_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at DESC);
CREATE INDEX idx_audit_logs_action     ON audit_logs(action, created_at DESC);
