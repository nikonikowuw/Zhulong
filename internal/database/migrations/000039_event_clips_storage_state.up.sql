-- 000039_event_clips_storage_state.up.sql
-- 为多存储后端（local / s3）引入片段上传状态追踪与感知/底库/画布资产的存储后端标记。

ALTER TABLE event_clips ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'local';
ALTER TABLE event_clips ADD COLUMN upload_status TEXT NOT NULL DEFAULT 'local'
    CHECK (upload_status IN ('local', 'pending', 'uploaded', 'failed'));
CREATE INDEX IF NOT EXISTS idx_event_clips_upload_status ON event_clips(upload_status);

ALTER TABLE recognitions ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'local';
CREATE INDEX IF NOT EXISTS idx_recognitions_storage_captured
    ON recognitions(storage_backend, captured_at, id);

ALTER TABLE captures ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'local';
CREATE INDEX IF NOT EXISTS idx_captures_storage_captured
    ON captures(storage_backend, captured_at, id);

ALTER TABLE alarms ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'local';
ALTER TABLE person_faces ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'local';
ALTER TABLE canvas_maps ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'local';
