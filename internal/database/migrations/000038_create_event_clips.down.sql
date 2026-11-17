-- 000038_create_event_clips.down.sql
-- 回滚事件录像片段。
--
-- ⚠️ 有损：片段与事件的关联（clip_id / clip_offset_ms）以及 event_clips 行会丢失；
--   recordings/ 下的片段文件不会被删除（交给 storage 清理或人工处理）。
--
-- ⚠️ 为什么重建 alarms / recognitions 而不是 ALTER TABLE DROP COLUMN：
--   SQLite（3.46 实测）拒绝删除带外键约束的列（「cannot drop column: is a foreign key」），
--   见 000016 的 down。顺序：先重建子表去掉 clip_id，再删父表 event_clips。

DROP INDEX IF EXISTS idx_alarms_clip;
CREATE TABLE alarms_v37 (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    algorithm_id TEXT REFERENCES algorithms(id) ON DELETE SET NULL,
    algorithm_name TEXT NOT NULL DEFAULT '',
    algorithm_version TEXT NOT NULL DEFAULT '',
    alarm_type TEXT NOT NULL,
    score REAL NOT NULL DEFAULT 0.0,
    box_json TEXT NOT NULL DEFAULT '[]',
    image_path TEXT NOT NULL DEFAULT '',
    crop_path TEXT NOT NULL DEFAULT '',
    extra_json TEXT NOT NULL DEFAULT '{}',
    pts INTEGER NOT NULL,
    captured_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO alarms_v37
SELECT id, camera_id, algorithm_id, algorithm_name, algorithm_version, alarm_type, score,
       box_json, image_path, crop_path, extra_json, pts, captured_at, created_at
FROM alarms;
DROP TABLE alarms;
ALTER TABLE alarms_v37 RENAME TO alarms;

CREATE INDEX IF NOT EXISTS idx_alarms_type ON alarms(alarm_type);
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at_id ON alarms(captured_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured_id ON alarms(camera_id, captured_at DESC, id DESC);

DROP INDEX IF EXISTS idx_recognitions_clip;
CREATE TABLE recognitions_v37 (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    algorithm_id TEXT REFERENCES algorithms(id) ON DELETE SET NULL,
    algorithm_name TEXT NOT NULL DEFAULT '',
    algorithm_version TEXT NOT NULL DEFAULT '',
    pts INTEGER NOT NULL,
    track_id INTEGER NOT NULL DEFAULT 0,
    person_id TEXT REFERENCES persons(id) ON DELETE SET NULL,
    person_name TEXT NOT NULL DEFAULT '',
    matched_face_id TEXT NOT NULL DEFAULT '',
    matched BOOLEAN NOT NULL DEFAULT 0,
    similarity REAL NOT NULL DEFAULT 0.0,
    candidates_json TEXT NOT NULL DEFAULT '[]',
    face_image_path TEXT NOT NULL DEFAULT '',
    gallery_image_path TEXT NOT NULL DEFAULT '',
    image_path TEXT NOT NULL DEFAULT '',
    box_json TEXT NOT NULL DEFAULT '[]',
    body_box_json TEXT NOT NULL DEFAULT '[]',
    attributes_json TEXT NOT NULL DEFAULT '{}',
    captured_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    person_external_id TEXT NOT NULL DEFAULT '',
    feature_vector BLOB,
    landmarks_json TEXT NOT NULL DEFAULT '[]'
);
INSERT INTO recognitions_v37
SELECT id, camera_id, algorithm_id, algorithm_name, algorithm_version, pts, track_id,
       person_id, person_name, matched_face_id, matched, similarity, candidates_json,
       face_image_path, gallery_image_path, image_path, box_json, body_box_json,
       attributes_json, captured_at, created_at, person_external_id, feature_vector,
       landmarks_json
FROM recognitions;
DROP TABLE recognitions;
ALTER TABLE recognitions_v37 RENAME TO recognitions;

CREATE INDEX IF NOT EXISTS idx_recognitions_person ON recognitions(person_id);
CREATE INDEX IF NOT EXISTS idx_recognitions_person_external
    ON recognitions(person_external_id, captured_at DESC)
    WHERE person_external_id <> '';
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at_id ON recognitions(captured_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured_id
    ON recognitions(camera_id, captured_at DESC, id DESC);

DROP TABLE IF EXISTS event_clips;

ALTER TABLE task_algorithms DROP COLUMN clip_config_json;
