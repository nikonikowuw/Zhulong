-- 000016_capture_algorithm_ref.up.sql
-- 给 captures 补齐算法来源归属，与 recognitions / alarms 的形态保持一致。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- 为什么需要
--   captures 记录的是「某个算法在某一帧里看到一个目标，这是它的图」。但在
--   000015 之前它连 algorithm_id 列都没有，于是运维面对一张抓拍时无法回答
--   「这是哪个算法产生的」。这个问题在排查「某算法误报率高」时是致命的：
--   你只能看到一堆抓拍，却无法按算法归因。
--
-- 为什么不在 000014 顺手加
--   加列是 API 契约变更（响应新增字段、前端 schema 需要同步），与 000014 的
--   「修外键」是两件事。混在一个迁移里会让回滚粒度变粗，也让 review 难以聚焦。
--   单独一个迁移，回滚时不会连带撤销外键修复。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- 形态（与 recognitions / alarms 完全同构，见 database-guidelines §63/§65）
--   algorithm_id      TEXT REFERENCES algorithms(id) ON DELETE SET NULL
--                     NULL = 来源未标注（算法已卸载，或宿主无法归属）
--   algorithm_name    TEXT NOT NULL DEFAULT ''   写入时快照
--   algorithm_version TEXT NOT NULL DEFAULT ''   写入时快照
--
--   ⚠️ 三者缺一不可。只加 id 而不加快照，卸载算法后「谁产生的」就永久丢失；
--   只加快照而不加 id，则无法按算法筛选与统计。这与 person_name /
--   person_id 的配对逻辑完全相同。
--
-- ⚠️ 为什么不重建表：SQLite 的 ADD COLUMN 可以带外键子句，且 NOT NULL 有
--   默认值时可安全填充已有行。captures 是高频表，能避免一次全表重写就避免。

ALTER TABLE captures ADD COLUMN algorithm_id TEXT REFERENCES algorithms(id) ON DELETE SET NULL;
ALTER TABLE captures ADD COLUMN algorithm_name TEXT NOT NULL DEFAULT '';
ALTER TABLE captures ADD COLUMN algorithm_version TEXT NOT NULL DEFAULT '';

-- 按算法归因的查询（「某算法产生了多少抓拍」）需要索引。
-- 已有索引覆盖的是 (camera_id, captured_at) / target_type / captured_at，
-- 没有覆盖 algorithm_id。
CREATE INDEX IF NOT EXISTS idx_captures_algorithm ON captures(algorithm_id);

-- 历史行无法追溯来源（当时没记录），保持 NULL / 空字符串。
-- 刻意不猜测：把算法猜错比留空危害更大 —— 空的语义是「未知」，错的语义是
-- 「确定但错误」，后者会让运维做出错误决策。
