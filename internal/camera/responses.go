package camera

import "time"

// StreamResponse represents public stream information in API responses.
type StreamResponse struct {
	UpdatedAt    time.Time        `json:"updatedAt"`
	CreatedAt    time.Time        `json:"createdAt"`
	FPS          *float64         `json:"fps"`
	RuntimeState *StreamStateInfo `json:"runtimeState,omitempty"`
	RTSPURL      string           `json:"rtspUrl"` // Complete RTSP URL for frontend playback and display
	Transport    string           `json:"transport"`
	Codec        string           `json:"codec"`
	Host         string           `json:"host"` // Target host (IP or domain) for display and search; empty when the URI cannot be resolved or decrypted
	FPSString    string           `json:"fpsString"`
	Protocol     string           `json:"protocol"`
	Role         string           `json:"role"`
	Width        int              `json:"width"`
	Height       int              `json:"height"`
	ID           int64            `json:"id"`
}

// CameraResponse represents public camera status and stream information in API responses.
type CameraResponse struct {
	UpdatedAt     time.Time        `json:"updatedAt"`
	CreatedAt     time.Time        `json:"createdAt"`
	LastSuccessAt *time.Time       `json:"lastSuccessAt,omitempty"`
	LastCheckedAt *time.Time       `json:"lastCheckedAt,omitempty"`
	Name          string           `json:"name"`
	ID            string           `json:"id"`
	Health        string           `json:"health"`
	Session       string           `json:"session"`
	Reason        string           `json:"reason,omitempty"`
	Streams       []StreamResponse `json:"streams"`
	Revision      int64            `json:"revision"`
	Stale         bool             `json:"stale"`
	Degraded      bool             `json:"degraded"`
	Enabled       bool             `json:"enabled"`
}

// CameraCredentialsResponse provides decrypted stream URLs for authorized administrator inspection/copying.
type CameraCredentialsResponse struct {
	Credentials map[string]string `json:"credentials"` // role -> full plaintext RTSP URL
	CameraID    string            `json:"cameraId"`
}

// DiagnoseResponse returns the outcome of an on-demand manual camera probe.
type DiagnoseResponse struct {
	CameraID string           `json:"cameraId"`
	State    *CameraStateInfo `json:"state"`
	Message  string           `json:"message"`
}
