-- 000013_rebuild_channel_tasks.up.sql
-- 重构分析任务为通道多算法模型：analysis_tasks (主表 1:1 camera) + task_algorithms (子表 1:N algorithms)

-- 1. 备份旧表数据 (含 000012 引入的 region_config_json)
CREATE TABLE temp_old_tasks AS SELECT * FROM analysis_tasks;

-- 2. 创建子表 task_algorithms
CREATE TABLE IF NOT EXISTS task_algorithms (
    id TEXT PRIMARY KEY NOT NULL,
    task_id TEXT NOT NULL REFERENCES analysis_tasks(id) ON DELETE CASCADE,
    algorithm_id TEXT NOT NULL REFERENCES algorithms(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    sample_fps INTEGER NOT NULL DEFAULT 5,
    config_json TEXT NOT NULL DEFAULT '{}',
    region_config_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(task_id, algorithm_id)
);

-- 3. 创建临时新主表 analysis_tasks_new
CREATE TABLE IF NOT EXISTS analysis_tasks_new (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL UNIQUE,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    name TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 4. 从 temp_old_tasks 分组聚合插入新主表
-- 聚合根 id 复用该相机最早那条任务的既有 ULID (MIN(id))；enabled 取 MAX(enabled)
INSERT INTO analysis_tasks_new (id, camera_id, enabled, created_at, updated_at)
SELECT 
    MIN(id),
    camera_id,
    MAX(enabled),
    MIN(created_at),
    MAX(updated_at)
FROM temp_old_tasks
GROUP BY camera_id;

-- 5. 删除旧主表 (此时子表为空，不会触发 CASCADE)
DROP TABLE analysis_tasks;

-- 6. 将新主表重命名为 analysis_tasks (满足 task_algorithms 的外键约束)
ALTER TABLE analysis_tasks_new RENAME TO analysis_tasks;

-- 7. 从 temp_old_tasks 插入所有算法配置明细至 task_algorithms，含 region_config_json
INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps, config_json, region_config_json, created_at, updated_at)
SELECT 
    o.id,
    n.id,
    o.algorithm_id,
    o.enabled,
    o.sample_fps,
    o.config_json,
    o.region_config_json,
    o.created_at,
    o.updated_at
FROM temp_old_tasks o
JOIN analysis_tasks n ON n.camera_id = o.camera_id;

-- 8. 清理临时表
DROP TABLE temp_old_tasks;

-- 9. 建立索引
CREATE UNIQUE INDEX IF NOT EXISTS idx_analysis_tasks_camera_id ON analysis_tasks(camera_id);
CREATE INDEX IF NOT EXISTS idx_task_algorithms_task_id ON task_algorithms(task_id);
CREATE INDEX IF NOT EXISTS idx_task_algorithms_algorithm_id ON task_algorithms(algorithm_id);
