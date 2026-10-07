package audit

import "time"

// Status constants
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// Standard action constants
const (
	ActionAuthInit     = "auth.init"
	ActionAuthLogin    = "auth.login"
	ActionAuthLogout   = "auth.logout"
	ActionCameraCreate = "camera.create"
	ActionCameraUpdate = "camera.update"
	ActionCameraDelete = "camera.delete"
	ActionCameraToggle = "camera.toggle"
)

// Default limits
const (
	DefaultMaxEntries = 5000
	DefaultBufferSize = 512
)

// AuditLog represents a persisted audit log record.
type AuditLog struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	IP        string    `gorm:"column:ip;size:45;not null;default:''" json:"ip"`
	Username  string    `gorm:"column:username;size:64;not null;default:'admin'" json:"username"`
	Action    string    `gorm:"column:action;size:64;not null" json:"action"`
	Target    string    `gorm:"column:target;size:128;not null;default:''" json:"target"`
	Detail    string    `gorm:"column:detail;type:text;not null;default:''" json:"detail"`
	Status    string    `gorm:"column:status;size:32;not null;default:'success'" json:"status"`
	ErrorMsg  string    `gorm:"column:error_msg;type:text;not null;default:''" json:"errorMsg"`
}

// TableName overrides the default GORM table name.
func (AuditLog) TableName() string {
	return "audit_logs"
}

// Entry represents an incoming audit event to record.
type Entry struct {
	IP        string    `json:"ip"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	Status    string    `json:"status"`
	ErrorMsg  string    `json:"errorMsg"`
	CreatedAt time.Time `json:"createdAt"`
}

// Filter defines parameters for querying audit logs.
type Filter struct {
	Action    string
	Status    string
	StartTime *time.Time
	EndTime   *time.Time
	Limit     int
	Offset    int
}
