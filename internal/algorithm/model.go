package algorithm

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/task"
)

// Lifecycle statuses are owned by algorunner, which drives the recovery state
// machine and writes them to the database. Duplicating the literals here would
// create a second source of truth for values that must match exactly.
const (
	StatusActive      = algorunner.StatusActive
	StatusDisabled    = algorunner.StatusDisabled
	StatusStarting    = algorunner.StatusStarting
	StatusRunning     = algorunner.StatusRunning
	StatusCrashed     = algorunner.StatusCrashed
	StatusCrashLoop   = algorunner.StatusCrashLoop
	StatusRollingBack = algorunner.StatusRollingBack
)

// Audit actions for algorithm-domain operations. Namespaced as
// <domain>.<resource>.<verb> to match the other modules: audit queries match on
// the exact action string, so a bare literal cannot be grouped or filtered.
const (
	ActionAlgorithmInstall    = "algorithm.install"
	ActionAlgorithmEnable     = "algorithm.enable"
	ActionAlgorithmDisable    = "algorithm.disable"
	ActionAlgorithmReset      = "algorithm.reset"
	ActionAlgorithmDelete     = "algorithm.delete"
	ActionAlgorithmTaskCreate = "algorithm.task.create"
	ActionAlgorithmTaskDelete = "algorithm.task.delete"
)

// Task type enum
const (
	TaskTypeDetection        = "detection"
	TaskTypeClassification   = "classification"
	TaskTypePersonStructure  = "person_structure"
	TaskTypeVehicleStructure = "vehicle_structure"
)

// Category enum
const (
	CategorySafety   = "safety"
	CategorySecurity = "security"
	CategoryFire     = "fire"
	CategoryTraffic  = "traffic"
	CategoryGeneral  = "general"
)

// Algorithm represents an installed computer vision algorithm.
// Paths are derived via conventions:
// - entry: plugin.so
// - sample: sample.jpg
// - models: models/
// - env: .env
type Algorithm struct {
	ID               string    `gorm:"primaryKey;column:id" json:"id"`
	Name             string    `gorm:"column:name;uniqueIndex;not null" json:"name"`
	Version          string    `gorm:"column:version;not null" json:"version"`
	TaskType         string    `gorm:"column:task_type;not null" json:"taskType"`
	Category         string    `gorm:"column:category;not null" json:"category"`
	TargetArch       string    `gorm:"column:target_arch;not null" json:"targetArch"`
	TargetDevice     string    `gorm:"column:target_device;not null" json:"targetDevice"`
	ComputeCost      int       `gorm:"column:compute_cost;not null;default:10" json:"computeCost"`
	MemoryMB         int       `gorm:"column:memory_mb;not null;default:64" json:"memoryMb"`
	Description      string    `gorm:"column:description;not null;default:''" json:"description"`
	ManifestJSON     string    `gorm:"column:manifest_json;not null;default:'{}'" json:"manifestJson"`
	ConfigSchemaJSON string    `gorm:"column:config_schema_json;not null;default:'{}'" json:"configSchemaJson"`
	Status           string    `gorm:"column:status;not null;default:'DISABLED'" json:"status"`
	PreviousVersion  string    `gorm:"column:previous_version;not null;default:''" json:"previousVersion"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// ToPackageMeta converts an Algorithm domain model to a runner-facing PackageMeta.
func (a *Algorithm) ToPackageMeta() algorunner.PackageMeta {
	if a == nil {
		return algorunner.PackageMeta{}
	}
	return algorunner.PackageMeta{
		ID:              a.ID,
		Name:            a.Name,
		Version:         a.Version,
		PreviousVersion: a.PreviousVersion,
		Status:          a.Status,
	}
}

// TableName overrides the default table name for Algorithm.
func (Algorithm) TableName() string {
	return "algorithms"
}

// AnalysisTask is an alias to task.Task for backward compatibility.
// Deprecated: use task.Task from github.com/nikonikowuw/Zhulong/internal/task instead.
type AnalysisTask = task.Task

// SampleFPSMax 是单路分析任务的采样帧率上限。
const SampleFPSMax = task.SampleFPSMax

// ValidateSampleFPS 校验并收敛分析任务的采样帧率。
func ValidateSampleFPS(raw int) (uint32, error) {
	return task.ValidateSampleFPS(raw)
}

// ManifestPlatform describes the target hardware requirement in manifest.json.
type ManifestPlatform struct {
	Arch   string `json:"arch"`
	Target string `json:"target"`
}

// ManifestResources describes compute and memory budget in manifest.json.
type ManifestResources struct {
	ComputeCost int `json:"compute_cost"`
	MemoryMB    int `json:"memory_mb"`
}

// Manifest represents the metadata declaration in manifest.json.
type Manifest struct {
	ID          string            `json:"id"`
	Version     string            `json:"version"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	TaskType    string            `json:"task_type"`
	Category    string            `json:"category"`
	Platform    ManifestPlatform  `json:"platform"`
	Resources   ManifestResources `json:"resources"`
}
