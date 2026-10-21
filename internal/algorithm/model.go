package algorithm

import (
	"path/filepath"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/task"
)

// Re-exports from algorunner for backward compatibility and clean cross-domain boundaries.
type (
	PackageMeta                = algorunner.PackageMeta
	LifecycleStore             = algorunner.LifecycleStore
	BrokerSubscriptionResolver = algorunner.BrokerSubscriptionResolver
	GallerySyncProvider        = algorunner.GallerySyncProvider
	GallerySyncItem            = algorunner.GallerySyncItem
	GallerySyncPayload         = algorunner.GallerySyncPayload
	GallerySyncShardPayload    = algorunner.GallerySyncShardPayload
	GalleryAckPayload          = algorunner.GalleryAckPayload
	GalleryUpsertPayload       = algorunner.GalleryUpsertPayload
	GalleryDeletePayload       = algorunner.GalleryDeletePayload
	GalleryBroadcaster         = algorunner.GalleryBroadcaster
	Supervisor                 = algorunner.Supervisor
	RecoveryOptions            = algorunner.RecoveryOptions
)

var (
	NewRecoveryStateMachine    = algorunner.NewRecoveryStateMachine
	DefaultRecoveryOptions     = algorunner.DefaultRecoveryOptions
	NewSupervisor              = algorunner.NewSupervisor
	NewGalleryBroadcaster      = algorunner.NewGalleryBroadcaster
	ErrAlgorithmNotInCrashLoop = algorunner.ErrAlgorithmNotInCrashLoop
	ErrAlgorithmAlreadyRunning = algorunner.ErrAlgorithmAlreadyRunning
	ErrNoBrokerSockets         = algorunner.ErrNoBrokerSockets
)

// Algorithm status enum
const (
	StatusActive      = "ACTIVE"
	StatusDisabled    = "DISABLED"
	StatusStarting    = "STARTING"
	StatusRunning     = "RUNNING"
	StatusCrashed     = "CRASHED"
	StatusCrashLoop   = "CRASH_LOOP"
	StatusRollingBack = "ROLLING_BACK"
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

// InstallDir returns the install directory of the algorithm given base algorithms directory.
func (a *Algorithm) InstallDir(algoDir string) string {
	return filepath.Join(algoDir, a.ID, a.Version)
}

// EntryPath returns the convention path to plugin.so.
func (a *Algorithm) EntryPath(algoDir string) string {
	return filepath.Join(a.InstallDir(algoDir), "plugin.so")
}

// SamplePath returns the convention path to sample.jpg.
func (a *Algorithm) SamplePath(algoDir string) string {
	return filepath.Join(a.InstallDir(algoDir), "sample.jpg")
}

// EnvPath returns the convention path to optional .env.
func (a *Algorithm) EnvPath(algoDir string) string {
	return filepath.Join(a.InstallDir(algoDir), ".env")
}

// ModelsDir returns the convention path to models/.
func (a *Algorithm) ModelsDir(algoDir string) string {
	return filepath.Join(a.InstallDir(algoDir), "models")
}

// PreviousInstallDir returns the directory of the previous version if available.
func (a *Algorithm) PreviousInstallDir(algoDir string) string {
	if a.PreviousVersion == "" {
		return ""
	}
	return filepath.Join(algoDir, a.ID, a.PreviousVersion)
}

// PreviousEntryPath returns the entry path of the previous version if available.
func (a *Algorithm) PreviousEntryPath(algoDir string) string {
	if a.PreviousVersion == "" {
		return ""
	}
	return filepath.Join(a.PreviousInstallDir(algoDir), "plugin.so")
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
