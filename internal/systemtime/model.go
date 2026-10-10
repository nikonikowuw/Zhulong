package systemtime

import "time"

// Mode constants
const (
	ModeNTP    = "ntp"
	ModeManual = "manual"
)

// SyncState constants
const (
	SyncStateUnsynced     = "unsynced"
	SyncStateSyncing      = "syncing"
	SyncStateSynchronized = "synchronized"
	SyncStatePanicReview  = "panic_review"
	SyncStateFailed       = "failed"
)

// RtcStatus represents the hardware RTC health state
type RtcStatus string

const (
	RtcStatusNormal  RtcStatus = "normal"
	RtcStatusMissing RtcStatus = "missing"
	RtcStatusError   RtcStatus = "error"
)

// Audit actions
const (
	ActionTimeUpdateConfig = "system.time.update_config"
	ActionTimeManualSet    = "system.time.manual_set"
	ActionTimeNTPSync      = "system.time.ntp_sync"
	ActionTimeRTCHeal      = "system.time.rtc_heal"
)

// SystemTimeConfig represents the persisted time configuration.
type SystemTimeConfig struct {
	ID                  int64     `gorm:"primaryKey" json:"id"`
	Mode                string    `gorm:"column:mode;type:text;not null;default:'ntp'" json:"mode"`
	NTPServersJSON      string    `gorm:"column:ntp_servers;type:text;not null" json:"-"`
	SyncIntervalSeconds int       `gorm:"column:sync_interval_seconds;not null;default:900" json:"syncIntervalSeconds"`
	Timezone            string    `gorm:"column:timezone;type:text;not null;default:'Asia/Shanghai'" json:"timezone"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
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
	OffsetMs       float64    `json:"offsetMs"`
	RttMs          float64    `json:"rttMs"`
	ErrorMessage   string     `json:"errorMessage"`
}

// SystemTimeStatus is the complete DTO returned by GET /api/v1/system/time.
type SystemTimeStatus struct {
	CurrentTime         time.Time  `json:"currentTime"`
	Timezone            string     `json:"timezone"`
	Mode                string     `json:"mode"`
	NTPServers          []string   `json:"ntpServers"`
	SyncIntervalSeconds int        `json:"syncIntervalSeconds"`
	SyncStatus          SyncStatus `json:"syncStatus"`
	RTCStatus           RtcStatus  `json:"rtcStatus"`
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
