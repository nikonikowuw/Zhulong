package algorunner

import "context"

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
	ID                string
	Name              string
	Version           string
	PreviousVersion   string
	Status            string
	DisableMotionGate bool
}

// LifecycleStore provides package lookup and status updates for the supervisor.
type LifecycleStore interface {
	GetAlgorithmPackage(ctx context.Context, id string) (PackageMeta, error)
	UpdateAlgorithmStatus(ctx context.Context, id string, status string) error
	RollbackAlgorithmVersion(ctx context.Context, id string, targetVersion, failedVersion string) error
}

// BrokerSubscriptionResolver resolves the Broker Socket paths the given package
// must subscribe to now, and syncs those subscriptions (which AI stream is kept
// alive for which package) onto the package's current task set.
//
// ⚠️ The name deliberately avoids Resolve*: this does not only query, it also
// establishes and tears down subscriptions on the implementation side. An
// implementation must be idempotent and must not leave half-committed
// subscriptions behind on failure.
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
