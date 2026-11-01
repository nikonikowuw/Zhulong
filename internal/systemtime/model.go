package systemtime

import "time"

// Mode constants
const (
	// ModeNTP keeps the clock disciplined by periodic NTP probes.
	ModeNTP = "ntp"
	// ModeManual only accepts explicit operator-set time; probes are skipped.
	ModeManual = "manual"
)

// SyncState constants
const (
	// SyncStateUnsynced means no probe has settled the clock yet (cold boot).
	SyncStateUnsynced = "unsynced"
	// SyncStateSyncing means a probe is in flight.
	SyncStateSyncing = "syncing"
	// SyncStateSynchronized means the last applied correction succeeded.
	SyncStateSynchronized = "synchronized"
	// SyncStatePanicReview means a panic-level offset awaits consecutive
	// confirmation and must not be reported as a success.
	SyncStatePanicReview = "panic_review"
	// SyncStateFailed means the last probe or clock write failed.
	SyncStateFailed = "failed"
)

// RTCStatus represents the hardware RTC health state.
type RTCStatus string

const (
	// RTCStatusNormal means the hardware RTC is present and readable.
	RTCStatusNormal RTCStatus = "normal"
	// RTCStatusMissing means no RTC device node and no sysfs entry were found.
	RTCStatusMissing RTCStatus = "missing"
	// RTCStatusError means the device exists but neither the node nor sysfs
	// yields a usable time.
	RTCStatusError RTCStatus = "error"
)

// Audit actions
//
// The time domain deliberately separates user intent from background clock
// work. `time.ntp_sync` is a *user* action recorded by the audit middleware when
// an operator presses sync. Background probes are not heartbeats to be logged:
// only a real wall-clock step, a panic-threshold review, and the failure/recovery
// edges are auditable.
const (
	// ActionTimeUpdateConfig records an accepted configuration change.
	ActionTimeUpdateConfig = "time.update_config"
	// ActionTimeManualSet records an explicit operator-set clock.
	ActionTimeManualSet = "time.manual_set"
	// ActionTimeNTPSync records a user-requested NTP probe.
	ActionTimeNTPSync = "time.ntp_sync"
	// ActionTimeRTCHeal records a cold-boot clock restore from the RTC chip.
	ActionTimeRTCHeal = "time.rtc_heal"
	// ActionTimeClockStep records a wheel-clock jump applied by the background
	// worker (cold-boot step, moderate step, or a panic offset confirmed by
	// consecutive probes). Steady-state slew never reaches the audit trail.
	ActionTimeClockStep = "time.clock_step"
	// ActionTimeNTPSyncFailed records the transition into a failed sync state.
	// It is written once per outage, not once per backoff retry.
	ActionTimeNTPSyncFailed = "time.ntp_sync_failed"
	// ActionTimeNTPRecovered records the transition back from a failed state.
	ActionTimeNTPRecovered = "time.ntp_sync_recovered"
)

// SystemTimeConfig is the persisted time configuration.
//
// It carries no json tags on purpose: it is a GORM model, and every response
// shape is built from SystemTimeStatus instead. Tagging it for serialization
// would invite leaking the primary key and the encoded server column.
type SystemTimeConfig struct {
	ID                  int64     `gorm:"primaryKey"`
	Mode                string    `gorm:"column:mode;type:text;not null;default:'ntp'"`
	NTPServersJSON      string    `gorm:"column:ntp_servers;type:text;not null"`
	SyncIntervalSeconds int       `gorm:"column:sync_interval_seconds;not null;default:900"`
	Timezone            string    `gorm:"column:timezone;type:text;not null;default:'Asia/Shanghai'"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null"`

	// NTPServers is the decoded form of NTPServersJSON so callers never have to
	// carry a second return value around. GORM ignores it; the repository fills
	// it on read.
	NTPServers []string `gorm:"-"`
}

// TimeConfigUpdate is a validated time configuration write: the domain-level
// shape shared by the service and the repository.
type TimeConfigUpdate struct {
	Mode                string
	NTPServers          []string
	SyncIntervalSeconds int
	Timezone            string
}

// TableName overrides the GORM table name.
func (SystemTimeConfig) TableName() string {
	return "system_time_configs"
}

// SyncStatus details the latest synchronization attempt or steady state.
type SyncStatus struct {
	State          string     `json:"state"`
	LastSyncTime   *time.Time `json:"lastSyncTime"`
	LastSyncServer string     `json:"lastSyncServer"`
	OffsetMS       float64    `json:"offsetMs"`
	RTTMS          float64    `json:"rttMs"`
	ErrorMessage   string     `json:"errorMessage"`
}

// SystemTimeStatus is the complete response payload returned by GET /api/v1/system/time.
type SystemTimeStatus struct {
	CurrentTime         time.Time  `json:"currentTime"`
	Timezone            string     `json:"timezone"`
	Mode                string     `json:"mode"`
	NTPServers          []string   `json:"ntpServers"`
	SyncIntervalSeconds int        `json:"syncIntervalSeconds"`
	SyncStatus          SyncStatus `json:"syncStatus"`
	RTCStatus           RTCStatus  `json:"rtcStatus"`
	HasPermission       bool       `json:"hasPermission"`
}

// UpdateConfigRequest represents the payload for PUT /api/v1/system/time/config.
type UpdateConfigRequest struct {
	Mode                string   `json:"mode" binding:"required,oneof=ntp manual"`
	NTPServers          []string `json:"ntpServers" binding:"required,min=1,max=5,dive,required"`
	SyncIntervalSeconds int      `json:"syncIntervalSeconds" binding:"required,min=60,max=86400"`
	Timezone            string   `json:"timezone" binding:"required"`
}

// ManualTimeRequest represents the payload for POST /api/v1/system/time/manual.
type ManualTimeRequest struct {
	TargetTime string `json:"targetTime" binding:"required"`
}
