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
	// ActionAuditClear is the self-audit record written when audit logs are
	// cleared. It is exempt from Prune so the clearing history survives.
	ActionAuditClear = "audit.clear"
)

// Default limits
const (
	DefaultMaxEntries = 5000
	DefaultBufferSize = 512
)

// TableName is the table that stores audit records. It is exported so that a
// caller needing the audited target name does not have to instantiate AuditLog
// just to reach TableName, which would pull the persistence model into the HTTP
// layer for a string.
const TableName = "audit_logs"

// AuditLog represents a persisted audit log record.
//
// It deliberately has no json tags: responses go through AuditLogResponse so a
// GORM model never reaches the wire.
type AuditLog struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	IP        string    `gorm:"column:ip;size:45;not null;default:''"`
	Username  string    `gorm:"column:username;size:64;not null;default:'admin'"`
	Action    string    `gorm:"column:action;size:64;not null"`
	Target    string    `gorm:"column:target;size:128;not null;default:''"`
	Detail    string    `gorm:"column:detail;type:text;not null;default:''"`
	Status    string    `gorm:"column:status;size:32;not null;default:'success'"`
	ErrorMsg  string    `gorm:"column:error_msg;type:text;not null;default:''"`
}

// TableName overrides the default GORM table name.
func (AuditLog) TableName() string {
	return TableName
}

// Entry represents an incoming audit event to record.
//
// It carries no json tags for the same reason as AuditLog: it is an internal
// projection, never a response body.
type Entry struct {
	IP        string
	Username  string
	Action    string
	Target    string
	Detail    string
	Status    string
	ErrorMsg  string
	CreatedAt time.Time
}

// Filter defines parameters for querying audit logs.
type Filter struct {
	// Action matches one exact action. It is the legacy single-value filter and
	// is ignored when Actions is non-empty.
	Action string
	// Actions matches any of the listed actions (OR semantics, ANDed with the
	// other filter fields).
	Actions   []string
	Status    string
	StartTime *time.Time
	EndTime   *time.Time
	Limit     int
	Offset    int
}
