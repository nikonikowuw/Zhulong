package spatial

import "time"

// CanvasMapResponse represents public map metadata in API responses.
type CanvasMapResponse struct {
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ImageURL  string    `json:"imageUrl"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
}

// CameraPositionInfo represents a camera's location and name on a canvas map.
type CameraPositionInfo struct {
	CameraID   string  `json:"cameraId"`
	CameraName string  `json:"cameraName"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Angle      float64 `json:"angle"`
}

// TrajectoryNodeInfo represents a spatio-temporal stop node along a target's trajectory.
//
// SnapshotPath is a media path relative to the media root (for example
// snapshots/{cameraID}/{date}/{id}_crop.jpg), not an HTTP URL: the record domain
// stores paths, so the field says so instead of claiming to be a URL the caller
// could load directly.
type TrajectoryNodeInfo struct {
	FirstSeenAt  time.Time `json:"firstSeenAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	CameraID     string    `json:"cameraId"`
	CameraName   string    `json:"cameraName"`
	SnapshotPath string    `json:"snapshotPath"`
	Details      string    `json:"details,omitempty"`
	Seq          int       `json:"seq"`
	DwellSec     int       `json:"dwellSec"`
	Score        float64   `json:"score"`
	X            float64   `json:"x"`
	Y            float64   `json:"y"`
}

// TrajectoryResponse represents the complete ordered trajectory along with metadata.
type TrajectoryResponse struct {
	MapID      string               `json:"mapId"`
	TargetType string               `json:"targetType"`
	TargetKey  string               `json:"targetKey"`
	Nodes      []TrajectoryNodeInfo `json:"nodes"`
}

// DeleteCanvasMapResponse reports the map removed by a successful delete.
type DeleteCanvasMapResponse struct {
	ID string `json:"id"`
}

// SaveCameraPositionsResponse reports how many positions the batch save persisted.
type SaveCameraPositionsResponse struct {
	MapID string `json:"mapId"`
	Count int    `json:"count"`
}

// utc returns t in UTC without an empty value: API time fields are RFC3339 in UTC
// (http-api-guidelines.md §2), and a zero time must stay zero instead of turning
// into year 1 in a different zone.
func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}
