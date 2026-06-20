-- SkillFleet skills master table + readable version sequence
-- (mgmt-refactor track B).
--
-- Two related changes, applied together because they both reshape how a
-- skill's identity and versions are addressed:
--
-- 1. A `skills` master table. Until now a skill was purely an implicit
--    GROUP BY name over skill_versions (no skills row anywhere). To support
--    a user-chosen "current/main version" we need a per-skill home for
--    current_version_id. The table is backfilled from the distinct names
--    already in skill_versions; current_version_id starts NULL, which the
--    code reads as "fall back to the newest version (MAX(created_at))" — so
--    existing behaviour (deploy/fork target the latest) is preserved exactly.
--
-- 2. A `version_seq` column on skill_versions: a per-skill auto-incrementing
--    integer (v1, v2, v3 …) for a readable version label, separate from the
--    user-facing free-text version_label. Backfilled by created_at order
--    within each name. New publishes compute the next seq inside the publish
--    transaction (registry.PublishFromDir), which becomes transactional in
--    this track to make the count+insert race-free.

CREATE TABLE skills (
    name               TEXT PRIMARY KEY,
    current_version_id TEXT,           -- NULL = use newest version (MAX created_at)
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

-- Backfill one skills row per distinct name. created_at/updated_at take the
-- name's oldest/newest version timestamps so the row reflects real history.
INSERT INTO skills (name, current_version_id, created_at, updated_at)
SELECT name, NULL, MIN(created_at), MAX(created_at)
FROM skill_versions
GROUP BY name;

-- Per-skill readable sequence number. Nullable: legacy rows are backfilled
-- below; rows inserted before this column existed but after a future code
-- path forgets to set it would simply have NULL (rendered as the raw id).
ALTER TABLE skill_versions ADD COLUMN version_seq INTEGER;

-- Backfill version_seq: number each name's versions 1..N by created_at
-- ascending (oldest = v1). The correlated subquery counts how many versions
-- of the same name are at or before this row's timestamp; ties broken by id
-- to keep it deterministic.
UPDATE skill_versions AS v
SET version_seq = (
    SELECT COUNT(*)
    FROM skill_versions AS w
    WHERE w.name = v.name
      AND (w.created_at < v.created_at
           OR (w.created_at = v.created_at AND w.id <= v.id))
);
