-- 000015_algorithm_delete_set_null.up.sql
-- 纠正 000014 的一个错误决定：算法卸载**不得删除感知证据**。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- 背景
--   000014 把 recognitions / alarms 的 algorithm_id 设成 ON DELETE CASCADE，
--   理由是「算法包卸载后 label 集合与阈值语义随之消失，记录无法再被正确解释」。
--
--   这个判断是错的。证据（谁、何时、在哪台相机的画面里、框在哪、图是什么样）
--   是**已发生事实的记录**，它的价值不依赖于产生它的工具是否还安装着。
--   卸载一个算法不应该、也没有权力销毁三个月的历史告警。
--
--   项目里早就确立了正确范式并且已经落地在 persons 上：
--     person_id REFERENCES persons(id) ON DELETE SET NULL
--     + 冗余不可变快照字段 person_name / gallery_image_path
--   （见 database-guidelines §63）。算法引用本该照抄同一套，000014 没做到。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- 本次改动
--   1. algorithm_id 外键：ON DELETE CASCADE ➜ ON DELETE SET NULL
--      算法卸载后记录仍在，只是不再指向任何算法（NULL = 来源未标注，
--      与「宿主无法归属算法」共用同一语义，前端已按 "" 渲染）。
--
--   2. 新增冗余快照列 algorithm_name / algorithm_version（两表各一）。
--      理由与 person_name 完全相同：算法包卸载后，algorithms 行没了，
--      若只保留一个 NULL 的 id，运维永远无法回答「这批旧告警是哪个算法产生的」。
--      冗余名字让证据链在工具消失后依然自洽。
--      ⚠️ 冗余值必须**写入时快照**，不得在读取时 join 回 algorithms 表补全 ——
--      那正是 person_name 的做法，也是它能活过人员注销的原因。
--
--   3. captures 在 000014 里没有 algorithm_id 列（只修了 camera_id 的级联语义），
--      本次一并不动：加列属于 API 契约变更，应独立评估。
--
-- ⚠️ camera_id 仍是 ON DELETE CASCADE，这个**保持不变**且是对的：
--   相机删除后 image_path 指向的 snapshots/{camera_id}/ 整棵子树都已无主，
--   记录也无法再回答「画面从哪来」。这与「卸载工具不该毁证据」是两回事。
-- ═══════════════════════════════════════════════════════════════════════════

-- ── 1. 先给旧表补冗余列（这样下面 INSERT ... SELECT 能直接带上快照值）──────
-- SQLite 支持 ADD COLUMN，且默认值填充已有行，不需要重建表。
ALTER TABLE recognitions ADD COLUMN algorithm_name TEXT NOT NULL DEFAULT '';
ALTER TABLE recognitions ADD COLUMN algorithm_version TEXT NOT NULL DEFAULT '';
ALTER TABLE alarms ADD COLUMN algorithm_name TEXT NOT NULL DEFAULT '';
ALTER TABLE alarms ADD COLUMN algorithm_version TEXT NOT NULL DEFAULT '';

-- 用当前仍在册的算法回填历史行的快照值。已卸载算法产生的历史行只能留空 ——
-- 这正是 000014 之前就该避免的损失，无法追溯，但至少之后不会再产生。
UPDATE recognitions
SET algorithm_name = COALESCE((SELECT name FROM algorithms WHERE id = recognitions.algorithm_id), ''),
    algorithm_version = COALESCE((SELECT version FROM algorithms WHERE id = recognitions.algorithm_id), '')
WHERE algorithm_id IS NOT NULL;

UPDATE alarms
SET algorithm_name = COALESCE((SELECT name FROM algorithms WHERE id = alarms.algorithm_id), ''),
    algorithm_version = COALESCE((SELECT version FROM algorithms WHERE id = alarms.algorithm_id), '')
WHERE algorithm_id IS NOT NULL;

-- ── 2. 重建表，把外键从 CASCADE 改成 SET NULL ────────────────────────────
-- ⚠️ 不能写 `PRAGMA foreign_keys = OFF`：golang-migrate 在外层套了事务，
-- 该 pragma 在事务内是 no-op。因此这里靠「新表先建、旧表后删」的顺序保证
-- 任一时刻都不存在悬空引用。

CREATE TABLE recognitions_v15 (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    -- NULL = 来源未标注（算法已卸载 或 宿主无法归属）。
    algorithm_id TEXT REFERENCES algorithms(id) ON DELETE SET NULL,
    -- ⚠️ 写入时快照，不得读取时 join 回补。算法卸载后这是唯一的来源凭证。
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
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO recognitions_v15
SELECT id, camera_id, algorithm_id, algorithm_name, algorithm_version, pts, track_id,
       person_id, person_name, matched_face_id, matched, similarity, candidates_json,
       face_image_path, gallery_image_path, image_path, box_json, body_box_json,
       attributes_json, captured_at, created_at
FROM recognitions;
DROP TABLE recognitions;
ALTER TABLE recognitions_v15 RENAME TO recognitions;

CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured ON recognitions(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_person ON recognitions(person_id);
CREATE INDEX IF NOT EXISTS idx_recognitions_captured_at ON recognitions(captured_at DESC);

CREATE TABLE alarms_v15 (
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
INSERT INTO alarms_v15
SELECT id, camera_id, algorithm_id, algorithm_name, algorithm_version, alarm_type, score,
       box_json, image_path, crop_path, extra_json, pts, captured_at, created_at
FROM alarms;
DROP TABLE alarms;
ALTER TABLE alarms_v15 RENAME TO alarms;

CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured ON alarms(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_type ON alarms(alarm_type);
CREATE INDEX IF NOT EXISTS idx_alarms_captured_at ON alarms(captured_at DESC);
