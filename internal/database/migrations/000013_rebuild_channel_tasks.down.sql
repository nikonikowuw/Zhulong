-- 000013_rebuild_channel_tasks.down.sql
-- 回滚通道多算法模型，还原为 (camera_id, algorithm_id) 扁平结构

-- 1. 创建旧版 analysis_tasks_old 结构
CREATE TABLE IF NOT EXISTS analysis_tasks_old (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL,
    algorithm_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    sample_fps INTEGER NOT NULL DEFAULT 5,
    config_json TEXT NOT NULL DEFAULT '{}',
    region_config_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(camera_id, algorithm_id)
);

-- 2. 将主子表平铺还原
-- enabled 由通道主开关与算法子开关合取计算：(t.enabled AND ta.enabled)
INSERT INTO analysis_tasks_old (id, camera_id, algorithm_id, enabled, sample_fps, config_json, region_config_json, created_at, updated_at)
SELECT 
    ta.id,
    t.camera_id,
    ta.algorithm_id,
    (t.enabled AND ta.enabled),
    ta.sample_fps,
    ta.config_json,
    ta.region_config_json,
    ta.created_at,
    ta.updated_at
FROM task_algorithms ta
JOIN analysis_tasks t ON t.id = ta.task_id;

-- 3. 删除新结构表
DROP TABLE task_algorithms;
DROP TABLE analysis_tasks;

-- 4. 恢复旧表并重建索引
ALTER TABLE analysis_tasks_old RENAME TO analysis_tasks;
CREATE INDEX IF NOT EXISTS idx_analysis_tasks_camera_id ON analysis_tasks(camera_id);
CREATE INDEX IF NOT EXISTS idx_analysis_tasks_algorithm_id ON analysis_tasks(algorithm_id);
