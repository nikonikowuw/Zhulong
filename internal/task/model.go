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

// SampleFPSMax 是单路分析任务的采样帧率上限。
const SampleFPSMax = 1000

// DefaultSampleFPS 是单路分析任务的默认采样帧率。
const DefaultSampleFPS = 5

// defaultConfigJSON 是未指定算法参数与区域策略时的空 JSON 文档。
const defaultConfigJSON = "{}"

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
	ScheduleJSON     string    `gorm:"column:schedule_json;not null;default:'{}'" json:"scheduleJson"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName overrides the default table name for TaskAlgorithm.
func (TaskAlgorithm) TableName() string {
	return "task_algorithms"
}

// ValidateSampleFPS 校验分析任务的采样帧率。
//
// 负数与超过上限都是调用方的输入错误，直接拒绝而不做静默纠正；0 由调用方按默认值收敛。
func ValidateSampleFPS(raw int) (uint32, error) {
	if raw < 0 || raw > SampleFPSMax {
		return 0, apperr.Invalid(
			"INVALID_TASK_CONFIG",
			fmt.Sprintf("sample_fps %d out of range [0, %d]", raw, SampleFPSMax),
			ErrInvalidTaskConfig,
		)
	}
	return uint32(raw), nil
}

// TaskAlgorithmItem represents an algorithm configuration item in creation or update requests.
//
// 同时接受 camelCase 与 snake_case 两种键名（历史客户端兼容）；camelCase 优先。
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
	ScheduleJSON        string `json:"scheduleJson,omitempty"`
	ScheduleJSONAlt     string `json:"schedule_json,omitempty"`
}

// ToEntity converts TaskAlgorithmItem to a TaskAlgorithm entity.
//
// 负数 sampleFps 不会被吞成默认值，交由 Service 的 ValidateSampleFPS 拒绝。
func (item TaskAlgorithmItem) ToEntity(taskID string) TaskAlgorithm {
	enabled := true
	if item.Enabled != nil {
		enabled = *item.Enabled
	}
	return TaskAlgorithm{
		ID:               item.ID,
		TaskID:           taskID,
		AlgorithmID:      firstNonZero(item.AlgorithmID, item.AlgorithmIDAlt),
		Enabled:          enabled,
		SampleFPS:        firstNonZero(item.SampleFPS, item.SampleFPSAlt, DefaultSampleFPS),
		ConfigJSON:       firstNonZero(item.ConfigJSON, item.ConfigJSONAlt, defaultConfigJSON),
		RegionConfigJSON: firstNonZero(item.RegionConfigJSON, item.RegionConfigJSONAlt, defaultConfigJSON),
		ScheduleJSON:     firstNonZero(item.ScheduleJSON, item.ScheduleJSONAlt, defaultConfigJSON),
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
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	t := &Task{
		ID:       req.ID,
		CameraID: firstNonZero(req.CameraID, req.CameraIDAlt),
		Name:     req.Name,
		Enabled:  enabled,
	}

	items := req.Algorithms
	if legacy := req.legacyAlgorithmItem(); len(items) == 0 && legacy != nil {
		items = []TaskAlgorithmItem{*legacy}
	}

	t.Algorithms = make([]TaskAlgorithm, 0, len(items))
	for _, item := range items {
		t.Algorithms = append(t.Algorithms, item.ToEntity(t.ID))
	}
	return t
}

// legacyAlgorithmItem 把旧的扁平单算法字段折成一个算法项；没有 legacy 字段时返回 nil。
func (req *CreateTaskRequest) legacyAlgorithmItem() *TaskAlgorithmItem {
	algorithmID := firstNonZero(req.AlgorithmID, req.AlgorithmIDAlt)
	if algorithmID == "" {
		return nil
	}
	return &TaskAlgorithmItem{
		AlgorithmID:         algorithmID,
		Enabled:             req.Enabled,
		SampleFPS:           req.SampleFPS,
		SampleFPSAlt:        req.SampleFPSAlt,
		ConfigJSON:          req.ConfigJSON,
		ConfigJSONAlt:       req.ConfigJSONAlt,
		RegionConfigJSON:    req.RegionConfigJSON,
		RegionConfigJSONAlt: req.RegionConfigJSONAlt,
	}
}

// UpdateTaskRequest is the request payload for updating an analysis task.
type UpdateTaskRequest struct {
	Name       *string              `json:"name,omitempty"`
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
	if t == nil {
		return
	}
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Algorithms != nil {
		t.Algorithms = make([]TaskAlgorithm, 0, len(*req.Algorithms))
		for _, item := range *req.Algorithms {
			t.Algorithms = append(t.Algorithms, item.ToEntity(t.ID))
		}
		return
	}
	// 兼容旧的扁平单算法字段：只在任务恰好挂了一个算法子项时生效。
	if len(t.Algorithms) == 1 {
		req.applyLegacyFields(&t.Algorithms[0])
	}
}

// applyLegacyFields 把扁平单算法字段合并进既有子项，只覆盖调用方显式给出的字段。
func (req *UpdateTaskRequest) applyLegacyFields(sub *TaskAlgorithm) {
	if fps := firstNonZero(req.SampleFPS, req.SampleFPSAlt); fps != 0 {
		sub.SampleFPS = fps
	}
	if cfg := firstNonZero(req.ConfigJSON, req.ConfigJSONAlt); cfg != "" {
		sub.ConfigJSON = cfg
	}
	if regionCfg := firstNonZero(req.RegionConfigJSON, req.RegionConfigJSONAlt); regionCfg != "" {
		sub.RegionConfigJSON = regionCfg
	}
}

// firstNonZero 返回候选值里第一个非零值；全为零时返回零值。
//
// 用于 camelCase / snake_case 别名兜底，以及"未提供则取默认值"的场景。
func firstNonZero[T comparable](candidates ...T) T {
	var zero T
	for _, candidate := range candidates {
		if candidate != zero {
			return candidate
		}
	}
	return zero
}
