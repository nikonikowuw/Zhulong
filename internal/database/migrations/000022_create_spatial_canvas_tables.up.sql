-- 电子地图与画布点位管理表
CREATE TABLE IF NOT EXISTS canvas_maps (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    image_path TEXT NOT NULL,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS camera_canvas_positions (
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    map_id TEXT NOT NULL REFERENCES canvas_maps(id) ON DELETE CASCADE,
    x REAL NOT NULL CHECK (x >= 0.0 AND x <= 1.0),
    y REAL NOT NULL CHECK (y >= 0.0 AND y <= 1.0),
    angle REAL NOT NULL DEFAULT 0.0 CHECK (angle >= 0.0 AND angle <= 360.0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (camera_id, map_id)
);

CREATE INDEX IF NOT EXISTS idx_camera_positions_map ON camera_canvas_positions(map_id);
