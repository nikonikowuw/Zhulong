package gb28181

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// CameraCreator encapsulates the creation of a camera entity from a GB28181 channel.
type CameraCreator interface {
	CreateGB28181Camera(ctx context.Context, params camera.CreateGB28181CameraParams) (*camera.CameraResponse, error)
}

// CameraDeleter allows rollback if channel binding fails after camera creation.
type CameraDeleter interface {
	Delete(ctx context.Context, id string) error
}

// CatalogSyncer triggers a catalog synchronization for an online device.
type CatalogSyncer interface {
	Sync(ctx context.Context, device Device) ([]Channel, error)
}

// Service owns the rules that must not be duplicated between the HTTP layer and
// the lifecycle: normalize → validate → resolve the advertised address → persist.
//
// Saving never touches the running listener. That is the whole restart-required
// contract, so no method here has a side effect on sockets.
type Service struct {
	configs       ConfigRepository
	devices       DeviceStore
	channels      ChannelStore
	catalog       CatalogSyncer
	cameraCreator CameraCreator
	cameraDeleter CameraDeleter
	runtime       *Runtime
	addrs         InterfaceAddrs
	logger        *zap.Logger
	// syncGroup collapses concurrent catalog queries for the same device: a second
	// click must join the running exchange instead of sending a second SN.
	syncGroup singleflight.Group
	// bindGroup collapses concurrent "add as camera" calls for the same channel. The
	// camera_id check below is a read-then-write, so without this two parallel POSTs
	// create two cameras and leave one unbound.
	bindGroup singleflight.Group
}

// ServiceOptions contains the dependencies used to construct a Service.
type ServiceOptions struct {
	Configs       ConfigRepository
	Devices       DeviceStore
	Channels      ChannelStore
	Catalog       CatalogSyncer
	CameraCreator CameraCreator
	CameraDeleter CameraDeleter
	Runtime       *Runtime
	Addrs         InterfaceAddrs
	Logger        *zap.Logger
}

// NewService wires the service. It performs no I/O.
func NewService(options ServiceOptions) *Service {
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}
	if options.Addrs == nil {
		options.Addrs = DefaultInterfaceAddrs
	}
	return &Service{
		configs:       options.Configs,
		devices:       options.Devices,
		channels:      options.Channels,
		catalog:       options.Catalog,
		cameraCreator: options.CameraCreator,
		cameraDeleter: options.CameraDeleter,
		runtime:       options.Runtime,
		addrs:         options.Addrs,
		logger:        options.Logger,
	}
}

// GetConfig returns the persisted target plus the running view. The target is read
// back from the database rather than from the runtime cache, so an out-of-band
// change is visible as restartRequired instead of being hidden.
func (s *Service) GetConfig(ctx context.Context) (ConfigResponse, error) {
	stored, err := s.configs.GetConfig(ctx)
	if err != nil {
		return ConfigResponse{}, storeFailure(err, "Failed to read GB28181 configuration")
	}
	desired := stored.Normalize()
	return s.configResponse(desired), nil
}

// configResponse builds the GET/PUT payload for a persisted target configuration: the
// target itself plus the running view, so both endpoints answer with one shape.
func (s *Service) configResponse(desired Config) ConfigResponse {
	return ConfigResponse{
		Config:  toConfigInfo(desired),
		Runtime: toRuntimeInfo(s.runtime.StatusAgainst(desired)),
	}
}

// UpdateConfig validates the whole configuration and persists it atomically.
//
// When GB28181 is enabled and no advertised address was supplied, a unique
// non-loopback IPv4 is inferred. This assumes no NAT; NAT deployments must set an
// explicit reachable address. Resolving at save time prevents an unbootable target
// config from being persisted.
func (s *Service) UpdateConfig(ctx context.Context, cfg Config) (ConfigResponse, error) {
	target := cfg.Normalize()
	if err := target.Validate(); err != nil {
		return ConfigResponse{}, err
	}
	if target.Enabled && target.MediaAdvertisedIP == "" {
		candidates, err := s.addrs()
		if err != nil {
			return ConfigResponse{}, apperr.Internal(CodeAddressProbeFailed,
				"Failed to enumerate host addresses", err)
		}
		resolved, err := resolveAdvertisedIP(target.MediaBindAddress, "", candidates)
		if err != nil {
			return ConfigResponse{}, err
		}
		target.MediaAdvertisedIP = resolved
	}

	saved, err := s.configs.SaveConfig(ctx, target)
	if err != nil {
		return ConfigResponse{}, storeFailure(err, "Failed to save GB28181 configuration")
	}
	persisted := saved.Normalize()
	s.runtime.SetDesired(persisted)

	return s.configResponse(persisted), nil
}

