-- SkillFleet source-binding table (phase 6 t1).
--
-- Scope:
--   * skill_sources -- the upstream a skill is bound to (v1.0 §6)
--
-- A skill_sources row records WHERE a skill came from and HOW to check
-- it for upstream changes: the repo URL, the ref (branch/tag/commit),
-- the subdir the skill lives in, and the last commit + time we polled.
-- skill_versions.source_id / skill_drafts.source_id (added nullable in
-- 0006) point here.
--
-- Conventions inherited from 0002 / 0004 / 0005 / 0006:
--   * Millisecond unix timestamps as INTEGER, suffix "_at".
--   * Application-assigned text IDs (no AUTOINCREMENT).
--   * STRICT so column-affinity typos fail at INSERT, not at read.
--
-- source_type enumerates how the skill is bound (v1.0 §6.1). The CHECK
-- set is the full v1.0 enum even though phase 6 only actively drives the
-- git/github kinds; the others (webui_created, *_import, zip_upload,
-- unknown_external) are written by earlier/later phases and must be
-- accepted here so a single column describes every skill's origin.
--
-- ref_type narrows what ref_name means (branch | tag | commit |
-- release); kept as a CHECK so a typo can't silently produce an
-- uncheckable source. config_json is a reserved, nullable extension
-- slot (v1.0 §12 leaves it unexplained); phase 6 writes nothing to it.
-- Credential storage is deliberately NOT modelled here — phase 6 binds
-- public repos only, and encrypted-credential storage is a separate
-- security task for a later phase (§1.3), so there is no token/secret
-- column to leak.
CREATE TABLE skill_sources (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    source_type        TEXT NOT NULL CHECK (source_type IN
                           ('webui_created', 'local_import', 'device_import',
                            'git_repo', 'github_repo', 'github_release',
                            'zip_upload', 'unknown_external')),
    source_url         TEXT,
    provider           TEXT,
    owner              TEXT,
    repo               TEXT,
    ref_type           TEXT CHECK (ref_type IS NULL OR ref_type IN
                           ('branch', 'tag', 'commit', 'release')),
    ref_name           TEXT,
    subdir             TEXT,
    last_checked_at    INTEGER,
    last_remote_commit TEXT,
    config_json        TEXT,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_skill_sources_type ON skill_sources(source_type);

-- On the absence of a retrofitted FK for skill_versions.source_id and
-- skill_drafts.source_id:
--
-- SQLite cannot ALTER TABLE ... ADD CONSTRAINT; adding a real FK to an
-- existing column requires the 12-step table rebuild (new table -> copy
-- -> drop -> rename). For skill_versions that means rebuilding the
-- IMMUTABLE, content-addressed version table (§1.3.4) and its
-- self-referencing base_version_id FK — a data-migration risk that
-- outweighs the benefit, since referential integrity for source_id is
-- already enforced in the application layer (internal/source +
-- internal/registry stores only ever write a source_id that was just
-- created in skill_sources). The 0006 note anticipated wiring the FK
-- "when skill_sources lands"; on landing, the honest engineering call
-- is to keep these as nullable TEXT columns guarded by the store rather
-- than rebuild an immutable table. A dedicated rebuild migration can add
-- DB-level FKs later if a need ever materialises.
