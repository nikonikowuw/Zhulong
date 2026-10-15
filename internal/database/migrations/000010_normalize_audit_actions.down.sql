-- Rollback time audit action names to legacy system.time.* format
UPDATE audit_logs SET action = 'system.time.update_config' WHERE action = 'time.update_config';
UPDATE audit_logs SET action = 'system.time.ntp_sync' WHERE action = 'time.ntp_sync';
UPDATE audit_logs SET action = 'system.time.manual_set' WHERE action = 'time.manual_set';
UPDATE audit_logs SET action = 'system.time.rtc_heal' WHERE action = 'time.rtc_heal';

-- Rollback storage audit action names to legacy system.storage.* format
UPDATE audit_logs SET action = 'system.storage.update_config' WHERE action = 'storage.update_config';
UPDATE audit_logs SET action = 'system.storage.manual_cleanup' WHERE action = 'storage.manual_cleanup';
UPDATE audit_logs SET action = 'system.storage.test_path' WHERE action = 'storage.test_path';
UPDATE audit_logs SET action = 'system.storage.emergency_stop' WHERE action = 'storage.emergency_stop';
UPDATE audit_logs SET action = 'system.storage.emergency_resume' WHERE action = 'storage.emergency_resume';