// UpsertDevice pre-provisions one device. The plaintext password is encrypted
// inside the store and never returned, logged, or audited.
func (s *Service) UpsertDevice(ctx context.Context, upd DeviceUpsert) (*Device, error) {
	device, err := s.devices.UpsertDevice(ctx, upd)
	if err != nil {
		// Validation errors already carry their stable code; a raw store error must
		// not surface as an anonymous 500.
		return nil, storeFailure(err, "Failed to save GB28181 device")
	}
	s.refreshDeviceCounters(ctx)
	return device, nil
}

// ListDevices returns the allowlist with secret material stripped. It has no side
// effect: counters are refreshed by writes and by Snapshot, so a read-only table
// view cannot change what the runtime endpoint reports.
func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	devices, err := s.devices.ListDevices(ctx)
	if err != nil {
		return nil, storeFailure(err, "Failed to list GB28181 devices")
	}
	return devices, nil
}

// Snapshot returns the observability view. Counters are refreshed from the store
// first so a polling dashboard never shows stale device totals.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	s.refreshDeviceCounters(ctx)
	return s.runtime.Snapshot(), nil
}

// refreshDeviceCounters is best-effort: the snapshot is diagnostics, so a failed
// count must not turn a successful device write into an error.
func (s *Service) refreshDeviceCounters(ctx context.Context) {
	publishDeviceCounters(ctx, s.devices, s.runtime, s.logger)
}

// SyncCatalog queries the device for its video channels and reconciles the catalog.
func (s *Service) SyncCatalog(ctx context.Context, deviceID string) (*SyncCatalogResponse, error) {
	device, err := s.devices.GetDevice(ctx, deviceID)
	if err != nil {
		return nil, storeFailure(err, "Failed to get GB28181 device")
	}
	if device.State != DeviceStateOnline {
		return nil, apperr.Invalid(CodeDeviceOffline, "GB28181 device is offline", nil)
	}
	if s.catalog == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "catalog syncer is not ready", nil)
	}

	s.recordCatalogAttempt()
	res, err, _ := s.syncGroup.Do(deviceID, func() (any, error) {
		// The shared body must not inherit the first caller's cancellation: closing the
		// browser tab must not abort an exchange that other callers are waiting on.
		// CatalogManager owns the deadline, so dropping the cancel signal lengthens
		// nothing.
		syncCtx := context.WithoutCancel(ctx)
		channels, err := s.catalog.Sync(syncCtx, *device)
		if err != nil {
			return nil, err
		}
		return &SyncCatalogResponse{DeviceID: deviceID, Total: len(channels)}, nil
	})
	if err != nil {
		s.recordCatalogFailure(err)
		return nil, err
	}
	response := res.(*SyncCatalogResponse)
	s.recordCatalogSuccess(response.Total)
	return response, nil
}

// ListChannels returns all imported channels for a device.
func (s *Service) ListChannels(ctx context.Context, deviceID string) (*ChannelListResponse, error) {
	if s.channels == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "channel store is not ready", nil)
	}
	channels, err := s.channels.ListChannels(ctx, deviceID)
	if err != nil {
		return nil, storeFailure(err, "Failed to list GB28181 channels")
	}
	items := make([]ChannelResponse, len(channels))
	for i, ch := range channels {
		items[i] = toChannelResponse(ch)
	}
	return &ChannelListResponse{Items: items}, nil
}

// AddChannelAsCamera explicitly adds an imported channel as a system camera entity.
func (s *Service) AddChannelAsCamera(ctx context.Context, deviceID, channelID string) (*camera.CameraResponse, error) {
	deviceID = strings.TrimSpace(deviceID)
	channelID = strings.TrimSpace(channelID)
	// Validate before touching the store: both codes are also the camera URI contract, so
	// a malformed one must fail here with a GB28181 code rather than inside camera.
	if !camera.IsGB28181ID(deviceID) {
		return nil, apperr.Invalid(CodeDeviceIDInvalid, "deviceId must be 20 digits", nil)
	}
	if !camera.IsGB28181ID(channelID) {
		return nil, apperr.Invalid(CodeChannelIDInvalid, "channelId must be 20 digits", nil)
	}
	if s.channels == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "channel store is not ready", nil)
	}
	if s.cameraCreator == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "camera creator is not ready", nil)
	}

	res, err, _ := s.bindGroup.Do(deviceID+"/"+channelID, func() (any, error) {
		return s.addChannelAsCamera(ctx, deviceID, channelID)
	})
	if err != nil {
		return nil, err
	}
	return res.(*camera.CameraResponse), nil
}

