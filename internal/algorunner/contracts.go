package algorunner

import (
	"context"
	"path/filepath"
)

// Lifecycle statuses for algorithm packages and runtime processes.
const (
	StatusActive      = "ACTIVE"
	StatusDisabled    = "DISABLED"
	StatusStarting    = "STARTING"
	StatusRunning     = "RUNNING"
	StatusCrashed     = "CRASHED"
	StatusCrashLoop   = "CRASH_LOOP"
	StatusRollingBack = "ROLLING_BACK"
)

// PackageMeta represents the minimal algorithm package metadata needed by the runner supervisor.
type PackageMeta struct {
	ID              string
	Name            string
	Version         string
	PreviousVersion string
	Status          string
}

// InstallDir returns the install directory of the algorithm given base algorithms directory.
func (p PackageMeta) InstallDir(algoDir string) string {
	return filepath.Join(algoDir, p.ID, p.Version)
}

// EntryPath returns the convention path to plugin.so.
func (p PackageMeta) EntryPath(algoDir string) string {
	return filepath.Join(p.InstallDir(algoDir), "plugin.so")
}

// EnvPath returns the convention path to optional .env.
func (p PackageMeta) EnvPath(algoDir string) string {
	return filepath.Join(p.InstallDir(algoDir), ".env")
}

// PreviousInstallDir returns the directory of the previous version if available.
func (p PackageMeta) PreviousInstallDir(algoDir string) string {
	if p.PreviousVersion == "" {
		return ""
	}
	return filepath.Join(algoDir, p.ID, p.PreviousVersion)
}

// PreviousEntryPath returns the entry path of the previous version if available.
func (p PackageMeta) PreviousEntryPath(algoDir string) string {
	if p.PreviousVersion == "" {
		return ""
	}
	return filepath.Join(p.PreviousInstallDir(algoDir), "plugin.so")
}

// LifecycleStore provides package lookup and status updates for the supervisor.
type LifecycleStore interface {
	GetAlgorithmPackage(ctx context.Context, id string) (PackageMeta, error)
	UpdateAlgorithmStatus(ctx context.Context, id string, status string) error
	RollbackAlgorithmVersion(ctx context.Context, id string, version, previousVersion string) error
}

// BrokerSubscriptionResolver 解析指定算法包当前应当订阅的 Broker Socket 路径列表，
// 并把订阅关系（哪一路 AI 流因哪个包而保活）同步到该算法包的最新任务集合。
//
// ⚠️ 命名刻意不用 Resolve*：它不只是查询，还会在实现侧建立/撤销订阅关系。
// 实现方必须是幂等的，并保证失败时不留下半提交的订阅。
type BrokerSubscriptionResolver interface {
	SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error)
}

// AIMessageBroadcaster sends down-link control messages to runner subprocesses via Native Engine FrameBroker.
type AIMessageBroadcaster interface {
	BroadcastAIMessage(ctx context.Context, streamID uint64, msgType uint16, payload []byte) error
}

// GallerySyncItem represents one registered face item in MSG_GALLERY_SYNC.
type GallerySyncItem struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}

// GallerySyncProvider supplies all enrolled face templates for runner cold-start synchronization.
type GallerySyncProvider interface {
	GetGallerySyncData(ctx context.Context) ([]GallerySyncItem, error)
}
