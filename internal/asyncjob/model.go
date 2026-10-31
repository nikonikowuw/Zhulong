package asyncjob

import (
	"context"
	"time"
)

// JobStatus represents the execution lifecycle state of a system background job.
type JobStatus string

const (
	StatusPending    JobStatus = "PENDING"
	StatusProcessing JobStatus = "PROCESSING"
	StatusCompleted  JobStatus = "COMPLETED"
	StatusFailed     JobStatus = "FAILED"
	StatusCancelled  JobStatus = "CANCELLED"
)

// Job represents a persistent background job entity in the system.
type Job struct {
	ID           string     `gorm:"primaryKey;column:id" json:"id"`
	Type         string     `gorm:"column:type;not null;index" json:"type"`
	Status       JobStatus  `gorm:"column:status;not null;index" json:"status"`
	Progress     int        `gorm:"column:progress;not null;default:0" json:"progress"`
	Total        int        `gorm:"column:total;not null;default:0" json:"total"`
	Processed    int        `gorm:"column:processed;not null;default:0" json:"processed"`
	Succeeded    int        `gorm:"column:succeeded;not null;default:0" json:"succeeded"`
	Failed       int        `gorm:"column:failed;not null;default:0" json:"failed"`
	CurrentStage string     `gorm:"column:current_stage;not null;default:''" json:"currentStage"`
	CurrentFile  string     `gorm:"column:current_file;not null;default:''" json:"currentFile"`
	MetadataJSON string     `gorm:"column:metadata_json;not null;default:'{}'" json:"metadata"`
	ResultJSON   string     `gorm:"column:result_json;not null;default:'{}'" json:"result"`
	ErrorMessage string     `gorm:"column:error_message;not null;default:''" json:"errorMessage"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;index" json:"createdAt"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null" json:"updatedAt"`
	FinishedAt   *time.Time `gorm:"column:finished_at" json:"finishedAt,omitempty"`
}

// TableName returns the table name for GORM.
func (Job) TableName() string {
	return "system_async_jobs"
}

// ProgressUpdate represents an incremental progress telemetry payload.
type ProgressUpdate struct {
	Stage       string
	CurrentFile string
	Total       int
	Processed   int
	Succeeded   int
	Failed      int
}

// ProgressUpdater provides methods for job handlers to push progress.
type ProgressUpdater interface {
	Update(ctx context.Context, p ProgressUpdate) error
}

// JobHandler defines the contract for domain-specific background job execution.
type JobHandler interface {
	// Type returns the unique string identifier for this job type (e.g. "person.face_import").
	Type() string
	// Execute executes the job logic asynchronously.
	Execute(ctx context.Context, job *Job, updater ProgressUpdater) error
}
