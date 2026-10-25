package gb28181

import (
	"context"
	"errors"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// Service owns the rules that must not be duplicated between the HTTP layer and
// the lifecycle: normalize → validate → resolve the advertised address → persist.
//
// Saving never touches the running listener. That is the whole restart-required
// contract, so no method here has a side effect on sockets.
type Service struct {
	configs ConfigRepository
	devices DeviceStore
	runtime *Runtime
	addrs   InterfaceAddrs
	logger  *zap.Logger
}

// ServiceOptions contains the dependencies used to construct a Service.
type ServiceOptions struct {
	Configs ConfigRepository
	Devices DeviceStore
	Runtime *Runtime
	Addrs   InterfaceAddrs
	Logger  *zap.Logger
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
		configs: options.Configs,
		devices: options.Devices,
		runtime: options.Runtime,
		addrs:   options.Addrs,
		logger:  options.Logger,
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
