package storage

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
)

// StorageHealthStatus represents the health and operating state of the storage subsystem.
type StorageHealthStatus string

// Values of StorageStatus.Status.
const (
	// StatusHealthy means there is plenty of space and read/write is normal.
	StatusHealthy StorageHealthStatus = "healthy"
	// StatusWarning means usage touched the high watermark and the cleaner may wake up.
	StatusWarning StorageHealthStatus = "warning"
	// StatusCleaning means the background cleaner is actively pruning files.
	StatusCleaning StorageHealthStatus = "cleaning"
	// StatusEmergencyStopped means the emergency line was hit and writes are suspended.
	StatusEmergencyStopped StorageHealthStatus = "emergency_stopped"
	// StatusError means the path is missing, read-only, or an external disk fell through.
	StatusError StorageHealthStatus = "error"
)

// Storage backend types and HTTP delivery modes.
const (
	BackendLocal = "local"
	BackendS3    = "s3"

	DeliveryModeProxy    = "proxy"
	DeliveryModeRedirect = "redirect"

	DefaultSpoolDirectory = "data/spool"
	DefaultSpoolMaxMB     = int64(4096)
)

// Audit actions for storage lifecycle events.
const (
	ActionStorageUpdateConfig    = "storage.update_config"
	ActionStorageManualCleanup   = "storage.manual_cleanup"
	ActionStorageAutoCleanup     = "storage.auto_cleanup"
	ActionStoragePathTest        = "storage.test_path"
	ActionStorageTestConnection  = "storage.test_connection"
	ActionStorageEmergencyStop   = "storage.emergency_stop"
	ActionStorageEmergencyResume = "storage.emergency_resume"
	ActionStorageMigrate         = "storage.migrate_remote"
)

