package mediafile

import (
	"context"

	"github.com/nikonikowuw/Zhulong/internal/storage"
)

// storageMediaRootProvider reads the active media root from the storage
// configuration, which is the single source of truth for the path.
//
// It deliberately reads the persisted configuration on every call rather than
// caching: storage.Service.UpdateConfig hot-switches the directory, and a cache
// here would make the media service serve bytes from the previous disk after an
// operator changed the path.
type storageMediaRootProvider struct {
	service *storage.Service
}

// NewMediaRootProvider adapts the storage service to the MediaRootProvider contract.
func NewMediaRootProvider(service *storage.Service) MediaRootProvider {
	return &storageMediaRootProvider{service: service}
}

func (p *storageMediaRootProvider) MediaRoot(ctx context.Context) (string, error) {
	cfg, err := p.service.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	return cfg.MediaDirectory, nil
}

// storageDeviceInspector adapts storage.PathInspector to the narrower
// DeviceInspector contract used by the resolver.
type storageDeviceInspector struct {
	inspector storage.PathInspector
}

// NewDeviceInspector adapts a storage path inspector to DeviceInspector.
func NewDeviceInspector(inspector storage.PathInspector) DeviceInspector {
	return &storageDeviceInspector{inspector: inspector}
}

func (d *storageDeviceInspector) InspectFS(path string) (*storage.FSStats, error) {
	return d.inspector.InspectFS(path)
}

func (d *storageDeviceInspector) GetRootDeviceID() (uint64, error) {
	return d.inspector.GetRootDeviceID()
}
