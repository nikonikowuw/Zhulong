package camera

import "time"

// StreamResponse represents public stream information in API responses.
type StreamResponse struct {
	ID           int64            `json:"id"`
	Role         string           `json:"role"`
	Protocol     string           `json:"protocol"`
	RTSPURL      string           `json:"rtspUrl"` // Complete RTSP URL for frontend playback and display
	Transport    string           `json:"transport"`
	Codec        string           `json:"codec"`
	Width        int              `json:"width"`
	Height       int              `json:"height"`
	FPS          *float64         `json:"fps"`
	FPSString    string           `json:"fpsString"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	RuntimeState *StreamStateInfo `json:"runtimeState,omitempty"`
}

// CameraResponse represents public camera status and stream information in API responses.
type CameraResponse struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Enabled       bool             `json:"enabled"`
	Revision      int64            `json:"revision"`
	Health        string           `json:"health"`
	Session       string           `json:"session"`
	Degraded      bool             `json:"degraded"`
	Stale         bool             `json:"stale"`
	Reason        string           `json:"reason,omitempty"`
	LastCheckedAt *time.Time       `json:"lastCheckedAt,omitempty"`
	LastSuccessAt *time.Time       `json:"lastSuccessAt,omitempty"`
	Streams       []StreamResponse `json:"streams"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

// CameraCredentialsResponse provides decrypted stream URLs for authorized administrator inspection/copying.
type CameraCredentialsResponse struct {
	CameraID    string            `json:"cameraId"`
	Credentials map[string]string `json:"credentials"` // role -> full plaintext RTSP URL
}

// DiagnoseResponse returns the outcome of an on-demand manual camera probe.
type DiagnoseResponse struct {
	CameraID string           `json:"cameraId"`
	State    *CameraStateInfo `json:"state"`
	Message  string           `json:"message"`
}
