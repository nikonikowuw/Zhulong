package storage

import "time"

// StorageHealthStatus represents the health and operating state of the storage subsystem.
type StorageHealthStatus string

const (
	StatusHealthy          StorageHealthStatus = "healthy"           // Storage has plenty of space and read/write is normal
	StatusWarning          StorageHealthStatus = "warning"           // Storage usage touched high watermark threshold
	StatusCleaning         StorageHealthStatus = "cleaning"          // Background cleaner is actively pruning expired/stale files
	StatusEmergencyStopped StorageHealthStatus = "emergency_stopped" // Storage touched critical emergency line; writes suspended
	StatusError            StorageHealthStatus = "error"             // Path missing, read-only, or external disk unmounted/fallen through
)

// Audit actions for storage lifecycle events.
const (
	ActionStorageUpdateConfig    = "system.storage.update_config"
	ActionStorageManualCleanup   = "system.storage.manual_cleanup"
	ActionStoragePathTest        = "system.storage.test_path"
	ActionStorageEmergencyStop   = "system.storage.emergency_stop"
	ActionStorageEmergencyResume = "system.storage.emergency_resume"
)

// StorageConfig holds the persistent media storage lifecycle and watermark configuration.
type StorageConfig struct {
	MediaDirectory          string `json:"mediaDirectory"`          // Root media path, e.g. "/mnt/storage/media"
	RecordingsRetentionDays int    `json:"recordingsRetentionDays"` // Video recordings retention in days (1..365)
	SnapshotsRetentionDays  int    `json:"snapshotsRetentionDays"`  // Snapshot captures retention in days (1..730)
	ExportsRetentionHours   int    `json:"exportsRetentionHours"`   // Exported clips retention in hours (1..168)
	HighWatermarkPercent    int    `json:"highWatermarkPercent"`    // High watermark percentage triggering cleaner (50..95)
	LowWatermarkPercent     int    `json:"lowWatermarkPercent"`     // Low watermark percentage where cleaner sleeps (40..85)
	EmergencyStopPercent    int    `json:"emergencyStopPercent"`    // Emergency write-stop percentage (90..99)
	EmergencyStopMinMB      int64  `json:"emergencyStopMinMb"`      // Emergency minimum free space in megabytes (100..102400)
}

// DefaultStorageConfig returns safe default settings tuned for edge NVR/AI boxes.
func DefaultStorageConfig() StorageConfig {
	return StorageConfig{
		MediaDirectory:          "data/media",
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

// StorageStatus is the telemetry DTO returned by GET /api/v1/system/storage/status.
type StorageStatus struct {
	MediaDirectory string                `json:"mediaDirectory"` // Currently active media directory
	MountPoint     string                `json:"mountPoint"`     // Mount point where the media directory lives
	FSType         string                `json:"fsType"`         // Filesystem type, e.g. "ext4", "xfs", "btrfs"
	TotalBytes     uint64                `json:"totalBytes"`     // Total capacity in bytes
	UsedBytes      uint64                `json:"usedBytes"`      // Used capacity in bytes
	FreeBytes      uint64                `json:"freeBytes"`      // Available capacity for non-root users in bytes
	UsagePercent   int                   `json:"usagePercent"`   // Volume usage percentage (0..100)
	Breakdown      StorageUsageBreakdown `json:"breakdown"`      // Categorized usage breakdown
	Status         StorageHealthStatus   `json:"status"`         // Current health status
	DeviceID       uint64                `json:"deviceId"`       // Associated filesystem device ID (st_dev)
	IsExternal     bool                  `json:"isExternal"`     // True if mounted on dedicated volume outside rootfs (/)
	CanWrite       bool                  `json:"canWrite"`       // True if storage is currently accepting new writes
	UpdatedAt      time.Time             `json:"updatedAt"`      // Metric collection timestamp (UTC)
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

// UpdateConfigRequest is the payload for PUT /api/v1/system/storage/config.
type UpdateConfigRequest struct {
	MediaDirectory          string `json:"mediaDirectory" binding:"required"`
	RecordingsRetentionDays int    `json:"recordingsRetentionDays" binding:"required,min=1,max=365"`
	SnapshotsRetentionDays  int    `json:"snapshotsRetentionDays" binding:"required,min=1,max=730"`
	ExportsRetentionHours   int    `json:"exportsRetentionHours" binding:"required,min=1,max=168"`
	HighWatermarkPercent    int    `json:"highWatermarkPercent" binding:"required,min=50,max=95"`
	LowWatermarkPercent     int    `json:"lowWatermarkPercent" binding:"required,min=40,max=85"`
	EmergencyStopPercent    int    `json:"emergencyStopPercent" binding:"required,min=90,max=99"`
	EmergencyStopMinMB      int64  `json:"emergencyStopMinMb" binding:"required,min=100,max=102400"`
}

// CleanupSummary details the execution metrics of a triggered prune cycle.
type CleanupSummary struct {
	TriggerReason string    `json:"triggerReason"` // "watermark", "scheduled", "manual"
	DeletedFiles  int       `json:"deletedFiles"`
	FreedBytes    int64     `json:"freedBytes"`
	DurationMs    int64     `json:"durationMs"`
	TargetStatus  string    `json:"targetStatus"`
	FinishedAt    time.Time `json:"finishedAt"`
}
