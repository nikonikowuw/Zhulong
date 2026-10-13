package mediafile

import (
	"context"

	"github.com/nikonikowuw/Zhulong/internal/storage"
)

// MediaRootProvider resolves the currently active media root directory.
//
// The media root has a single source of truth: the storage configuration. This
// service must never keep its own copy of the path, otherwise a hot switch in
// storage would leave media reads pinned to the previous disk.
type MediaRootProvider interface {
	MediaRoot(ctx context.Context) (string, error)
}

// DeviceInspector exposes the filesystem telemetry this service needs to detect
// an external disk that dropped off and left its mount point resolving to the
// system root filesystem.
//
// It is deliberately narrower than storage.PathInspector: read-only media
// transfer has no reason to probe writability or scan usage breakdowns.
type DeviceInspector interface {
	InspectFS(path string) (*storage.FSStats, error)
	GetRootDeviceID() (uint64, error)
}
