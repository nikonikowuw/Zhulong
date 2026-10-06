package camera

import (
	"fmt"
	"time"
)

const (
	StreamRoleMain = "main"
	StreamRoleSub  = "sub"

	ProtocolRTSP    = "rtsp"
	ProtocolGB28181 = "gb28181"

	TransportTCP = "tcp"
	TransportUDP = "udp"

	HealthStateUnknown  = "unknown"
	HealthStateOnline   = "online"
	HealthStateOffline  = "offline"
	HealthStateError    = "error"
	HealthStateDegraded = "degraded"

	SessionStateIdle         = "idle"
	SessionStateStarting     = "starting"
	SessionStateRunning      = "running"
	SessionStateReconnecting = "reconnecting"
	SessionStateError        = "error"
)

// Camera represents the logical video camera entity persisted in SQLite.
type Camera struct {
	ID        string         `gorm:"primaryKey;column:id" json:"id"`
	Name      string         `gorm:"column:name;not null" json:"name"`
	Enabled   bool           `gorm:"column:enabled;not null;default:true" json:"enabled"`
	Revision  int64          `gorm:"column:revision;not null;default:1" json:"revision"`
	CreatedAt time.Time      `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at" json:"updatedAt"`
	Streams   []CameraStream `gorm:"foreignKey:CameraID;references:ID;constraint:OnDelete:CASCADE" json:"streams,omitempty"`
}

// TableName returns the table name for GORM.
func (Camera) TableName() string {
	return "cameras"
}

// CameraStream represents a specific stream (main/sub) associated with a camera.
type CameraStream struct {
	ID             int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	CameraID       string    `gorm:"column:camera_id;not null;index" json:"cameraId"`
	Role           string    `gorm:"column:role;not null" json:"role"`
	Protocol       string    `gorm:"column:protocol;not null;default:'rtsp'" json:"protocol"`
	EncryptedURI   []byte    `gorm:"column:encrypted_uri;not null" json:"-"`
	Transport      string    `gorm:"column:transport;not null;default:'tcp'" json:"transport"`
	Codec          string    `gorm:"column:codec;not null" json:"codec"`
	Width          int       `gorm:"column:width;not null" json:"width"`
	Height         int       `gorm:"column:height;not null" json:"height"`
	FPSNumerator   int       `gorm:"column:fps_numerator;not null;default:0" json:"fpsNumerator"`
	FPSDenominator int       `gorm:"column:fps_denominator;not null;default:1" json:"fpsDenominator"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName returns the table name for GORM.
func (CameraStream) TableName() string {
	return "camera_streams"
}

// HasKnownFPS returns true if FPS is valid and known.
func (s *CameraStream) HasKnownFPS() bool {
	return s.FPSNumerator > 0 && s.FPSDenominator > 0
}

// FPSValue returns the calculated FPS or nil if unknown.
func (s *CameraStream) FPSValue() *float64 {
	if !s.HasKnownFPS() {
		return nil
	}
	fps := float64(s.FPSNumerator) / float64(s.FPSDenominator)
	return &fps
}

// FormatFPS returns a human-readable FPS string or "unknown".
func (s *CameraStream) FormatFPS() string {
	if !s.HasKnownFPS() {
		return "unknown"
	}
	fps := float64(s.FPSNumerator) / float64(s.FPSDenominator)
	return fmt.Sprintf("%.2f", fps)
}
