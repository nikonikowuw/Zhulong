package spatial

import "time"

// UpdateCanvasMapRequest represents parameters for renaming a canvas map.
type UpdateCanvasMapRequest struct {
	Name string `json:"name" binding:"required,min=1,max=64"`
}

// CameraPositionItem is one camera's normalized coordinates and heading on a map.
//
// Range rules live here and only here: the service no longer rewrites out-of-range
// input, so a bad payload must be rejected at the edge (HTTP 422) instead of being
// silently clamped into a position the caller never asked for.
type CameraPositionItem struct {
	CameraID string  `json:"cameraId" binding:"required,min=1,max=64"`
	X        float64 `json:"x" binding:"gte=0,lte=1"`
	Y        float64 `json:"y" binding:"gte=0,lte=1"`
	Angle    float64 `json:"angle" binding:"gte=0,lt=360"`
}

// BatchSavePositionsRequest replaces the full set of calibrated positions of a map.
//
// `dive` is what makes the element rules above run at all: validator does not
// descend into a slice of structs without it. An absent `positions` field is
// rejected by the service; an explicitly empty array means "unbind every camera
// on this map" and is honoured.
type BatchSavePositionsRequest struct {
	Positions []CameraPositionItem `json:"positions" binding:"dive"`
}

// TrajectoryQuery represents parameters for searching a cross-camera trajectory.
type TrajectoryQuery struct {
	StartTime  time.Time `form:"startTime" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
	EndTime    time.Time `form:"endTime" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
	MapID      string    `form:"mapId" binding:"required"`
	TargetType string    `form:"targetType" binding:"required,oneof=vehicle person"`
	Plate      string    `form:"plate"`
	PersonID   string    `form:"personId"`
}
