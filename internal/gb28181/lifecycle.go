package gb28181

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"
)

// Ingress is the SIP signaling surface the lifecycle starts when the stored
// configuration is enabled. Stop must also clean resources left by a failed Start.
//
// The production implementation is sipIngress. When an enabled configuration has no
// ingress (a stub in tests, or a build without one) the lifecycle reports state=error
// with a diagnostic rather than claiming to be running: an operator who enabled
// GB28181 must never see "running" on a process that binds nothing.
type Ingress interface {
	Start(ctx context.Context, cfg Config, advertisedIP string) error
	Stop(ctx context.Context) error
}

// InterfaceAddrs supplies the host's addresses for media_advertised_ip resolution.
// Injecting it keeps the resolver pure and testable without touching real NICs.
type InterfaceAddrs func() ([]net.IP, error)

// DefaultInterfaceAddrs enumerates non-loopback IPv4 addresses of the host.
func DefaultInterfaceAddrs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("enumerate interface addresses: %w", err)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			ips = append(ips, ipNet.IP)
		}
	}
	return ips, nil
}

// LifecycleManager loads the persisted configuration at startup, validates it, and
// starts the SIP ingress when enabled.
//
// It is deliberately a degradable subsystem: a bind failure or an invalid stored
// config must not take the web console down with it, because the console is the
// only way for an operator to fix the config.
type LifecycleManager struct {
	repo    ConfigRepository
	runtime *Runtime
	ingress Ingress
	addrs   InterfaceAddrs
	logger  *zap.Logger

	// mu serializes Start and Stop. The host calls them from a single lifecycle
	// goroutine, but the pair mutates one shared flag and the ingress socket: making
	// the assumption a guarantee is cheaper than rediscovering it as a race.
	mu      sync.Mutex
	started bool
}

// LifecycleOptions contains the dependencies used to construct a LifecycleManager.
type LifecycleOptions struct {
	Repo    ConfigRepository
	Runtime *Runtime
	Ingress Ingress
	Addrs   InterfaceAddrs
	Logger  *zap.Logger
}

// NewLifecycleManager constructs the lifecycle without performing I/O.
func NewLifecycleManager(options LifecycleOptions) *LifecycleManager {
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}
	if options.Addrs == nil {
		options.Addrs = DefaultInterfaceAddrs
	}
	return &LifecycleManager{
		repo:    options.Repo,
		runtime: options.Runtime,
		ingress: options.Ingress,
		addrs:   options.Addrs,
		logger:  options.Logger,
	}
}

// Start reads the persisted configuration and applies it. Errors are returned so
// the host records the subsystem as degraded; the web console keeps serving.
func (m *LifecycleManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, err := m.repo.GetConfig(ctx)
	if err != nil {
		m.runtime.SetState(RuntimeStateError, DetailConfigUnreadable)
		return fmt.Errorf("load gb28181 config: %w", err)
	}
	cfg := stored.Normalize()

	advertised, resolveErr := m.resolveAdvertised(cfg)
	if resolveErr != nil {
		m.runtime.LoadStartup(cfg, "")
		m.runtime.SetState(RuntimeStateError, DetailMediaAddressUnresolvable)
		return fmt.Errorf("resolve media advertised address: %w", resolveErr)
	}
	m.runtime.LoadStartup(cfg, advertised)

	if !cfg.Enabled {
		m.runtime.SetState(RuntimeStateDisabled, "")
		m.logger.Info("gb28181 ingress disabled by stored configuration")
		return nil
	}
	if err := cfg.Validate(); err != nil {
		m.runtime.SetState(RuntimeStateError, DetailConfigInvalid)
		return fmt.Errorf("stored gb28181 config is invalid: %w", err)
	}
	if m.ingress == nil {
		m.runtime.SetState(RuntimeStateError, DetailIngressUnavailable)
		return fmt.Errorf("gb28181 ingress is not available in this build")
	}

	// started is set before the attempt because Ingress.Stop must clean up a partially
	// started listener. A failed rollback leaves it set so host shutdown retries.
	m.started = true
	startErr := m.ingress.Start(ctx, cfg, advertised)
	if startErr != nil {
		m.runtime.SetState(RuntimeStateError, DetailIngressStartFailed)
		if cleanupErr := m.ingress.Stop(ctx); cleanupErr != nil {
			return errors.Join(
				fmt.Errorf("start gb28181 ingress: %w", startErr),
				fmt.Errorf("rollback partially started gb28181 ingress: %w", cleanupErr))
		}
		m.started = false
		return fmt.Errorf("start gb28181 ingress: %w", startErr)
	}
	m.runtime.SetState(RuntimeStateRunning, "")
	m.logger.Info("gb28181 ingress started",
		zap.String("sip_listen", cfg.SIPListen),
		zap.String("transport", cfg.SIPTransport),
		zap.String("advertised_ip", advertised),
		zap.Int("playback_capacity", cfg.MediaCapacity()),
	)
	return nil
}

// Stop releases the ingress. Ingress.Stop must be safe after a partially failed
// Start; failed rollback leaves started set so the host shutdown path can retry.
func (m *LifecycleManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started || m.ingress == nil {
		return nil
	}
	if err := m.ingress.Stop(ctx); err != nil {
		m.runtime.SetState(RuntimeStateError, DetailIngressStopFailed)
		return fmt.Errorf("stop gb28181 ingress: %w", err)
	}
	m.started = false
	m.runtime.SetState(RuntimeStateDisabled, "")
	return nil
}

// resolveAdvertised computes the address written into SDP. A configured value wins;
// otherwise exactly one non-loopback IPv4 is required (see resolveAdvertisedIP).
//
// A disabled configuration is never resolved: nothing binds, so demanding a
// resolvable address there would make a fresh box fail its first boot for a
// subsystem that is switched off.
func (m *LifecycleManager) resolveAdvertised(cfg Config) (string, error) {
	if !cfg.Enabled {
		return cfg.MediaAdvertisedIP, nil
	}
	var candidates []net.IP
	if cfg.MediaAdvertisedIP == "" {
		addrs, err := m.addrs()
		if err != nil {
			return "", err
		}
		candidates = addrs
	}
	return resolveAdvertisedIP(cfg.MediaBindAddress, cfg.MediaAdvertisedIP, candidates)
}
