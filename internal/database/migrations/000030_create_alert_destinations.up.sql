CREATE TABLE IF NOT EXISTS alert_destinations (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    channel_type TEXT NOT NULL CHECK (channel_type IN ('webhook', 'mqtt')),
    enabled BOOLEAN NOT NULL DEFAULT 1,
    image_mode TEXT NOT NULL DEFAULT 'url' CHECK (image_mode IN ('url', 'multipart', 'base64')),
    mqtt_client_id TEXT,
    config_json TEXT NOT NULL DEFAULT '{}',
    secret_ciphertext BLOB,
    filter_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_alert_destinations_name ON alert_destinations(name);
CREATE UNIQUE INDEX IF NOT EXISTS ux_alert_destinations_mqtt_client_id
    ON alert_destinations(mqtt_client_id)
    WHERE channel_type = 'mqtt' AND mqtt_client_id IS NOT NULL;
