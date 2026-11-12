-- 000035_add_alert_destination_capabilities.up.sql
ALTER TABLE alert_destinations
    ADD COLUMN events_enabled BOOLEAN NOT NULL DEFAULT 1;

ALTER TABLE alert_destinations
    ADD COLUMN heartbeat_enabled BOOLEAN NOT NULL DEFAULT 0;
