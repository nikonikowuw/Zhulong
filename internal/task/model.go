package task

import (
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// Audit action constants for task operations.
const (
	ActionTaskCreate = "task.create"
	ActionTaskUpdate = "task.update"
	ActionTaskDelete = "task.delete"
)

// Task represents a video analysis task associated with a camera and an algorithm.
//
// ⚠️ Enabled 不得带 `gorm:"default:true"` 标签：GORM 对带 default 标签的零值字段会
// 从 INSERT 中省略该列，导致显式写入的 Enabled=false 被数据库默认值静默翻回 true。
// 表级 DEFAULT 1 已在迁移中声明，插入语义由调用方显式给出。
type Task struct {
	ID               string    `gorm:"primaryKey;column:id" json:"id"`
	CameraID         string    `gorm:"column:camera_id;not null;index" json:"cameraId"`
	AlgorithmID      string    `gorm:"column:algorithm_id;not null;index" json:"algorithmId"`
	Enabled          bool      `gorm:"column:enabled;not null" json:"enabled"`
	SampleFPS        int       `gorm:"column:sample_fps;not null;default:5" json:"sampleFps"`
	ConfigJSON       string    `gorm:"column:config_json;not null;default:'{}'" json:"configJson"`
	RegionConfigJSON string    `gorm:"column:region_config_json;not null;default:'{}'" json:"regionConfigJson"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName overrides the default table name for Task.
func (Task) TableName() string {
	return "analysis_tasks"
}

// SampleFPSMax 是单路分析任务的采样帧率上限。
const SampleFPSMax = 1000

// DefaultSampleFPS 是单路分析任务的默认采样帧率。
const DefaultSampleFPS = 5

// ValidateSampleFPS 校验并收敛分析任务的采样帧率。
func ValidateSampleFPS(raw int) (uint32, error) {
	if raw < 0 || raw > SampleFPSMax {
		return 0, apperr.Invalid("INVALID_TASK_CONFIG", fmt.Sprintf("sample_fps %d out of range [0, %d]", raw, SampleFPSMax), ErrInvalidTaskConfig)
	}
	return uint32(raw), nil
}

// CreateTaskRequest is the request payload for creating an analysis task.
//
// Both camelCase and snake_case keys are accepted: the snake_case alias is used
// only when the camelCase key is absent.
type CreateTaskRequest struct {
	ID                  string `json:"id"`
	CameraID            string `json:"cameraId"`
	CameraIDAlt         string `json:"camera_id"`
	AlgorithmID         string `json:"algorithmId"`
	AlgorithmIDAlt      string `json:"algorithm_id"`
	Enabled             *bool  `json:"enabled"`
	SampleFPS           int    `json:"sampleFps"`
	SampleFPSAlt        int    `json:"sample_fps"`
	ConfigJSON          string `json:"configJson"`
	ConfigJSONAlt       string `json:"config_json"`
	RegionConfigJSON    string `json:"regionConfigJson"`
	RegionConfigJSONAlt string `json:"region_config_json"`
}

// ToTask converts CreateTaskRequest into a Task entity skeleton,
// resolving camelCase and snake_case alias fields.
func (req *CreateTaskRequest) ToTask() *Task {
	camID := req.CameraID
	if camID == "" {
		camID = req.CameraIDAlt
	}
	algoID := req.AlgorithmID
	if algoID == "" {
		algoID = req.AlgorithmIDAlt
	}
	fps := req.SampleFPS
	if fps <= 0 {
		fps = req.SampleFPSAlt
	}
	if fps <= 0 {
		fps = DefaultSampleFPS
	}
	cfgJSON := req.ConfigJSON
	if cfgJSON == "" {
		cfgJSON = req.ConfigJSONAlt
	}
	if cfgJSON == "" {
		cfgJSON = "{}"
	}

	regionCfgJSON := req.RegionConfigJSON
	if regionCfgJSON == "" {
		regionCfgJSON = req.RegionConfigJSONAlt
	}
	if regionCfgJSON == "" {
		regionCfgJSON = "{}"
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	return &Task{
		ID:               req.ID,
		CameraID:         camID,
		AlgorithmID:      algoID,
		Enabled:          enabled,
		SampleFPS:        fps,
		ConfigJSON:       cfgJSON,
		RegionConfigJSON: regionCfgJSON,
	}
}

// UpdateTaskRequest is the request payload for updating an analysis task.
type UpdateTaskRequest struct {
	Enabled             *bool  `json:"enabled"`
	SampleFPS           int    `json:"sampleFps"`
	SampleFPSAlt        int    `json:"sample_fps"`
	ConfigJSON          string `json:"configJson"`
	ConfigJSONAlt       string `json:"config_json"`
	RegionConfigJSON    string `json:"regionConfigJson"`
	RegionConfigJSONAlt string `json:"region_config_json"`
}

// ApplyTo applies the update fields onto the target task entity.
func (req *UpdateTaskRequest) ApplyTo(t *Task) {
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	fps := req.SampleFPS
	if fps <= 0 {
		fps = req.SampleFPSAlt
	}
	if fps > 0 {
		t.SampleFPS = fps
	}
	cfg := req.ConfigJSON
	if cfg == "" {
		cfg = req.ConfigJSONAlt
	}
	if cfg != "" {
		t.ConfigJSON = cfg
	}
	regionCfg := req.RegionConfigJSON
	if regionCfg == "" {
		regionCfg = req.RegionConfigJSONAlt
	}
	if regionCfg != "" {
		t.RegionConfigJSON = regionCfg
	}
}
