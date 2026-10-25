-- 000019_purge_empty_analysis_tasks.up.sql
-- 清理没有任何算法子项的「空聚合根」分析任务行。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- 为什么需要
--   analysis_tasks 是通道聚合根，它的语义全部由 task_algorithms 子项承载。
--   但 SaveTaskWithinBudget 的 syncTaskAlgorithms 是「先按 keepIDs 剪掉不在列表里的
--   子项，再 upsert 剩下的」——当调用方提交 algorithms: [] 时，剪枝会把子项删干净，
--   主表行却原样留着。于是库里出现了 enabled=true、algorithms=[] 的行。
--
--   这种行的危害不是「多一条记录」：
--     · 它不产生明细行、不占算力点，看板显示 0/1000；
--     · 前端按「任务行存在」判定时，会在明明没有任何任务的页面上渲染出算力看板与
--       一张空的通道卡片（用户视角就是「没任务却还有卡片」）；
--     · PUT 再次提交时它又会被 upsert 命中，脏数据永远不消失。
--
-- 为什么可以安全删除
--   主表行没有独立的用户可见信息：name 在空聚合根场景从未被赋过有意义的值
--  （前端弹窗只在有勾选算法时才提交），enabled/时间戳都属于已不存在的配置。
--   子表已有 ON DELETE CASCADE，因此这里不涉及外键残留。
--
-- 后续
--   Service 层已收敛为「algorithms 为空即 422 TASK_ALGORITHM_REQUIRED」，
--   清空通道必须走 DELETE /api/v1/tasks/:id。此迁移只负责把存量扫干净。
--
-- ⚠️ 一次性：本迁移不幂等地保留任何「以后不要删」的豁免名单。若现场确实有人
--   依赖空行做占位，请在应用层显式建模，而不是靠脏数据。

DELETE FROM analysis_tasks
 WHERE NOT EXISTS (
   SELECT 1 FROM task_algorithms WHERE task_algorithms.task_id = analysis_tasks.id
 );
