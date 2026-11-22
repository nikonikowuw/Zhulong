package recording

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

// EventClip is the GORM model for the event_clips table.
type EventClip struct {
	ID       string `gorm:"primaryKey;column:id"`
	CameraID string `gorm:"column:camera_id;not null"`
	// Path is relative to the media root: recordings/{camera}/{YYYY-MM-DD}/evt-{id}.mp4.
	Path           string       `gorm:"column:path;not null"`
	Codec          string       `gorm:"column:codec;not null"`
	Status         ClipStatus   `gorm:"column:status;not null"`
	Partial        bool         `gorm:"column:partial;not null"`
	StorageBackend string       `gorm:"column:storage_backend;not null;default:'local'"`
	UploadStatus   UploadStatus `gorm:"column:upload_status;not null;default:'local'"`
	StartedAt      time.Time    `gorm:"column:started_at;not null"`
	EndedAt        *time.Time   `gorm:"column:ended_at"`
	DurationMS     int64        `gorm:"column:duration_ms;not null"`
	SizeBytes      int64        `gorm:"column:size_bytes;not null"`
	CreatedAt      time.Time    `gorm:"column:created_at;not null"`
}

// TableName overrides the default table name.
func (EventClip) TableName() string { return "event_clips" }

// ClipResult is the terminal state written to a clip row.
type ClipResult struct {
	Status     ClipStatus
	Codec      string
	Partial    bool
	DurationMS int64
	SizeBytes  int64
	EndedAt    time.Time
}

// Store persists event clips and their association with event rows.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a Store. Pure constructor.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s == nil || s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

// CreateClip inserts a new clip row.
func (s *Store) CreateClip(ctx context.Context, clip *EventClip) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if clip.CreatedAt.IsZero() {
		clip.CreatedAt = now
	}
	if clip.StartedAt.IsZero() {
		clip.StartedAt = now
	}
	if clip.StorageBackend == "" {
		clip.StorageBackend = "local"
	}
	if clip.UploadStatus == "" {
		if clip.StorageBackend == "local" {
			clip.UploadStatus = UploadStatusLocal
		} else {
			clip.UploadStatus = UploadStatusPending
		}
	}
	clip.StartedAt = clip.StartedAt.UTC()
	clip.CreatedAt = clip.CreatedAt.UTC()
	return gorm.G[EventClip](db).Create(ctx, clip)
}

// SetUploadStatus updates storage_backend and upload_status for a finished clip.
func (s *Store) SetUploadStatus(
	ctx context.Context,
	id string,
	storageBackend string,
	status UploadStatus,
) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.WithContext(ctx).Model(&EventClip{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"storage_backend": storageBackend,
			"upload_status":   status,
		})
	return res.Error
}

// PendingUploadClips lists completed/truncated remote clips awaiting upload recovery.
func (s *Store) PendingUploadClips(ctx context.Context, limit int) ([]EventClip, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultCleanupBatch
	}
	rows, err := gorm.G[EventClip](db).
		Where(
			"storage_backend <> ? AND status IN ? AND upload_status IN ?",
			"local",
			[]ClipStatus{ClipStatusCompleted, ClipStatusTruncated},
			[]UploadStatus{UploadStatusPending, UploadStatusFailed},
		).
		Order("started_at ASC, id ASC").
		Limit(limit).
		Find(ctx)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []EventClip{}
	}
	return rows, nil
}

// GetClip returns a clip by ID, or (nil, nil) when missing.
func (s *Store) GetClip(ctx context.Context, id string) (*EventClip, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	clip, err := gorm.G[EventClip](db).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &clip, nil
}

// eventTable maps an event kind to its table; unknown kinds are rejected so the
// table name is never derived from caller input.
func eventTable(kind EventKind) (string, error) {
	switch kind {
	case EventKindAlarm:
		return "alarms", nil
	case EventKindRecognition:
		return "recognitions", nil
	default:
		return "", fmt.Errorf("%w: unknown event kind %q", ErrInvalidTrigger, kind)
	}
}

// AttachClip sets clip_id / clip_offset_ms on an event row.
func (s *Store) AttachClip(ctx context.Context, kind EventKind, eventID, clipID string, offsetMs int64) error {
	table, err := eventTable(kind)
	if err != nil {
		return err
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Table(table).Where("id = ?", eventID).Updates(map[string]any{
		"clip_id":        clipID,
		"clip_offset_ms": offsetMs,
	})
	if res.Error != nil {
		return fmt.Errorf("attach clip to %s %q: %w", table, eventID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: %s %q", ErrEventNotFound, table, eventID)
	}
	return nil
}

// FinishClip writes the terminal state of a clip that is still recording.
// Returns false when no recording row matched (already finished or deleted).
func (s *Store) FinishClip(ctx context.Context, id string, result ClipResult) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	endedAt := result.EndedAt.UTC()
	updates := map[string]any{
		"status":      result.Status,
		"partial":     result.Partial,
		"duration_ms": result.DurationMS,
		"size_bytes":  result.SizeBytes,
		"ended_at":    endedAt,
	}
	if result.Codec != "" {
		updates["codec"] = result.Codec
	}
	res := db.Model(&EventClip{}).
		Where("id = ? AND status = ?", id, ClipStatusRecording).
		Updates(updates)
	if res.Error != nil {
		return false, fmt.Errorf("finish clip %q: %w", id, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// RecordingClips returns every row still marked recording (start-up repair input).
func (s *Store) RecordingClips(ctx context.Context) ([]EventClip, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := gorm.G[EventClip](db).Where("status = ?", ClipStatusRecording).Order("id").Find(ctx)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []EventClip{}
	}
	return rows, nil
}

// FinishedClipsAfter pages through completed/truncated clips ordered by id (cursor = afterID).
func (s *Store) FinishedClipsAfter(ctx context.Context, afterID string, limit int) ([]EventClip, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultCleanupBatch
	}
	rows, err := gorm.G[EventClip](db).
		Where("id > ? AND status IN ?", afterID, []ClipStatus{ClipStatusCompleted, ClipStatusTruncated}).
		Order("id").
		Limit(limit).
		Find(ctx)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []EventClip{}
	}
	return rows, nil
}

// DeleteClips deletes clip rows by ID; event rows referencing them are set NULL by the FK.
func (s *Store) DeleteClips(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	// 只删已确定状态的行：并发 Trigger 新建的 recording 行绝不能被清理误删。
	res := db.Where("id IN ? AND status <> ?", ids, ClipStatusRecording).Delete(&EventClip{})
	return res.RowsAffected, res.Error
}

// DeleteFailedClipsBefore deletes failed clip rows that started before cutoff.
func (s *Store) DeleteFailedClipsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	res := db.Where("status = ? AND started_at < ?", ClipStatusFailed, cutoff.UTC()).Delete(&EventClip{})
	return res.RowsAffected, res.Error
}
