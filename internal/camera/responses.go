package camera

import "time"

// StreamResponse represents public stream information in API responses.
type StreamResponse struct {
	UpdatedAt    time.Time        `json:"updatedAt"`
	CreatedAt    time.Time        `json:"createdAt"`
	FPS          *float64         `json:"fps"`
	RuntimeState *StreamStateInfo `json:"runtimeState,omitempty"`
	// RTSPURL is the complete RTSP URL for frontend playback and display.
	RTSPURL   string `json:"rtspUrl"`
	Transport string `json:"transport"`
	Codec     string `json:"codec"`
	// Host is the target host (IP or domain) for display and search;
	// empty when the URI cannot be resolved or decrypted.
	Host      string `json:"host"`
	FPSString string `json:"fpsString"`
	Protocol  string `json:"protocol"`
	Role      string `json:"role"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ID        int64  `json:"id"`
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

// AIStreamClientInfo is one algorithm package's delivery counters on a camera's AI stream.
type AIStreamClientInfo struct {
	ClientID        uint32 `json:"clientId"`
	Name            string `json:"name"`
	InFlight        uint32 `json:"inFlight"`
	MaxInFlight     uint32 `json:"maxInFlight"`
	DeliveredFrames uint64 `json:"deliveredFrames"`
	DroppedFrames   uint64 `json:"droppedFrames"`
}

// AIStreamStatsResponse reports per-algorithm frame delivery on one camera's AI stream.
//
// ClientCount is the true number of connected clients. When it exceeds len(clients)
// the list was truncated to the fixed ABI snapshot capacity — that itself is a signal
// worth looking at, since a single stream normally has a single-digit subscriber count.
type AIStreamStatsResponse struct {
	CameraID    string               `json:"cameraId"`
	ClientCount uint32               `json:"clientCount"`
	Clients     []AIStreamClientInfo `json:"clients"`
}

// DiagnoseResponse returns the outcome of an on-demand manual camera probe.
type DiagnoseResponse struct {
	CameraID string           `json:"cameraId"`
	State    *CameraStateInfo `json:"state"`
	Message  string           `json:"message"`
}
