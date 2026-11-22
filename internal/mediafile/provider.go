package mediafile

import (
	"context"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
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

// NewDeliveryModeProvider reads the configured HTTP delivery mode ("proxy" or "redirect")
// from storage.Service without caching stale config across hot updates.
func NewDeliveryModeProvider(service *storage.Service) DeliveryModeProvider {
	return &storageDeliveryModeProvider{service: service}
}

type storageDeliveryModeProvider struct {
	service *storage.Service
}

func (p *storageDeliveryModeProvider) DeliveryMode(ctx context.Context) string {
	if p == nil || p.service == nil {
		return storage.DeliveryModeProxy
	}
	cfg, err := p.service.GetConfig(ctx)
	if err != nil || cfg == nil || cfg.Backend != storage.BackendS3 || cfg.DeliveryMode == "" {
		return storage.DeliveryModeProxy
	}
	return cfg.DeliveryMode
}

// storageDeviceInspector adapts storage.PathInspector to the narrower
// DeviceInspector contract used by LocalDriver mount telemetry checks.
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

type deviceInspectorMountAdapter struct {
	inspector DeviceInspector
}

func (a *deviceInspectorMountAdapter) InspectMount(path string) (filestore.MountTelemetry, error) {
	stats, err := a.inspector.InspectFS(path)
	if err != nil {
		return filestore.MountTelemetry{}, err
	}
	return filestore.MountTelemetry{
		DeviceID:   stats.DeviceID,
		MountPoint: stats.MountPoint,
		FSType:     stats.FSType,
		IsReadOnly: stats.IsReadOnly,
	}, nil
}

func (a *deviceInspectorMountAdapter) RootDeviceID() (uint64, error) {
	return a.inspector.GetRootDeviceID()
}
