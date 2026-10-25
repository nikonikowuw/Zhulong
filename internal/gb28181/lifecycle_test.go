package gb28181

import (
	"context"
	"errors"
	"net"
	"testing"
)

// stubRepo is a ConfigRepository whose read can be made to fail.
type stubRepo struct {
	cfg     Config
	readErr error
	saved   *Config
}

func (r *stubRepo) GetConfig(context.Context) (*Config, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	cfg := r.cfg
	return &cfg, nil
}

func (r *stubRepo) SaveConfig(_ context.Context, cfg Config) (*Config, error) {
	r.saved = &cfg
	return &cfg, nil
}

// stubIngress records lifecycle calls and can fail on demand.
type stubIngress struct {
	startErr   error
	stopErrors []error
	stopCalls  int
	started    bool
	stopped    bool
	cfg        Config
}

func (i *stubIngress) Start(_ context.Context, cfg Config, _ string) error {
	i.started = true
	i.cfg = cfg
	return i.startErr
}

func (i *stubIngress) Stop(context.Context) error {
	i.stopCalls++
	if i.stopCalls <= len(i.stopErrors) {
		return i.stopErrors[i.stopCalls-1]
	}
	i.stopped = true
	i.started = false
	return nil
}

func fixedAddrs(values ...string) InterfaceAddrs {
	return func() ([]net.IP, error) {
		ips := make([]net.IP, 0, len(values))
		for _, value := range values {
			ips = append(ips, net.ParseIP(value))
		}
		return ips, nil
	}
}

func enabledConfig() Config {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.SIPID = "34020000002000000001"
	return cfg.Normalize()
}

func TestLifecycleDisabledDoesNotStartIngress(t *testing.T) {
	repo := &stubRepo{cfg: DefaultConfig()}
	runtime := NewRuntime()
	ingress := &stubIngress{}
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Ingress: ingress, Addrs: fixedAddrs(),
	})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if ingress.started {
		t.Fatal("a disabled configuration must not bind the SIP listener")
	}
	status := runtime.Status()
	if status.State != RuntimeStateDisabled {
		t.Fatalf("expected state %q, got %q", RuntimeStateDisabled, status.State)
	}
	if status.RestartRequired {
		t.Fatal("expected no restart requirement right after startup")
	}
}

func TestLifecycleEnabledWithoutIngressReportsError(t *testing.T) {
	repo := &stubRepo{cfg: enabledConfig()}
	runtime := NewRuntime()
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Addrs: fixedAddrs("192.168.1.10"),
	})

	err := manager.Start(context.Background())
	if err == nil {
		t.Fatal("expected an error so the host records a degraded subsystem")
	}
	status := runtime.Status()
	// The critical assertion: an enabled subsystem with no ingress must never
	// report "running" — the operator would be looking at a listener that does not
	// exist and cannot understand why devices never register.
	if status.State != RuntimeStateError {
		t.Fatalf("expected state %q, got %q", RuntimeStateError, status.State)
	}
	if status.Detail != DetailIngressUnavailable {
		t.Fatalf("expected detail %q, got %q", DetailIngressUnavailable, status.Detail)
	}
}

func TestLifecycleStartsAndStopsIngress(t *testing.T) {
	repo := &stubRepo{cfg: enabledConfig()}
	runtime := NewRuntime()
	ingress := &stubIngress{}
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Ingress: ingress, Addrs: fixedAddrs("192.168.1.10"),
	})

	ctx := context.Background()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !ingress.started {
		t.Fatal("expected the ingress to start")
	}
	status := runtime.Status()
	if status.State != RuntimeStateRunning {
		t.Fatalf("expected state %q, got %q (detail %q)", RuntimeStateRunning, status.State, status.Detail)
	}
	if status.AdvertisedIP != "192.168.1.10" {
		t.Fatalf("expected the single NIC address to be advertised, got %q", status.AdvertisedIP)
	}
	if status.Startup.RTPPortMax == 0 {
		t.Fatal("expected the startup snapshot to carry the port range")
	}

	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if !ingress.stopped {
		t.Fatal("expected the ingress to be stopped")
	}
}

