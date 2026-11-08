package task

import (
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
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
	CropJSON   string          `gorm:"column:crop_json;not null;default:'{}'" json:"cropJson"`
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
// 负数与超过上限都是调用方的输入错误，直接拒绝而不做静默纠正；
// 0 由调用方按默认值收敛。
func ValidateSampleFPS(raw int) (uint32, error) {
	if raw < 0 || raw > SampleFPSMax {
		return 0, apperr.Invalid(
			"INVALID_TASK_CONFIG",
			fmt.Sprintf("sample_fps %d out of range [0, %d]", raw, SampleFPSMax),
			errInvalidTaskConfig,
		)
	}
	return uint32(raw), nil
}
