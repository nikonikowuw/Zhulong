-- Normalize systemtime audit action names to standard domain format
UPDATE audit_logs SET action = 'time.update_config' WHERE action = 'system.time.update_config';
UPDATE audit_logs SET action = 'time.ntp_sync' WHERE action = 'system.time.ntp_sync';
UPDATE audit_logs SET action = 'time.manual_set' WHERE action = 'system.time.manual_set';
UPDATE audit_logs SET action = 'time.rtc_heal' WHERE action = 'system.time.rtc_heal';

-- Normalize storage audit action names to standard domain format
UPDATE audit_logs SET action = 'storage.update_config' WHERE action = 'system.storage.update_config';
UPDATE audit_logs SET action = 'storage.manual_cleanup' WHERE action = 'system.storage.manual_cleanup';
UPDATE audit_logs SET action = 'storage.test_path' WHERE action = 'system.storage.test_path' OR action = 'system.storage.path_test';
UPDATE audit_logs SET action = 'storage.emergency_stop' WHERE action = 'system.storage.emergency_stop';
UPDATE audit_logs SET action = 'storage.emergency_resume' WHERE action = 'system.storage.emergency_resume';
