-- 优化改造 §3.4：为 discovered_skills.name 加索引。
-- fleet-status (GET /api/skills/{name}/fleet-status) 以 name 过滤跨设备
-- 查询；此前无 name 索引（仅有 device/run/tool 复合索引），多设备场景
-- 下走全表扫描。name 列查询频率随 fleet-status 上线而上升，加单列索引。
CREATE INDEX IF NOT EXISTS idx_discovered_skills_name ON discovered_skills(name);
