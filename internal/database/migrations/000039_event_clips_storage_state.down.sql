-- 000039_event_clips_storage_state.down.sql
-- 回滚 000039 新增的存储后端与上传状态列及索引。

DROP INDEX IF EXISTS idx_captures_storage_captured;
ALTER TABLE captures DROP COLUMN storage_backend;

DROP INDEX IF EXISTS idx_recognitions_storage_captured;
ALTER TABLE recognitions DROP COLUMN storage_backend;

ALTER TABLE canvas_maps DROP COLUMN storage_backend;
ALTER TABLE person_faces DROP COLUMN storage_backend;
ALTER TABLE alarms DROP COLUMN storage_backend;

DROP INDEX IF EXISTS idx_event_clips_upload_status;
ALTER TABLE event_clips DROP COLUMN upload_status;
ALTER TABLE event_clips DROP COLUMN storage_backend;
