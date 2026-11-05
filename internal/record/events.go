package record

import (
	"context"
	"time"
)

// EventType distinguishes the category of a dispatched perception event.
type EventType string

const (
	EventTypeAlarm            EventType = "alarm"
	EventTypeFaceRecognition  EventType = "face_recognition"
	EventTypePlateRecognition EventType = "plate_recognition"
)

// FaceEventData holds face recognition details and dynamic attributes.
type FaceEventData struct {
	Matched          bool           `json:"matched"`
	PersonName       string         `json:"person_name"`
	PersonID         string         `json:"person_id"` // 对外业务键（person_external_id）
	Similarity       float64        `json:"similarity"`
	Box              [4]float64     `json:"box"`
	GalleryImagePath string         `json:"gallery_image_path,omitempty"`
	Attributes       map[string]any `json:"attributes"`
}

// VehicleEventData holds vehicle & license plate recognition details and dynamic attributes.
type VehicleEventData struct {
	PlateNumber  string         `json:"plate_number"`
	PlateColor   string         `json:"plate_color"`
	VehicleType  string         `json:"vehicle_type"`
	VehicleColor string         `json:"vehicle_color"`
	Score        float64        `json:"score"`
	Box          [4]float64     `json:"box"`
	Attributes   map[string]any `json:"attributes"`
}

// AlarmEventData holds safety & behavior alarm details.
type AlarmEventData struct {
	AlarmType string         `json:"alarm_type"`
	Score     float64        `json:"score"`
	Box       [4]float64     `json:"box"`
	Extra     map[string]any `json:"extra"`
}

// AlarmEvent represents a perception event ready for downstream dispatch across alarm, face, and vehicle events.
// ImageReady and CropReady independently signify whether the corresponding image files have been written to disk.
type AlarmEvent struct {
	ID          string    `json:"id"`
	EventType   EventType `json:"event_type"` // "alarm" | "face_recognition" | "plate_recognition"
	CameraID    string    `json:"camera_id"`
	CapturedAt  time.Time `json:"captured_at"`
	ImagePath   string    `json:"image_path,omitempty"`
	CropPath    string    `json:"crop_path,omitempty"`
	ImageReady  bool      `json:"image_ready"`
	CropReady   bool      `json:"crop_ready"`
	AlgorithmID string    `json:"algorithm_id,omitempty"`

	// Category-specific payloads (one is non-nil based on EventType)
	Alarm   *AlarmEventData   `json:"alarm,omitempty"`
	Face    *FaceEventData    `json:"face,omitempty"`
	Vehicle *VehicleEventData `json:"vehicle,omitempty"`

	// Backward-compatible shortcut fields for legacy behavior alarms
	AlarmType string         `json:"-"`
	Score     float64        `json:"-"`
	Box       [4]float64     `json:"-"`
	Extra     map[string]any `json:"-"`
}

// AlarmListener receives finalized events.
//
// ctx 的生命周期不保证等于推理请求：同步分发（dispatchCreated）传入当次
// ProcessInferenceResult 的 ctx，异步路径（sweeper 超时降级、快照完成回调）传入
// 后台 context。实现方不得把 ctx 取消当成「事件被撤销」的信号，也不得把它存进结构体。
type AlarmListener interface {
	OnAlarm(ctx context.Context, ev AlarmEvent)
}

// SnapshotJobOutput reports write completion for one snapshot output path.
type SnapshotJobOutput struct {
	RelPath string
	Written bool
}

// SnapshotJobResult summarizes the result of a snapshot write job.
type SnapshotJobResult struct {
	Outputs []SnapshotJobOutput
}

// SnapshotJobObserver observes snapshot job completion per target record.
type SnapshotJobObserver interface {
	OnSnapshotJobDone(targetTable, targetID string, result SnapshotJobResult)
}
