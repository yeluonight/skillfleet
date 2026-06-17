-- SkillFleet registry tables (phase 4 t4).
--
-- Scope:
--   * skill_versions     -- immutable, content-addressed package versions
--   * skill_drafts       -- editable working copies (Draft model, v1.0 §7.3)
--   * skill_draft_files  -- per-file contents of an open draft
--
-- Conventions inherited from 0002 / 0004 / 0005:
--   * Millisecond unix timestamps as INTEGER, suffix "_at".
--   * Application-assigned text IDs (no AUTOINCREMENT).
--   * STRICT so column-affinity typos fail at INSERT, not at read.
--
-- Immutability (v1.0 §1.3.4): a skill_versions row is never updated or
-- deleted in the normal flow. Editing always forks a draft, and
-- publishing a draft INSERTs a new version. content_sha256 is the
-- package tree fingerprint (internal/skill.Manifest.ContentSHA256) and
-- is the natural dedup key — two versions with identical content share
-- one package file on disk (ADR-0008).
--
-- Note on absent FKs: source_id references skill_sources, which is a
-- Phase 6 table not yet created. It is kept as a nullable TEXT column
-- (no REFERENCES) so this migration stands alone; the FK is added when
-- skill_sources lands. base_version_id is a self-reference to
-- skill_versions and IS wired, with ON DELETE SET NULL so a (rare,
-- admin-forced) version deletion doesn't orphan a child row's FK.

-- skill_versions: one immutable package version. package_path is the
-- relative path under the server store (e.g. "packages/<sha>.tgz");
-- the store root lives in config, not the DB. version_kind records how
-- the version came to exist; the CHECK set covers the kinds grounded
-- in v1.0 (§7 publish, §6 import, §8 local_edit / upstream / merged).
CREATE TABLE skill_versions (
    id             TEXT PRIMARY KEY,
    source_id      TEXT,
    name           TEXT NOT NULL,
    version_label  TEXT,
    version_kind   TEXT NOT NULL CHECK (version_kind IN
                       ('manual', 'import', 'draft_publish', 'upstream', 'local_edit', 'merged')),
    source_commit  TEXT,
    source_tag     TEXT,
    source_release TEXT,
    base_version_id TEXT REFERENCES skill_versions(id) ON DELETE SET NULL,
    content_sha256 TEXT NOT NULL,
    manifest_json  TEXT NOT NULL,
    package_path   TEXT NOT NULL,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_skill_versions_name    ON skill_versions(name, created_at);
CREATE INDEX idx_skill_versions_source  ON skill_versions(source_id);
CREATE INDEX idx_skill_versions_content ON skill_versions(content_sha256);

-- skill_drafts: a mutable working copy. status drives the lifecycle
-- open -> published | discarded (v1.0 §7.3). base_version_id is the
-- version the draft forked from (NULL for a brand-new skill). title is
-- an operator-facing label distinct from the skill name.
CREATE TABLE skill_drafts (
    id              TEXT PRIMARY KEY,
    source_id       TEXT,
    base_version_id TEXT REFERENCES skill_versions(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    title           TEXT,
    status          TEXT NOT NULL CHECK (status IN ('open', 'published', 'discarded')),
    created_by      TEXT,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_skill_drafts_status ON skill_drafts(status, updated_at);
CREATE INDEX idx_skill_drafts_base   ON skill_drafts(base_version_id);

-- skill_draft_files: one row per file in an open draft. Small text
-- files are stored inline in content_text (cheap to read/edit in the
-- WebUI); binary or oversized files store their bytes on disk and keep
-- the relative blob path in content_path. Exactly one of
-- (content_text, content_path) is meaningful per row; is_binary picks
-- which. path is package-relative and MUST have passed safefs
-- (validated in the application layer, not the DB). validation_json
-- caches the last validate() result for the file (Phase 5 panel).
CREATE TABLE skill_draft_files (
    id              TEXT PRIMARY KEY,
    draft_id        TEXT NOT NULL REFERENCES skill_drafts(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    content_path    TEXT,
    content_text    TEXT,
    encoding        TEXT,
    is_binary       INTEGER NOT NULL DEFAULT 0 CHECK (is_binary IN (0, 1)),
    size            INTEGER NOT NULL DEFAULT 0,
    sha256          TEXT,
    validation_json TEXT,
    updated_at      INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_skill_draft_files_draft ON skill_draft_files(draft_id);
-- A draft can't hold the same package path twice.
CREATE UNIQUE INDEX idx_skill_draft_files_draft_path ON skill_draft_files(draft_id, path);
