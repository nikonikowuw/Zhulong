CREATE TABLE IF NOT EXISTS algorithms (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL,
    task_type TEXT NOT NULL,
    category TEXT NOT NULL,
    target_arch TEXT NOT NULL,
    target_device TEXT NOT NULL,
    compute_cost INTEGER NOT NULL DEFAULT 10,
    memory_mb INTEGER NOT NULL DEFAULT 64,
    description TEXT NOT NULL DEFAULT '',
    manifest_json TEXT NOT NULL DEFAULT '{}',
    config_schema_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'DISABLED',
    previous_version TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_algorithms_status ON algorithms(status);
CREATE INDEX IF NOT EXISTS idx_algorithms_category ON algorithms(category);

CREATE TABLE IF NOT EXISTS analysis_tasks (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL,
    algorithm_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    sample_fps INTEGER NOT NULL DEFAULT 5,
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(camera_id, algorithm_id)
);

CREATE INDEX IF NOT EXISTS idx_analysis_tasks_camera_id ON analysis_tasks(camera_id);
CREATE INDEX IF NOT EXISTS idx_analysis_tasks_algorithm_id ON analysis_tasks(algorithm_id);
