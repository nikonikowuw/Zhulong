CREATE TABLE IF NOT EXISTS system_async_jobs (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    progress INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    processed INTEGER NOT NULL DEFAULT 0,
    succeeded INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    current_stage TEXT NOT NULL DEFAULT '',
    current_file TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    error_message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_system_async_jobs_type ON system_async_jobs(type);
CREATE INDEX IF NOT EXISTS idx_system_async_jobs_status ON system_async_jobs(status);
CREATE INDEX IF NOT EXISTS idx_system_async_jobs_created_at ON system_async_jobs(created_at);
