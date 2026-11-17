-- 000038_create_event_clips.up.sql
-- 事件录像片段（告警 / 识别触发的前后段主码流 fMP4）。
--
-- ═══════════════════════════════════════════════════════════════════════════
-- event_clips
--   id          ULID（idutil.New），同时是文件名 evt-{id}.mp4 的一部分。
--   camera_id   ⚠️ 刻意不加 FK：片段是证据，删相机后片段文件与行仍保留，
--               由 recordings TTL / 水位清理（只被识别引用的片段）或人工处理。
--               与 alarms.camera_id 的 CASCADE 取舍不同 —— 片段不是事件行本身，
--               事件行被级联删除后，FK 的 ON DELETE SET NULL 只是让片段失去引用。
--   path        媒体根内的相对路径 recordings/{camera_id}/{YYYY-MM-DD}/evt-{id}.mp4。
--               ⚠️ 文件名不得含 '_'：storage mediaPrefix 以第一个 '_' 分组。
--   status      recording → completed / truncated / failed。
--   partial     1 = 启动修正或停机收尾得到的不完整片段。
--
-- alarms / recognitions
--   clip_id         可空 FK，片段行删除时置 NULL（文件被清理后行随之删除）。
--   clip_offset_ms  事件时刻在片段内的偏移（毫秒）。
--
-- task_algorithms.clip_config_json
--   每个任务算法的录像配置；'{}' = 关闭（默认，升级后行为不变）。
-- ═══════════════════════════════════════════════════════════════════════════

CREATE TABLE event_clips (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL,
    path TEXT NOT NULL,
    codec TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('recording', 'completed', 'truncated', 'failed')),
    partial INTEGER NOT NULL DEFAULT 0,
    started_at DATETIME NOT NULL,
    ended_at DATETIME,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_event_clips_camera_started ON event_clips(camera_id, started_at);
CREATE INDEX idx_event_clips_status ON event_clips(status);

ALTER TABLE alarms ADD COLUMN clip_id TEXT REFERENCES event_clips(id) ON DELETE SET NULL;
ALTER TABLE alarms ADD COLUMN clip_offset_ms INTEGER;
CREATE INDEX idx_alarms_clip ON alarms(clip_id);

ALTER TABLE recognitions ADD COLUMN clip_id TEXT REFERENCES event_clips(id) ON DELETE SET NULL;
ALTER TABLE recognitions ADD COLUMN clip_offset_ms INTEGER;
CREATE INDEX idx_recognitions_clip ON recognitions(clip_id);

ALTER TABLE task_algorithms ADD COLUMN clip_config_json TEXT NOT NULL DEFAULT '{}';
