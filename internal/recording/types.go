package recording

import (
	"context"
	"errors"
	"time"
)

// ClipStatus is the lifecycle state of an event clip (event_clips.status).
type ClipStatus string

// Clip statuses. Only "recording" is non-terminal.
const (
	ClipStatusRecording ClipStatus = "recording"
	ClipStatusCompleted ClipStatus = "completed"
	ClipStatusTruncated ClipStatus = "truncated"
	ClipStatusFailed    ClipStatus = "failed"
)

// EventKind identifies the event table a clip is attached to.
type EventKind string

// Event kinds that may trigger a clip. Captures never trigger clips.
const (
	EventKindAlarm       EventKind = "alarm"
	EventKindRecognition EventKind = "recognition"
)

// DefaultMaxClipMs is the hard upper bound of a single clip (R3.4).
const DefaultMaxClipMs int64 = 120_000

// ClipTriggerRequest is the recording-local view of a native clip trigger.
type ClipTriggerRequest struct {
	// EventPTSUS is the event's stream PTS in microseconds; <=0 lets native use the latest packet.
	EventPTSUS int64
	// PostMs is how long to keep recording after the event.
	PostMs int64
	// MaxMs caps the total clip length; <=0 uses the native default.
	MaxMs int64
	// Path is the absolute target file path; native writes Path+".part" and renames on finish.
	Path string
}

// ClipTriggerResult is the recording-local view of a native trigger result.
type ClipTriggerResult struct {
	// Seq is unique within the engine's lifetime and keys completion events.
	Seq uint64
	// Created reports whether a new clip was started (Path used) rather than an active one extended.
	Created bool
	// Codec is the video codec name, e.g. "h264" / "hevc"; may be empty.
	Codec string
	// OffsetMs is the event position within the clip.
	OffsetMs int64
	// StartPTSUS is the PTS of the clip's first packet.
	StartPTSUS int64
}

// ClipEvent is a clip completion event drained from the native queue.
type ClipEvent struct {
	Seq        uint64
	Status     ClipStatus // completed / truncated / failed
	Codec      string
	DurationMs int64
	SizeBytes  int64
	FirstPTSUS int64
	LastPTSUS  int64
}

// ClipHub is the narrow port to the native recorder (implemented by an app-layer adapter
// over camera.StreamHub.TriggerClip and engine.Engine.PollClipEvents).
type ClipHub interface {
	TriggerClip(ctx context.Context, cameraID string, req ClipTriggerRequest) (ClipTriggerResult, error)
	PollClipEvents(ctx context.Context, max int) ([]ClipEvent, error)
}

// MediaRootProvider returns the currently effective media root.
// ⚠️ Re-read on every call: storage may hot-switch the directory.
type MediaRootProvider interface {
	MediaRoot(ctx context.Context) (string, error)
}

// WriteGate reports whether storage currently accepts new files.
type WriteGate interface {
	CanWrite() bool
}

// RetentionDaysProvider returns storage's RecordingsRetentionDays (re-read every call).
// <=0 disables age-based deletion of failed clip rows.
type RetentionDaysProvider interface {
	RecordingRetentionDays(ctx context.Context) (int, error)
}

// RetentionDaysFunc adapts a function to RetentionDaysProvider.
type RetentionDaysFunc func(ctx context.Context) (int, error)

// RecordingRetentionDays implements RetentionDaysProvider.
func (f RetentionDaysFunc) RecordingRetentionDays(ctx context.Context) (int, error) { return f(ctx) }

// ClipFileChecker verifies whether a clip's relative path exists under the active media
// root, distinguishing "not found" (false, nil) from "media volume unmounted/unavailable"
// (error) so fallthrough never deletes event_clips rows.
type ClipFileChecker interface {
	ClipFileExists(ctx context.Context, relativePath string) (bool, error)
}

// UploadStatus tracks asynchronous spool upload progress for remote backends.
type UploadStatus string

const (
	UploadStatusLocal    UploadStatus = "local"
	UploadStatusPending  UploadStatus = "pending"
	UploadStatusUploaded UploadStatus = "uploaded"
	UploadStatusFailed   UploadStatus = "failed"
)

// StorageTarget describes where new clips are recorded before optional remote upload.
type StorageTarget struct {
	Backend  string
	MediaDir string
	SpoolDir string
}

// StorageTargetProvider resolves the active storage backend and directories.
type StorageTargetProvider interface {
	RecordingTarget(ctx context.Context) (StorageTarget, error)
}

// ClipPolicy is the task-independent part of a clip policy needed to trigger.
type ClipPolicy struct {
	// PostSeconds is the post-event duration (1–60).
	PostSeconds int
}

// TriggerRequest asks the service to record (or extend) a clip for a persisted event row.
type TriggerRequest struct {
	CameraID    string
	AlgorithmID string
	EventKind   EventKind
	// EventID is the alarms.id / recognitions.id the clip is attached to.
	EventID string
	// PTSUS is the event frame PTS in microseconds when known.
	PTSUS *int64
	// CapturedAt selects the UTC date directory; zero uses now.
	CapturedAt time.Time
	Policy     ClipPolicy
}

var (
	// ErrClipSkipped means recording was intentionally not started (storage write gate closed).
	ErrClipSkipped = errors.New("event clip skipped: storage not writable")
	// ErrClipUnavailable means no recorder hub or media root is wired.
	ErrClipUnavailable = errors.New("event clip recording unavailable")
	// ErrClipStateLost means native extended a clip unknown to this process.
	ErrClipStateLost = errors.New("event clip state lost: extended clip is not tracked")
	// ErrInvalidTrigger means the trigger request is malformed.
	ErrInvalidTrigger = errors.New("invalid event clip trigger")
	// ErrEventNotFound means the event row to attach the clip to does not exist.
	ErrEventNotFound = errors.New("event row not found")
)
