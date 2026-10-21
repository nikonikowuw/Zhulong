-- 000016_capture_algorithm_ref.down.sql
-- 回滚 captures 的算法来源归属列。
--
-- ⚠️ 有损：algorithm_name / algorithm_version 是算法卸载后唯一能回答「这批抓拍
-- 是谁产生的」的信息，删除后无法恢复。仅在确需降级时接受。
--
-- ⚠️ 为什么重建表而不是 ALTER TABLE DROP COLUMN：
--   SQLite（3.46 实测）拒绝删除带外键约束的列，报
--   「cannot drop column "algorithm_id": is a foreign key」。因此必须重建。
--   直接写 DROP COLUMN 会让 down 迁移在真实环境里失败，而 down 失败意味着
--   数据库停在半降级状态 —— 比不支持降级严重得多。

CREATE TABLE captures_v15 (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL,
    track_id INTEGER NOT NULL DEFAULT 0,
    score REAL NOT NULL DEFAULT 0.0,
    box_json TEXT NOT NULL DEFAULT '[]',
    crop_path TEXT NOT NULL DEFAULT '',
    image_path TEXT NOT NULL DEFAULT '',
    attributes_json TEXT NOT NULL DEFAULT '{}',
    pts INTEGER NOT NULL,
    captured_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO captures_v15
SELECT id, camera_id, target_type, track_id, score, box_json, crop_path,
       image_path, attributes_json, pts, captured_at, created_at
FROM captures;
DROP TABLE captures;
ALTER TABLE captures_v15 RENAME TO captures;

CREATE INDEX IF NOT EXISTS idx_captures_camera_captured ON captures(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_captures_target_type ON captures(target_type);
CREATE INDEX IF NOT EXISTS idx_captures_captured_at ON captures(captured_at DESC);