// S3Config configures an S3v4-compatible remote object store (MinIO / cloud OSS).
type S3Config struct {
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix"`
	UsePathStyle       bool   `json:"usePathStyle"`
	UseTLS             bool   `json:"useTls"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	AccessKeyID        string `json:"accessKeyId"`
	// SecretAccessKey is write-only on incoming update/test payloads and is always
	// cleared before returning persisted configurations over HTTP or logging.
	SecretAccessKey    string `json:"secretAccessKey,omitempty"`
	HasSecretAccessKey bool   `json:"hasSecretAccessKey"`
	QuotaGB            int64  `json:"quotaGb"`
}

// StorageConfig holds the persistent media storage lifecycle and watermark configuration.
type StorageConfig struct {
	Backend                 string    `json:"backend"`                 // "local" (default) | "s3"
	DeliveryMode            string    `json:"deliveryMode"`            // "proxy" (default) | "redirect"
	LocalFallback           bool      `json:"localFallback"`           // Fallback read to MediaDirectory when S3 misses
	MediaDirectory          string    `json:"mediaDirectory"`          // Root media path, e.g. "/mnt/storage/media"
	SpoolDirectory          string    `json:"spoolDirectory"`          // Local spool dir for remote backends
	SpoolMaxMB              int64     `json:"spoolMaxMb"`              // Local spool size floor/cap in MB
	RecordingsRetentionDays int       `json:"recordingsRetentionDays"` // Recording retention in days (1..365)
	SnapshotsRetentionDays  int       `json:"snapshotsRetentionDays"`  // Snapshot retention in days (1..730)
	ExportsRetentionHours   int       `json:"exportsRetentionHours"`   // Export retention in hours (1..168)
	HighWatermarkPercent    int       `json:"highWatermarkPercent"`    // Cleaner wake-up usage (50..95)
	LowWatermarkPercent     int       `json:"lowWatermarkPercent"`     // Cleaner sleep usage (40..85)
	EmergencyStopPercent    int       `json:"emergencyStopPercent"`    // Write-stop usage (90..99)
	EmergencyStopMinMB      int64     `json:"emergencyStopMinMb"`      // Write-stop free-space floor in MB
	S3                      *S3Config `json:"s3,omitempty"`
}

// DefaultStorageConfig returns safe default settings tuned for edge NVR/AI boxes.
func DefaultStorageConfig() StorageConfig {
	return StorageConfig{
		Backend:                 BackendLocal,
		DeliveryMode:            DeliveryModeProxy,
		LocalFallback:           true,
		MediaDirectory:          "data/media",
		SpoolDirectory:          DefaultSpoolDirectory,
		SpoolMaxMB:              DefaultSpoolMaxMB,
		RecordingsRetentionDays: 15,
		SnapshotsRetentionDays:  90,
		ExportsRetentionHours:   48,
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      2048,
	}
}

// StorageUsageBreakdown itemizes disk consumption under the media directory and overall volume.
type StorageUsageBreakdown struct {
	RecordingsBytes int64 `json:"recordingsBytes"` // Total size of video recordings in bytes
	SnapshotsBytes  int64 `json:"snapshotsBytes"`  // Total size of AI snapshots in bytes
	ExportsBytes    int64 `json:"exportsBytes"`    // Total size of temporary exported files in bytes
	OtherBytes      int64 `json:"otherBytes"`      // Other files residing on the same filesystem/volume
}

// StorageStatus is the telemetry payload returned by GET /api/v1/system/storage/status.
type StorageStatus struct {
	Backend        string                `json:"backend"`        // Active backend: "local" | "s3"
	MediaDirectory string                `json:"mediaDirectory"` // Currently active media directory
	MountPoint     string                `json:"mountPoint"`     // Mount point where the media/spool directory lives
	FSType         string                `json:"fsType"`         // Filesystem type, e.g. "ext4", "xfs", "btrfs"
	TotalBytes     uint64                `json:"totalBytes"`     // Total capacity in bytes
	UsedBytes      uint64                `json:"usedBytes"`      // Used capacity in bytes
	FreeBytes      uint64                `json:"freeBytes"`      // Available capacity for non-root users in bytes
	UsagePercent   int                   `json:"usagePercent"`   // User-available usage percentage (0..100): used/(used+free)
	Breakdown      StorageUsageBreakdown `json:"breakdown"`      // Categorized usage breakdown
	Status         StorageHealthStatus   `json:"status"`         // Current health status
	DeviceID       uint64                `json:"deviceId"`       // Associated filesystem device ID (st_dev)
	IsExternal     bool                  `json:"isExternal"`     // True if mounted on dedicated volume outside rootfs (/)
	CanWrite       bool                  `json:"canWrite"`       // True if storage is currently accepting new writes
	RemoteHealthy  bool                  `json:"remoteHealthy"`  // True for local backend or healthy S3 bucket
	RemoteError    string                `json:"remoteError,omitempty"`
	SpoolDirectory string                `json:"spoolDirectory,omitempty"`
	SpoolUsedBytes uint64                `json:"spoolUsedBytes,omitempty"`
	SpoolFreeBytes uint64                `json:"spoolFreeBytes,omitempty"`
	UpdatedAt      time.Time             `json:"updatedAt"` // Metric collection timestamp (UTC)
}

// PathTestRequest is the payload for POST /api/v1/system/storage/test.
type PathTestRequest struct {
	Path string `json:"path" binding:"required"`
}

// PathTestResponse is the response for POST /api/v1/system/storage/test.
type PathTestResponse struct {
	Path        string `json:"path"`
	Exists      bool   `json:"exists"`
	Writable    bool   `json:"writable"`
	IsMount     bool   `json:"isMount"`
	MountPoint  string `json:"mountPoint"`
	FSType      string `json:"fsType"`
	TotalBytes  uint64 `json:"totalBytes"`
	FreeBytes   uint64 `json:"freeBytes"`
	DeviceID    uint64 `json:"deviceId"`
	IsExternal  bool   `json:"isExternal"`
	ErrorReason string `json:"errorReason,omitempty"`
}

// ConnectionTestRequest is the payload for POST /api/v1/system/storage/test-connection.
type ConnectionTestRequest struct {
	Backend string    `json:"backend" binding:"required"`
	Path    string    `json:"path"`
	S3      *S3Config `json:"s3,omitempty"`
}

// ConnectionTestResponse is the response for POST /api/v1/system/storage/test-connection.
type ConnectionTestResponse struct {
	Backend     string `json:"backend"`
	Reachable   bool   `json:"reachable"`
	Writable    bool   `json:"writable"`
	ErrorReason string `json:"errorReason,omitempty"`
	// Local path telemetry populated when backend is "local".
	MountPoint string `json:"mountPoint,omitempty"`
	FSType     string `json:"fsType,omitempty"`
	TotalBytes uint64 `json:"totalBytes,omitempty"`
	FreeBytes  uint64 `json:"freeBytes,omitempty"`
	DeviceID   uint64 `json:"deviceId,omitempty"`
	IsExternal bool   `json:"isExternal,omitempty"`
	IsMount    bool   `json:"isMount,omitempty"`
}

// UpdateConfigRequest is the payload for PUT /api/v1/system/storage/config.
type UpdateConfigRequest struct {
	Backend                 string    `json:"backend"`
	DeliveryMode            string    `json:"deliveryMode"`
	LocalFallback           *bool     `json:"localFallback,omitempty"`
	MediaDirectory          string    `json:"mediaDirectory"`
	SpoolDirectory          string    `json:"spoolDirectory"`
	SpoolMaxMB              int64     `json:"spoolMaxMb"`
	RecordingsRetentionDays int       `json:"recordingsRetentionDays" binding:"required,min=1,max=365"`
	SnapshotsRetentionDays  int       `json:"snapshotsRetentionDays" binding:"required,min=1,max=730"`
	ExportsRetentionHours   int       `json:"exportsRetentionHours" binding:"required,min=1,max=168"`
	HighWatermarkPercent    int       `json:"highWatermarkPercent" binding:"required,min=50,max=95"`
	LowWatermarkPercent     int       `json:"lowWatermarkPercent" binding:"required,min=40,max=85"`
	EmergencyStopPercent    int       `json:"emergencyStopPercent" binding:"required,min=90,max=99"`
	EmergencyStopMinMB      int64     `json:"emergencyStopMinMb" binding:"required,min=100,max=102400"`
	S3                      *S3Config `json:"s3,omitempty"`
}

// CleanupSummary details the execution metrics of a triggered prune cycle.
type CleanupSummary struct {
	TriggerReason  PruneTrigger    `json:"triggerReason"` // manual, watermark, or emergency
	DeletedFiles   int             `json:"deletedFiles"`
	FreedBytes     int64           `json:"freedBytes"`
	ProtectedFiles int             `json:"protectedFiles"`
	ProtectedBytes int64           `json:"protectedBytes"`
	DurationMs     int64           `json:"durationMs"`
	TargetStatus   PruneStatus     `json:"targetStatus"` // completed or skipped_below_high_watermark
	StopReason     PruneStopReason `json:"stopReason"`
	FinishedAt     time.Time       `json:"finishedAt"`

	// DeletedSnapshotIDs carries the snapshot IDs whose media files were fully
	// removed. It is internal transport to the record reconciler and must never
	// be serialized into the manual-cleanup API response.
	DeletedSnapshotIDs []string `json:"-"`
}

// StartMigrationRequest is the optional payload for POST /api/v1/system/storage/migrate.
type StartMigrationRequest struct {
	DeleteLocal bool `json:"deleteLocal"`
}

// MigrationJobResponse represents the HTTP status projection of an asynchronous
// local-to-remote storage migration job.
type MigrationJobResponse struct {
	JobID        string             `json:"jobId"`
	Status       asyncjob.JobStatus `json:"status"`
	Progress     int                `json:"progress"`
	Total        int                `json:"total"`
	Processed    int                `json:"processed"`
	Succeeded    int                `json:"succeeded"`
	Failed       int                `json:"failed"`
	CurrentStage string             `json:"currentStage,omitempty"`
	CurrentFile  string             `json:"currentFile,omitempty"`
	DeleteLocal  bool               `json:"deleteLocal"`
	ErrorCode    string             `json:"errorCode,omitempty"`
	ErrorMessage string             `json:"errorMessage,omitempty"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	FinishedAt   *time.Time         `json:"finishedAt,omitempty"`
}

// usagePercent converts byte counts into the user-available usage percentage,
// rounding up and matching `df`'s Use%: used / (used + available).
//
// Ext4 reserved blocks are only unavailable to unprivileged processes, so
// dividing by TotalBytes systematically underestimates pressure for a non-root
// daemon. Rounding up avoids 89.9% being truncated to 89% at high=90.
//
// On a root daemon Bavail usually equals Bfree, making this collapse to
// used/total; callers still need the min-free emergency gate as the backstop.
func usagePercent(stats *FSStats) int {
	if stats == nil {
		return 0
	}

	available := stats.UsedBytes + stats.FreeBytes
	if available == 0 {
		return 0
	}

	return int((stats.UsedBytes*100 + available - 1) / available)
}
