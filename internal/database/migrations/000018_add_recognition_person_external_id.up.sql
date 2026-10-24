-- recognitions.person_id 是弱外键（REFERENCES persons(id) ON DELETE SET NULL），
-- 承载的只能是内部代理主键。但上报给外部业务系统的身份是 persons.person_id
-- （对外业务键），且底库 IPC 载荷按契约下发的也是业务键原值。
--
-- 两者混用的后果（实测）：业务键与内部主键不同的那一刻起，每次人脸命中都会把
-- 业务键写进外键列 → FOREIGN KEY constraint failed → CreateRecognitionBatch 整批
-- 失败，而 record.Service 只记日志即 return nil，表现为「识别记录静默消失」。
--
-- 因此按 database-guidelines 的「弱外键 + 快照冗余」规矩，额外落一列写入时快照：
--   person_id           → 内部行 ID，负责库内关系、去重与 ON DELETE SET NULL
--   person_external_id  → 对外业务键快照，负责跨系统上报对账
-- 两列成对写入，缺一不可（只有 id 无法上报，只有快照无法筛选关联）。
ALTER TABLE recognitions ADD COLUMN person_external_id TEXT NOT NULL DEFAULT '';

-- 历史行一次性回填。业务键一旦赋值即不可改写（PERSON_ID_IMMUTABLE），
-- 所以这里补出来的值与「当年写入时快照」等价，不属于改写历史证据。
-- 未绑定业务键的人员保持空串：宁可标注「不可上报」，也不回退成内部主键。
UPDATE recognitions
   SET person_external_id = COALESCE(
         (SELECT p.person_id FROM persons p WHERE p.id = recognitions.person_id), '')
 WHERE person_external_id = '';

-- 上报侧的增量游标查询：按业务键取某人的识别记录，按抓拍时间倒序。
CREATE INDEX IF NOT EXISTS idx_recognitions_person_external
    ON recognitions(person_external_id, captured_at DESC)
    WHERE person_external_id <> '';
