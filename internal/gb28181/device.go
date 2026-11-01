package gb28181

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Registration state machine defaults. A GB28181 device re-REGISTERs at half its
// lease and sends a keepalive every 60s; three missed keepalives is the point where
// the link is considered down even though the lease may still be valid.
const (
	defaultKeepaliveTimeout    = 180 * time.Second
	defaultRegistrationGrace   = 15 * time.Second
	defaultDeviceSweepInterval = 30 * time.Second
)

// deviceStateMachine owns the online/offline transitions of the allowlist and keeps
// the runtime counters in step with the database.
//
// It exists so the SIP layer never writes device state directly: the ingress parses
// requests, this decides what a request means for the persisted session.
type deviceStateMachine struct {
	store   *Store
	runtime *Runtime
	logger  *zap.Logger

	now               func() time.Time
	keepaliveTimeout  time.Duration
	registrationGrace time.Duration
	sweepInterval     time.Duration

	// states caches the last observed state per device so logs and counters report
	// transitions instead of every keepalive. It is bounded by the allowlist.
	mu     sync.Mutex
	states map[string]string
}

// deviceStateOptions configures the state machine. Zero values fall back to the
// documented defaults.
type deviceStateOptions struct {
	Store             *Store
	Runtime           *Runtime
	Logger            *zap.Logger
	Now               func() time.Time
	KeepaliveTimeout  time.Duration
	RegistrationGrace time.Duration
	SweepInterval     time.Duration
}

// newDeviceStateMachine wires the state machine. It performs no I/O.
func newDeviceStateMachine(options deviceStateOptions) *deviceStateMachine {
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.KeepaliveTimeout <= 0 {
		options.KeepaliveTimeout = defaultKeepaliveTimeout
	}
	if options.RegistrationGrace <= 0 {
		options.RegistrationGrace = defaultRegistrationGrace
	}
	if options.SweepInterval <= 0 {
		options.SweepInterval = defaultDeviceSweepInterval
	}
	return &deviceStateMachine{
		store:             options.Store,
		runtime:           options.Runtime,
		logger:            options.Logger,
		now:               options.Now,
		keepaliveTimeout:  options.KeepaliveTimeout,
		registrationGrace: options.RegistrationGrace,
		sweepInterval:     options.SweepInterval,
		states:            make(map[string]string),
	}
}

// Registered applies an accepted REGISTER. Re-registration refreshes the lease and
// the remote address, which is how a device behind NAT recovers from an address change.
func (m *deviceStateMachine) Registered(ctx context.Context, registration Registration) error {
	if registration.RegisteredAt.IsZero() {
		registration.RegisteredAt = m.now()
	}
	if err := m.store.MarkRegistered(ctx, registration); err != nil {
		return err
	}
	if m.transition(registration.DeviceID, DeviceStateOnline) {
		m.logger.Info("gb28181 device registered",
			zap.String("device_id", registration.DeviceID),
			zap.String("transport", registration.Transport),
			zap.String("remote_addr", registration.RemoteAddr),
			zap.Int("expires_seconds", registration.ExpiresSeconds),
		)
	} else {
		m.logger.Debug("gb28181 device re-registered",
			zap.String("device_id", registration.DeviceID),
			zap.String("remote_addr", registration.RemoteAddr),
			zap.Int("expires_seconds", registration.ExpiresSeconds),
		)
	}
	m.refreshCounters(ctx)
	return nil
}

// Unregistered applies EXPIRES=0.
func (m *deviceStateMachine) Unregistered(ctx context.Context, deviceID string) error {
	if err := m.store.MarkUnregistered(ctx, deviceID, m.now()); err != nil {
		return err
	}
	if m.transition(deviceID, DeviceStateOffline) {
		m.logger.Info("gb28181 device unregistered", zap.String("device_id", deviceID))
	}
	m.refreshCounters(ctx)
	return nil
}

// Keepalive records liveness. A keepalive from an unknown device is rejected by the
// ingress before it reaches this method; a keepalive from a device the sweeper has
// already taken offline is ignored on purpose (see Store.TouchKeepalive).
func (m *deviceStateMachine) Keepalive(ctx context.Context, deviceID string) error {
	updated, err := m.store.TouchKeepalive(ctx, deviceID, m.now())
	if err != nil {
		return err
	}
	if !updated {
		m.logger.Debug("ignoring gb28181 keepalive from an offline device",
			zap.String("device_id", deviceID))
	}
	return nil
}

// Sweep takes expired devices offline. It is safe to call concurrently with the
// request path: the store update is conditional on the row still being online.
func (m *deviceStateMachine) Sweep(ctx context.Context) (int64, error) {
	expired, err := m.store.SweepStale(ctx, m.now(), m.keepaliveTimeout, m.registrationGrace)
	if err != nil {
		return 0, err
	}
	if expired > 0 {
		m.forgetStates()
		m.logger.Info("gb28181 devices timed out", zap.Int64("devices", expired))
		m.refreshCounters(ctx)
	}
	return expired, nil
}

// Interval is how often the ingress should call Sweep.
func (m *deviceStateMachine) Interval() time.Duration { return m.sweepInterval }

// DeviceCounter is the narrow view of the store needed to republish snapshot
// counters. It is declared here so the rule below works for both the service and the
// state machine without either owning it.
type DeviceCounter interface {
	DeviceCounts(ctx context.Context) (total, online int64, err error)
}

// publishDeviceCounters republishes the device counters for the runtime snapshot.
// It is best-effort: the counters are diagnostics, so a failed count must not fail
// the request (or the REGISTER) that triggered it.
func publishDeviceCounters(
	ctx context.Context,
	devices DeviceCounter,
	runtime *Runtime,
	logger *zap.Logger,
) {
	if runtime == nil {
		return
	}
	total, online, err := devices.DeviceCounts(ctx)
	if err != nil {
		logger.Warn("failed to count gb28181 devices", zap.Error(err))
		return
	}
	runtime.SetDeviceCounters(total, online)
}

// refreshCounters republishes the snapshot counters after a state transition.
func (m *deviceStateMachine) refreshCounters(ctx context.Context) {
	publishDeviceCounters(ctx, m.store, m.runtime, m.logger)
}

// transition records a state and reports whether it changed. It keeps the log
// footprint proportional to real events instead of to keepalive traffic.
func (m *deviceStateMachine) transition(deviceID, state string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, ok := m.states[deviceID]
	m.states[deviceID] = state
	return !ok || previous != state
}

// forgetStates drops the transition cache after a bulk state change, so the next
// REGISTER is logged as a real transition instead of a re-registration.
func (m *deviceStateMachine) forgetStates() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = make(map[string]string)
}
