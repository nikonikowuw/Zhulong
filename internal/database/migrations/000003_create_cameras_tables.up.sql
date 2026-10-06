CREATE TABLE cameras (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    revision INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE camera_streams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'rtsp',
    encrypted_uri BLOB NOT NULL,
    transport TEXT NOT NULL DEFAULT 'tcp',
    codec TEXT NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    fps_numerator INTEGER NOT NULL DEFAULT 0,
    fps_denominator INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(camera_id, role)
);

CREATE INDEX idx_camera_streams_camera_id ON camera_streams(camera_id);
