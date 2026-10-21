-- 000014_perception_foreign_keys.up.sql
-- 修复感知事件表（recognitions / alarms / captures）的引用完整性问题。
-- 三处缺陷都只能靠重建表解决：SQLite 无法 ALTER 外键，也无法放宽 NOT NULL。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- 缺陷 1：algorithm_id 为空串时整条记录写不进去
--   000011 把 algorithm_id 声明为 NOT NULL REFERENCES algorithms(id)。但宿主拿到
--   推理结果时并不总能确定算法归属（Hub 在历史上曾固定传空串，见
--   camera/stream_hub.go 的注释），于是落库触发 FOREIGN KEY constraint failed。
--   而 record.Service 只 logger.Error 后 return nil —— 结果是「静默丢记录」。
--
--   修复：null 表示「来源未标注」；非空值仍必须是真实算法。
--   ⚠️ 不能用空串代替 null：SQLite 的外键检查对空串一视同仁地做「必须在父表存在」，
--   空串依然会被拒绝。只有 NULL 才真正跳过外键校验（SQL 标准语义）。
--   特意不引入占位 algorithms 行：那会污染算法列表、统计与卸载语义。
--
-- 缺陷 2：有历史记录的相机删不掉
--   camera_id 是裸 REFERENCES cameras(id)（默认 NO ACTION），且 camera/store.go 的
--   Delete 只手动删了 camera_streams。于是 DELETE FROM cameras 直接撞外键失败 ——
--   实测确认：只要有任何一条感知记录，相机就永久无法删除。
--
--   修复：ON DELETE CASCADE。理由不是「省事」：image_path 指向
--   snapshots/{camera_id}/ 下的文件，相机一删这些文件即成孤儿，记录也不再能解释
--   「画面从哪来」。硬性约定是「证据属于某台相机」。
--   ⚠️ 与之对照的是 person_id 的 ON DELETE SET NULL：人员注销后记录仍有证据价值
--   （快照冗余了 person_name / gallery_image_path，见 database-guidelines）。
--   两者是不同的语义，不要统一。
--
-- 缺陷 3：有历史记录的算法删不掉
--   algorithm_id 同为裸引用。修复：ON DELETE CASCADE —— 算法包卸载后，其产生的
--   记录无法再被正确解释（label 集合、阈值语义随包消失）。
--   ⚠️ 这是有损操作：卸载算法会连带删除其历史证据。运维流程必须在卸载前先导出。
-- ═══════════════════════════════════════════════════════════════════════════

-- ⚠️ 不能写 `PRAGMA foreign_keys = OFF`：golang-migrate 在本文件外层套了事务，
-- 而 SQLite 的 foreign_keys pragma 在事务内是 no-op。因此重建时必须让「DROP 旧表
-- → 改名」这个窗口本身合法 —— 做法是先清掉非法引用，再重建，且重建后立刻补数据。

-- ── 0. 数据清洗：删除引用已不存在的行 ─────────────────────────────────────
-- 这些行只可能来自历史 bug。必须在重建前删掉，否则新表的 INSERT ... SELECT
-- 会被外键拒绝，整个迁移失败。
-- 空串的 algorithm_id 是历史 bug 的产物（Hub 曾固定传空串）。迁移到 NULL 表达
-- 「未标注」，否则新 schema 会用外键把同一批行拒之门外。
UPDATE recognitions SET algorithm_id = NULL WHERE algorithm_id = '';
UPDATE alarms SET algorithm_id = NULL WHERE algorithm_id = '';

DELETE FROM recognitions
WHERE camera_id NOT IN (SELECT id FROM cameras)
   OR (algorithm_id IS NOT NULL AND algorithm_id NOT IN (SELECT id FROM algorithms));

DELETE FROM alarms
WHERE camera_id NOT IN (SELECT id FROM cameras)
   OR (algorithm_id IS NOT NULL AND algorithm_id NOT IN (SELECT id FROM algorithms));

DELETE FROM captures
WHERE camera_id NOT IN (SELECT id FROM cameras);

-- ── 1. recognitions ───────────────────────────────────────────────────────
CREATE TABLE recognitions_new (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    -- NULL = 来源未标注；非空必须是真实算法（NULL 才跳过外键校验，空串不能）。
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
INSERT INTO recognitions_new SELECT * FROM recognitions;
DROP TABLE recognitions;
ALTER TABLE recognitions_new RENAME TO recognitions;

CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured ON recognitions(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_person ON recognitions(person_id);
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at ON recognitions(captured_at DESC);

-- ── 2. alarms ─────────────────────────────────────────────────────────────
CREATE TABLE alarms_new (
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
INSERT INTO alarms_new SELECT * FROM alarms;
DROP TABLE alarms;
ALTER TABLE alarms_new RENAME TO alarms;

CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured ON alarms(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_type ON alarms(alarm_type);
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at ON alarms(captured_at DESC);

-- ── 3. captures ───────────────────────────────────────────────────────────
-- captures 本身没有 algorithm_id 列，本次只修 camera_id 的级联语义，
-- 不顺手加列（那会连带影响 API 契约与前端 schema，属于独立变更）。
CREATE TABLE captures_new (
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
INSERT INTO captures_new SELECT * FROM captures;
DROP TABLE captures;
ALTER TABLE captures_new RENAME TO captures;

CREATE INDEX IF NOT EXISTS idx_captures_camera_captured ON captures(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_captures_target_type ON captures(target_type);
CREATE INDEX IF NOT EXISTS idx_captures_captured_at ON captures(captured_at DESC);
