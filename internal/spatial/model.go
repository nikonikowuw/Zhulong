package spatial

import (
	"time"
)

// CanvasMap represents a 2D floorplan or campus layout image persisted in SQLite.
type CanvasMap struct {
	ID        string    `gorm:"primaryKey;column:id" json:"id"`
	Name      string    `gorm:"column:name;not null" json:"name"`
	ImagePath string    `gorm:"column:image_path;not null" json:"imagePath"`
	Width     int       `gorm:"column:width;not null;default:0" json:"width"`
	Height    int       `gorm:"column:height;not null;default:0" json:"height"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

// TableName returns the table name for GORM.
func (CanvasMap) TableName() string {
	return "canvas_maps"
}

// CameraCanvasPosition represents the normalized 2D coordinate and orientation angle of a camera on a specific map.
type CameraCanvasPosition struct {
	CameraID  string    `gorm:"primaryKey;column:camera_id" json:"cameraId"`
	MapID     string    `gorm:"primaryKey;column:map_id" json:"mapId"`
	X         float64   `gorm:"column:x;not null" json:"x"`
	Y         float64   `gorm:"column:y;not null" json:"y"`
	Angle     float64   `gorm:"column:angle;not null;default:0" json:"angle"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

// TableName returns the table name for GORM.
func (CameraCanvasPosition) TableName() string {
	return "camera_canvas_positions"
}
