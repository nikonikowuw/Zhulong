-- 000035_add_alert_destination_capabilities.down.sql
ALTER TABLE alert_destinations
    DROP COLUMN heartbeat_enabled;

ALTER TABLE alert_destinations
    DROP COLUMN events_enabled;
