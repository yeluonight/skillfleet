-- SkillFleet initial schema (phase 1 t2).
--
-- This migration only sets up infrastructure that the migration runner
-- itself needs to be reasoned about: the schema_migrations bookkeeping
-- table is created by the runner before applying any file, so the file
-- below is intentionally minimal. Business tables (users, sessions,
-- enrollment_tokens, audit_logs) arrive in phase 1 t3.

CREATE TABLE schema_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

INSERT INTO schema_meta (key, value) VALUES
    ('initialized_at', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
