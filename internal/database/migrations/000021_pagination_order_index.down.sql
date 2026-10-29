-- 回退到 000011/000014 定义的单列 captured_at 索引形态。

DROP INDEX IF EXISTS idx_recognitions_captured_at_id;
DROP INDEX IF EXISTS idx_recognitions_camera_captured_id;
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at ON recognitions(captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured ON recognitions(camera_id, captured_at DESC);

DROP INDEX IF EXISTS idx_alarms_captured_at_id;
DROP INDEX IF EXISTS idx_alarms_camera_captured_id;
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at ON alarms(captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured ON alarms(camera_id, captured_at DESC);

DROP INDEX IF EXISTS idx_captures_captured_at_id;
DROP INDEX IF EXISTS idx_captures_camera_captured_id;
CREATE INDEX IF NOT EXISTS idx_captures_captured_at ON captures(captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_captures_camera_captured ON captures(camera_id, captured_at DESC);