// addChannelAsCamera performs the read-then-write binding. Callers must go through
// AddChannelAsCamera so the pair is serialized per channel.
func (s *Service) addChannelAsCamera(ctx context.Context, deviceID, channelID string) (*camera.CameraResponse, error) {
	ch, err := s.channels.GetChannel(ctx, deviceID, channelID)
	if err != nil {
		return nil, storeFailure(err, "Failed to get GB28181 channel")
	}
	if ch.CameraID != nil && *ch.CameraID != "" {
		return nil, apperr.Conflict(CodeChannelAlreadyBound, "channel is already added as a camera", nil)
	}

	name := ch.Name
	if strings.TrimSpace(name) == "" {
		name = channelID
	}
	width, height := 0, 0
	if ch.ResolutionWidth != nil {
		width = *ch.ResolutionWidth
	}
	if ch.ResolutionHeight != nil {
		height = *ch.ResolutionHeight
	}

	camResp, err := s.cameraCreator.CreateGB28181Camera(ctx, camera.CreateGB28181CameraParams{
		DeviceID:         deviceID,
		ChannelID:        channelID,
		Name:             name,
		ResolutionWidth:  width,
		ResolutionHeight: height,
	})
	if err != nil {
		return nil, err
	}

	if err := s.channels.BindChannelCamera(ctx, deviceID, channelID, camResp.ID); err != nil {
		s.rollbackCamera(camResp.ID)
		return nil, storeFailure(err, "Failed to bind channel to camera record")
	}
	return camResp, nil
}

// rollbackCamera deletes a camera created for a binding that then failed. The request
// context may already be cancelled (client gone), which is exactly when the rollback
// still has to run, so it gets its own bounded context instead of the caller's.
func (s *Service) rollbackCamera(cameraID string) {
	if s.cameraDeleter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
	defer cancel()
	if err := s.cameraDeleter.Delete(ctx, cameraID); err != nil {
		// A failed rollback leaves an orphan camera, which the operator can delete; a
		// panic or a silent success claim would be worse. Log it and move on.
		s.logger.Error("failed to roll back camera created for gb28181 channel",
			zap.String("camera_id", cameraID),
			zap.Error(err),
		)
	}
}

// recordCatalogAttempt counts an operator-triggered catalog query.
func (s *Service) recordCatalogAttempt() {
	if s.runtime != nil {
		s.runtime.RecordCatalogAttempt()
	}
}

// recordCatalogSuccess records a completed sync.
func (s *Service) recordCatalogSuccess(channels int) {
	if s.runtime != nil {
		s.runtime.RecordCatalogSuccess(channels)
	}
}

// recordCatalogFailure records a failed sync, distinguishing a timeout from an
// exchange that failed outright.
func (s *Service) recordCatalogFailure(err error) {
	if s.runtime == nil {
		return
	}
	var appErr *apperr.Error
	timeout := errors.As(err, &appErr) && appErr.Code == CodeCatalogTimeout
	s.runtime.RecordCatalogFailure(timeout)
}

// DeleteDevice removes a pre-provisioned device and the catalog it reported. A device
// whose channels are bound to cameras is refused by the store: the operator must decide
// what happens to those cameras first.
func (s *Service) DeleteDevice(ctx context.Context, deviceID string) error {
	if err := s.devices.DeleteDevice(ctx, deviceID); err != nil {
		return storeFailure(err, "Failed to delete GB28181 device")
	}
	s.refreshDeviceCounters(ctx)
	return nil
}

// storeFailure wraps an unexpected repository error under the shared
// DATABASE_ERROR code (HTTP 500) while passing domain errors through untouched.
// Reusing a validation code here would report a working configuration as invalid.
func storeFailure(err error, message string) error {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		return err
	}
	return apperr.Internal(CodeDatabaseError, message, err)
}
