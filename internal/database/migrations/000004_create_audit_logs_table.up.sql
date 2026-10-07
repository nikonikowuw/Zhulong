CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ip TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT 'admin',
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'success',
    error_msg TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs (action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_status ON audit_logs (status);
