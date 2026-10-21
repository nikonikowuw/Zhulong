-- 000015_algorithm_delete_set_null.down.sql
-- 回滚到 000014 的形态：algorithm_id 恢复 ON DELETE CASCADE，去掉冗余快照列。

-- ── recognitions ──────────────────────────────────────────────────────────
CREATE TABLE recognitions_v14 (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    algorithm_id TEXT REFERENCES algorithms(id) ON DELETE CASCADE,
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
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- ⚠️ algorithm_name / algorithm_version 在此回滚中丢失。它们是唯一能在算法
-- 卸载后回答「这批告警是谁产生的」的信息，因此这次降级是**有损**的。
INSERT INTO recognitions_v14
SELECT id, camera_id, algorithm_id, pts, track_id, person_id, person_name,
       matched_face_id, matched, similarity, candidates_json, face_image_path,
       gallery_image_path, image_path, box_json, body_box_json, attributes_json,
       captured_at, created_at
FROM recognitions;
DROP TABLE recognitions;
ALTER TABLE recognitions_v14 RENAME TO recognitions;

CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured ON recognitions(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_person ON recognitions(person_id);
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at ON recognitions(captured_at DESC);

-- ── alarms ────────────────────────────────────────────────────────────────
CREATE TABLE alarms_v14 (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    algorithm_id TEXT REFERENCES algorithms(id) ON DELETE CASCADE,
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
INSERT INTO alarms_v14
SELECT id, camera_id, algorithm_id, alarm_type, score, box_json, image_path,
       crop_path, extra_json, pts, captured_at, created_at
FROM alarms;
DROP TABLE alarms;
ALTER TABLE alarms_v14 RENAME TO alarms;

CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured ON alarms(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_type ON alarms(alarm_type);
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at ON alarms(captured_at DESC);
