-- SkillFleet setup-state singleton (phase 1 t4).
--
-- Tracks the one-time admin bootstrap code printed to stderr on first
-- boot (v1.0 §5.1). The table holds at most one row (id = 1).
--
-- Lifecycle:
--   * users empty + no row        -> server generates code on boot,
--                                    INSERTs row, stderr-prints code.
--   * users empty + pending row   -> server *regenerates* a new code,
--                                    overwrites code_hash + created_at,
--                                    stderr-prints the new code. The
--                                    prior code (only ever displayed to
--                                    a terminal, never persisted in
--                                    plaintext) becomes unusable.
--   * users non-empty             -> setup complete; row's code_hash
--                                    is NULL and consumed_at is set.
--                                    Server skips generation entirely.
--
-- Why regenerate on power-loss instead of preserving the old code:
-- hashes are one-way, so the server cannot replay the prior plaintext
-- to the operator. Forcing a fresh code keeps the contract simple
-- (whatever is in the most recent boot log is correct) and is safe
-- because the prior code, by definition, was never consumed.

CREATE TABLE setup_state (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    code_hash           TEXT,                                       -- sha256(setup_code) hex; NULL after consumption
    code_created_at     INTEGER,                                    -- unix ms; NULL after consumption
    consumed_at         INTEGER,                                    -- unix ms; NULL while pending
    consumed_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL
) STRICT;
