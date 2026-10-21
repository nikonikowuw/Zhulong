-- 000014_perception_foreign_keys.down.sql
-- 回滚到 000011 的外键形态（裸引用 + algorithm_id 强制非空）。

-- ── recognitions ──────────────────────────────────────────────────────────
CREATE TABLE recognitions_old (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id),
    algorithm_id TEXT NOT NULL REFERENCES algorithms(id),
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
-- ⚠️ 回滚会丢失 algorithm_id 为 NULL 的行：旧 schema 的 NOT NULL + REFERENCES
-- 组合根本容不下它们。这是不可逆的数据损失，仅在确需降级时接受。
INSERT INTO recognitions_old
SELECT * FROM recognitions WHERE algorithm_id IS NOT NULL;
DROP TABLE recognitions;
ALTER TABLE recognitions_old RENAME TO recognitions;

CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured ON recognitions(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_person ON recognitions(person_id);
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at ON recognitions(captured_at DESC);

-- ── alarms ────────────────────────────────────────────────────────────────
CREATE TABLE alarms_old (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id),
    algorithm_id TEXT NOT NULL REFERENCES algorithms(id),
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
INSERT INTO alarms_old
SELECT * FROM alarms WHERE algorithm_id IS NOT NULL;
DROP TABLE alarms;
ALTER TABLE alarms_old RENAME TO alarms;

CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured ON alarms(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_type ON alarms(alarm_type);
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at ON alarms(captured_at DESC);

-- ── captures ──────────────────────────────────────────────────────────────
CREATE TABLE captures_old (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id),
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
INSERT INTO captures_old SELECT * FROM captures;
DROP TABLE captures;
ALTER TABLE captures_old RENAME TO captures;

CREATE INDEX IF NOT EXISTS idx_captures_camera_captured ON captures(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_captures_target_type ON captures(target_type);
CREATE INDEX IF NOT EXISTS idx_captures_captured_at ON captures(captured_at DESC);
