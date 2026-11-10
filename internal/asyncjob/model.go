package asyncjob

import (
	"context"
	"slices"
	"time"
)

// JobStatus represents the execution lifecycle state of a system background job.
//
// The state machine is strictly forward: PENDING -> EXTRACTING|PROCESSING ->
// COMPLETED|FAILED|CANCELLED. Terminal states never transition again; in
// particular a finished job cannot be cancelled retroactively.
type JobStatus string

const (
	StatusPending    JobStatus = "PENDING"
	StatusExtracting JobStatus = "EXTRACTING"
	StatusProcessing JobStatus = "PROCESSING"
	StatusCompleted  JobStatus = "COMPLETED"
	StatusFailed     JobStatus = "FAILED"
	StatusCancelled  JobStatus = "CANCELLED"
)

// activeStatuses are the only states a job may be updated from. Every write
// that finishes, cancels or claims a job filters on this set, so a late writer
// can never resurrect a terminal row.
var activeStatuses = []JobStatus{StatusPending, StatusExtracting, StatusProcessing}

// IsTerminal reports whether the status ends the job lifecycle.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// IsActive reports whether the status belongs to the in-flight lifecycle.
func (s JobStatus) IsActive() bool {
	return slices.Contains(activeStatuses, s)
}

// JobStage is the human-facing phase a handler reports while it works. It is
// deliberately separate from JobStatus: several stages share one status.
type JobStage string

const (
	StageInitializing JobStage = "INITIALIZING"
	StageExtracting   JobStage = "EXTRACTING"
	StageProcessing   JobStage = "PROCESSING"
	StageCompleted    JobStage = "COMPLETED"
)

// Machine codes emitted by this package. Every one is registered in locale.go
// with en / zh-Hans / zh-Hant text. Codes that describe a terminal state are
// also persisted in system_async_jobs.error_code, so a failure reason survives
// restarts without storing raw driver or handler text.
const (
	CodeJobNotFound        = "JOB_NOT_FOUND"
	CodeJobTypeUnsupported = "JOB_TYPE_UNSUPPORTED"
	CodeJobQueueFull       = "ASYNC_JOB_QUEUE_FULL"
	CodeJobNotCancellable  = "JOB_NOT_CANCELLABLE"
	CodeJobServiceStopped  = "JOB_SERVICE_STOPPED"
	CodeJobExecutionFailed = "JOB_EXECUTION_FAILED"
	CodeJobPanicked        = "JOB_PANICKED"
	CodeJobInterrupted     = "JOB_INTERRUPTED"
	CodeJobShutdown        = "JOB_SHUTDOWN"
)

// ActionCancelJob is the audit action for a manual job cancellation. Audit
// actions are `<domain>.<verb>` and declared as constants, never raw strings.
const ActionCancelJob = "job.cancel"

// Job represents a persistent background job entity in the system.
//
// This is the persistence model and carries no JSON tags on purpose: the HTTP
// layer always projects through JobResponse.
type Job struct {
	ID           string     `gorm:"primaryKey;column:id"`
	Type         string     `gorm:"column:type;not null;index"`
	Status       JobStatus  `gorm:"column:status;not null;index"`
	Progress     int        `gorm:"column:progress;not null;default:0"`
	Total        int        `gorm:"column:total;not null;default:0"`
	Processed    int        `gorm:"column:processed;not null;default:0"`
	Succeeded    int        `gorm:"column:succeeded;not null;default:0"`
	Failed       int        `gorm:"column:failed;not null;default:0"`
	CurrentStage string     `gorm:"column:current_stage;not null;default:''"`
	CurrentFile  string     `gorm:"column:current_file;not null;default:''"`
	MetadataJSON string     `gorm:"column:metadata_json;not null;default:'{}'"`
	ResultJSON   string     `gorm:"column:result_json;not null;default:'{}'"`
	ErrorCode    string     `gorm:"column:error_code;not null;default:''"`
	ErrorMessage string     `gorm:"column:error_message;not null;default:''"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;index"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
	FinishedAt   *time.Time `gorm:"column:finished_at"`
}

// TableName returns the table name for GORM.
func (Job) TableName() string {
	return "system_async_jobs"
}

// FinishOutcome describes the terminal state and summary recorded when an
// active job finishes.
type FinishOutcome struct {
	Status     JobStatus
	ResultJSON string
	ErrorCode  string
}

// ProgressUpdate represents an incremental progress telemetry payload.
type ProgressUpdate struct {
	// Stage is the handler-specific phase label shown by the UI.
	Stage JobStage
	// Status optionally promotes the job to a non-default active status, such
	// as EXTRACTING. Empty means "keep the job in PROCESSING".
	Status      JobStatus
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

// JobAborter is optionally implemented by a JobHandler that owns side
// artifacts outside the database (temp directories, staged uploads). It is
// called when a job will never execute — cancelled while queued, rejected by a
// full queue, or dropped during shutdown — so those artifacts are released
// without running the work.
type JobAborter interface {
	Abort(ctx context.Context, job *Job) error
}
