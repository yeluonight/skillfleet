-- SkillFleet discovered_skills.modified_at (mgmt-refactor track C).
--
-- Records the newest file mtime in each discovered skill directory, so the
-- WebUI can show "when was this skill last edited on the device" — a time
-- dimension drift never had (it compares content_sha256, deliberately
-- ignoring mtime). Nullable: the agent reports it as 0/absent when unknown
-- (empty or unreadable directory), and older inventory rows predate it.
--
-- A plain ADD COLUMN suffices: discovered_skills has no CHECK to widen, and
-- the table is fully replaced on every inventory run, so existing rows that
-- get NULL here are transient — the next scan repopulates them.

ALTER TABLE discovered_skills ADD COLUMN modified_at INTEGER;
