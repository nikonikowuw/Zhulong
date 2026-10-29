-- 分页查询固定使用 ORDER BY captured_at DESC, id DESC（见 internal/record/store.go）。
--
-- 单列 captured_at 索引只能满足第一个排序键，SQLite 对每一页都要
-- "USE TEMP B-TREE FOR LAST TERM OF ORDER BY"；带 camera_id 过滤时更退化成
-- 全表扫描 + 全量排序。把 id 补进复合索引后，整个窗口能直接从索引顺序取出。
--
-- 旧索引被新索引完全覆盖（同样能服务保留清理的 captured_at < ? 游标与
-- count 查询），继续保留只会让每条写入多维护一棵 B-tree。

DROP INDEX IF EXISTS idx_recognitions_captured_at;
DROP INDEX IF EXISTS idx_recognitions_camera_captured;
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at_id
    ON recognitions(captured_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured_id
    ON recognitions(camera_id, captured_at DESC, id DESC);

DROP INDEX IF EXISTS idx_alarms_captured_at;
DROP INDEX IF EXISTS idx_alarms_camera_captured;
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at_id
    ON alarms(captured_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured_id
    ON alarms(camera_id, captured_at DESC, id DESC);

DROP INDEX IF EXISTS idx_captures_captured_at;
DROP INDEX IF EXISTS idx_captures_camera_captured;
CREATE INDEX IF NOT EXISTS idx_captures_captured_at_id
    ON captures(captured_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_captures_camera_captured_id
    ON captures(camera_id, captured_at DESC, id DESC);
