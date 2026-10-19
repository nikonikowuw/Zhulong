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

// Task represents a channel video analysis task aggregate root associated with a camera and multiple algorithms.
//
// ⚠️ Enabled 不得带 `gorm:"default:true"` 标签：GORM 对带 default 标签的零值字段会
// 从 INSERT 中省略该列，导致显式写入的 Enabled=false 被数据库默认值静默翻回 true。
// 表级 DEFAULT 1 已在迁移中声明，插入语义由调用方显式给出。
type Task struct {
	ID         string          `gorm:"primaryKey;column:id" json:"id"`
	CameraID   string          `gorm:"column:camera_id;not null;uniqueIndex" json:"cameraId"`
	Enabled    bool            `gorm:"column:enabled;not null" json:"enabled"`
	Name       string          `gorm:"column:name;not null;default:''" json:"name"`
	CreatedAt  time.Time       `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt  time.Time       `gorm:"column:updated_at;not null" json:"updatedAt"`
	Algorithms []TaskAlgorithm `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE" json:"algorithms"`
}

// TableName overrides the default table name for Task.
func (Task) TableName() string {
	return "analysis_tasks"
}

// TaskAlgorithm represents a specific algorithm configuration item bound to a channel task.
type TaskAlgorithm struct {
	ID               string    `gorm:"primaryKey;column:id" json:"id"`
	TaskID           string    `gorm:"column:task_id;not null;index" json:"taskId"`
	AlgorithmID      string    `gorm:"column:algorithm_id;not null;index" json:"algorithmId"`
	Enabled          bool      `gorm:"column:enabled;not null" json:"enabled"`
	SampleFPS        int       `gorm:"column:sample_fps;not null;default:5" json:"sampleFps"`
	ConfigJSON       string    `gorm:"column:config_json;not null;default:'{}'" json:"configJson"`
	RegionConfigJSON string    `gorm:"column:region_config_json;not null;default:'{}'" json:"regionConfigJson"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName overrides the default table name for TaskAlgorithm.
func (TaskAlgorithm) TableName() string {
	return "task_algorithms"
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

// TaskAlgorithmItem represents an algorithm configuration item in creation or update requests.
type TaskAlgorithmItem struct {
	ID                  string `json:"id,omitempty"`
	AlgorithmID         string `json:"algorithmId"`
	AlgorithmIDAlt      string `json:"algorithm_id,omitempty"`
	Enabled             *bool  `json:"enabled,omitempty"`
	SampleFPS           int    `json:"sampleFps,omitempty"`
	SampleFPSAlt        int    `json:"sample_fps,omitempty"`
	ConfigJSON          string `json:"configJson,omitempty"`
	ConfigJSONAlt       string `json:"config_json,omitempty"`
	RegionConfigJSON    string `json:"regionConfigJson,omitempty"`
	RegionConfigJSONAlt string `json:"region_config_json,omitempty"`
}

// ToEntity converts TaskAlgorithmItem to a TaskAlgorithm entity.
func (item *TaskAlgorithmItem) ToEntity(taskID string) TaskAlgorithm {
	algoID := item.AlgorithmID
	if algoID == "" {
		algoID = item.AlgorithmIDAlt
	}
	fps := item.SampleFPS
	if fps <= 0 {
		fps = item.SampleFPSAlt
	}
	if fps <= 0 {
		fps = DefaultSampleFPS
	}
	cfg := item.ConfigJSON
	if cfg == "" {
		cfg = item.ConfigJSONAlt
	}
	if cfg == "" {
		cfg = "{}"
	}
	regionCfg := item.RegionConfigJSON
	if regionCfg == "" {
		regionCfg = item.RegionConfigJSONAlt
	}
	if regionCfg == "" {
		regionCfg = "{}"
	}
	enabled := true
	if item.Enabled != nil {
		enabled = *item.Enabled
	}
	return TaskAlgorithm{
		ID:               item.ID,
		TaskID:           taskID,
		AlgorithmID:      algoID,
		Enabled:          enabled,
		SampleFPS:        fps,
		ConfigJSON:       cfg,
		RegionConfigJSON: regionCfg,
	}
}

// CreateTaskRequest is the request payload for creating an analysis task.
// Both modern multi-algorithm payloads and legacy single-algorithm payloads are supported.
type CreateTaskRequest struct {
	ID          string              `json:"id,omitempty"`
	CameraID    string              `json:"cameraId"`
	CameraIDAlt string              `json:"camera_id,omitempty"`
	Name        string              `json:"name,omitempty"`
	Enabled     *bool               `json:"enabled,omitempty"`
	Algorithms  []TaskAlgorithmItem `json:"algorithms,omitempty"`
	// Legacy single-algorithm fields for backward compatibility
	AlgorithmID         string `json:"algorithmId,omitempty"`
	AlgorithmIDAlt      string `json:"algorithm_id,omitempty"`
	SampleFPS           int    `json:"sampleFps,omitempty"`
	SampleFPSAlt        int    `json:"sample_fps,omitempty"`
	ConfigJSON          string `json:"configJson,omitempty"`
	ConfigJSONAlt       string `json:"config_json,omitempty"`
	RegionConfigJSON    string `json:"regionConfigJson,omitempty"`
	RegionConfigJSONAlt string `json:"region_config_json,omitempty"`
}

// ToTask converts CreateTaskRequest into a Task entity skeleton,
// resolving camelCase and snake_case alias fields and supporting legacy single-algorithm payloads.
func (req *CreateTaskRequest) ToTask() *Task {
	camID := req.CameraID
	if camID == "" {
		camID = req.CameraIDAlt
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	t := &Task{
		ID:       req.ID,
		CameraID: camID,
		Name:     req.Name,
		Enabled:  enabled,
	}

	items := req.Algorithms
	// Support legacy single-algorithm request
	legacyAlgoID := req.AlgorithmID
	if legacyAlgoID == "" {
		legacyAlgoID = req.AlgorithmIDAlt
	}
	if len(items) == 0 && legacyAlgoID != "" {
		items = []TaskAlgorithmItem{{
			AlgorithmID:         legacyAlgoID,
			Enabled:             req.Enabled,
			SampleFPS:           req.SampleFPS,
			SampleFPSAlt:        req.SampleFPSAlt,
			ConfigJSON:          req.ConfigJSON,
			ConfigJSONAlt:       req.ConfigJSONAlt,
			RegionConfigJSON:    req.RegionConfigJSON,
			RegionConfigJSONAlt: req.RegionConfigJSONAlt,
		}}
	}

	t.Algorithms = make([]TaskAlgorithm, 0, len(items))
	for _, it := range items {
		t.Algorithms = append(t.Algorithms, it.ToEntity(t.ID))
	}

	return t
}

// UpdateTaskRequest is the request payload for updating an analysis task.
type UpdateTaskRequest struct {
	Name       string               `json:"name,omitempty"`
	Enabled    *bool                `json:"enabled,omitempty"`
	Algorithms *[]TaskAlgorithmItem `json:"algorithms,omitempty"`
	// Legacy single-algorithm update fields
	SampleFPS           int    `json:"sampleFps,omitempty"`
	SampleFPSAlt        int    `json:"sample_fps,omitempty"`
	ConfigJSON          string `json:"configJson,omitempty"`
	ConfigJSONAlt       string `json:"config_json,omitempty"`
	RegionConfigJSON    string `json:"regionConfigJson,omitempty"`
	RegionConfigJSONAlt string `json:"region_config_json,omitempty"`
}

// ApplyTo applies the update fields onto the target task entity.
func (req *UpdateTaskRequest) ApplyTo(t *Task) {
	if req.Name != "" {
		t.Name = req.Name
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Algorithms != nil {
		t.Algorithms = make([]TaskAlgorithm, 0, len(*req.Algorithms))
		for _, it := range *req.Algorithms {
			t.Algorithms = append(t.Algorithms, it.ToEntity(t.ID))
		}
	} else if len(t.Algorithms) == 1 {
		// Legacy single-algorithm update fallback
		fps := req.SampleFPS
		if fps <= 0 {
			fps = req.SampleFPSAlt
		}
		if fps > 0 {
			t.Algorithms[0].SampleFPS = fps
		}
		cfg := req.ConfigJSON
		if cfg == "" {
			cfg = req.ConfigJSONAlt
		}
		if cfg != "" {
			t.Algorithms[0].ConfigJSON = cfg
		}
		regionCfg := req.RegionConfigJSON
		if regionCfg == "" {
			regionCfg = req.RegionConfigJSONAlt
		}
		if regionCfg != "" {
			t.Algorithms[0].RegionConfigJSON = regionCfg
		}
	}
}
