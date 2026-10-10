CREATE TABLE IF NOT EXISTS system_time_configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    mode TEXT NOT NULL DEFAULT 'ntp',
    ntp_servers TEXT NOT NULL DEFAULT '["ntp.aliyun.com","cn.pool.ntp.org","pool.ntp.org"]',
    sync_interval_seconds INTEGER NOT NULL DEFAULT 900,
    timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO system_time_configs (id, mode, ntp_servers, sync_interval_seconds, timezone, updated_at)
VALUES (1, 'ntp', '["ntp.aliyun.com","cn.pool.ntp.org","pool.ntp.org"]', 900, 'Asia/Shanghai', CURRENT_TIMESTAMP);