func TestLifecycleIngressStartFailureRollsBackAndRetriesCleanup(t *testing.T) {
	repo := &stubRepo{cfg: enabledConfig()}
	runtime := NewRuntime()
	startErr := errors.New("tcp bind failed after udp bind")
	rollbackErr := errors.New("udp listener close failed")
	ingress := &stubIngress{startErr: startErr, stopErrors: []error{rollbackErr}}
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Ingress: ingress, Addrs: fixedAddrs("192.168.1.10"),
	})

	err := manager.Start(context.Background())
	if !errors.Is(err, startErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("expected start and rollback errors, got %v", err)
	}
	if ingress.stopCalls != 1 {
		t.Fatalf("expected failed start to attempt cleanup, got %d Stop calls", ingress.stopCalls)
	}
	status := runtime.Status()
	if status.State != RuntimeStateError || status.Detail != DetailIngressStartFailed {
		t.Fatalf("expected error/%s, got %q/%q", DetailIngressStartFailed, status.State, status.Detail)
	}

	// The shared host stop path retries cleanup if the immediate rollback failed.
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop failed: %v", err)
	}
	if ingress.stopCalls != 2 || ingress.started || !ingress.stopped {
		t.Fatalf("expected a successful cleanup retry, calls=%d started=%t stopped=%t",
			ingress.stopCalls, ingress.started, ingress.stopped)
	}
}

func TestLifecycleInvalidStoredConfigReportsError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true // no SIP identity
	repo := &stubRepo{cfg: cfg}
	runtime := NewRuntime()
	ingress := &stubIngress{}
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Ingress: ingress, Addrs: fixedAddrs("192.168.1.10"),
	})

	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("expected an invalid stored configuration to fail the start")
	}
	if ingress.started {
		t.Fatal("an invalid configuration must not reach the ingress")
	}
	if status := runtime.Status(); status.State != RuntimeStateError || status.Detail != DetailConfigInvalid {
		t.Fatalf("expected error/%s, got %q/%q", DetailConfigInvalid, status.State, status.Detail)
	}
}

func TestLifecycleAmbiguousAddressIsReported(t *testing.T) {
	repo := &stubRepo{cfg: enabledConfig()}
	runtime := NewRuntime()
	ingress := &stubIngress{}
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Ingress: ingress,
		Addrs: fixedAddrs("192.168.1.10", "10.0.0.5"),
	})

	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("expected multiple candidate addresses to fail loudly")
	}
	if ingress.started {
		t.Fatal("the ingress must not start with an undetermined advertised address")
	}
	if status := runtime.Status(); status.Detail != DetailMediaAddressUnresolvable {
		t.Fatalf("expected detail %q, got %q", DetailMediaAddressUnresolvable, status.Detail)
	}
}

func TestLifecycleUnreadableConfigIsReported(t *testing.T) {
	repo := &stubRepo{readErr: errors.New("database is not ready")}
	runtime := NewRuntime()
	manager := NewLifecycleManager(LifecycleOptions{Repo: repo, Runtime: runtime})

	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("expected a read failure to surface")
	}
	if status := runtime.Status(); status.State != RuntimeStateError || status.Detail != DetailConfigUnreadable {
		t.Fatalf("expected error/%s, got %q/%q", DetailConfigUnreadable, status.State, status.Detail)
	}
}

func TestLifecycleStopIsSafeBeforeStart(t *testing.T) {
	repo := &stubRepo{cfg: DefaultConfig()}
	runtime := NewRuntime()
	manager := NewLifecycleManager(LifecycleOptions{
		Repo: repo, Runtime: runtime, Ingress: &stubIngress{}, Addrs: fixedAddrs(),
	})

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start must be a no-op, got %v", err)
	}
}
