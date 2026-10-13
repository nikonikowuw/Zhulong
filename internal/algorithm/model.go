package algorithm

import (
	"path/filepath"
	"time"
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

// Task type enum
const (
	TaskTypeDetection       = "detection"
	TaskTypeClassification  = "classification"
	TaskTypePersonStructure = "person_structure"
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

// AnalysisTask represents a video analysis task associated with a camera and algorithm.
type AnalysisTask struct {
	ID          string    `gorm:"primaryKey;column:id" json:"id"`
	CameraID    string    `gorm:"column:camera_id;not null;index" json:"cameraId"`
	AlgorithmID string    `gorm:"column:algorithm_id;not null;index" json:"algorithmId"`
	Enabled     bool      `gorm:"column:enabled;not null;default:true" json:"enabled"`
	SampleFPS   int       `gorm:"column:sample_fps;not null;default:5" json:"sampleFps"`
	ConfigJSON  string    `gorm:"column:config_json;not null;default:'{}'" json:"configJson"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName overrides the default table name for AnalysisTask.
func (AnalysisTask) TableName() string {
	return "analysis_tasks"
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
