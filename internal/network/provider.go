package network

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// ProviderName represents the identifier of the active network driver.
type ProviderName string

const (
	ProviderNetworkManager ProviderName = "network-manager"
	ProviderSystemdNetwork ProviderName = "systemd-networkd"
	ProviderCustomScript   ProviderName = "custom-script"
	ProviderMock           ProviderName = "mock"
)

// capNetAdminBit is the bit position of CAP_NET_ADMIN inside the CapEff mask.
const capNetAdminBit = 12

// NetworkProvider defines the interface for interacting with heterogeneous Linux host network stacks.
type NetworkProvider interface {
	Name() ProviderName
	IsSupported() bool
	HasPermission() bool
	ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error)
	ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error
	ResetInterfaceToMaintenance(ctx context.Context, iface string) error
}

func checkLinuxNetworkPermission() bool {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		return true
	}

	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(data), "\n") {
		mask, found := strings.CutPrefix(line, "CapEff:")
		if !found {
			continue
		}

		fields := strings.Fields(mask)
		if len(fields) == 0 {
			return false
		}

		capabilities, err := strconv.ParseUint(fields[0], 16, 64)
		if err != nil {
			return false
		}

		return capabilities&(1<<capNetAdminBit) != 0
	}

	return false
}

// DetectProvider automatically detects the appropriate network provider based on host runtime environment.
//
// It performs filesystem and exec.LookPath probes, so it must not be called during dependency injection
// wiring; NewNetworkComponents defers it to NetworkLifecycle.Start through lazyProvider.
func DetectProvider(customScript string, logger *zap.Logger) NetworkProvider {
	if logger == nil {
		logger = zap.NewNop()
	}

	if customScript != "" {
		if fi, err := os.Stat(customScript); err == nil && fi.Mode().Perm()&0o111 != 0 {
			logger.Info("Using custom script network provider", zap.String("script", customScript))
			return NewScriptProvider(customScript, logger)
		}
		logger.Warn("Custom network script configured but not executable, continuing auto-detection",
			zap.String("script", customScript))
	}

	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("nmcli"); err == nil {
			logger.Info("Detected NetworkManager provider (nmcli available)")
			return NewNetworkManagerProvider(logger)
		}

		if _, err := os.Stat("/run/systemd/system"); err == nil {
			if _, err := exec.LookPath("networkctl"); err == nil {
				logger.Info("Detected systemd-networkd provider (networkctl available)")
				return NewSystemdNetworkdProvider(logger)
			}
		}
	}

	logger.Warn("Using mock network provider (runtime environment has no supported Linux network stack)",
		zap.String("os", runtime.GOOS))
	return NewMockProvider(logger)
}

// lazyProvider defers host network stack detection until first use so that component
// construction stays free of filesystem and process side effects.
type lazyProvider struct {
	customScript string
	logger       *zap.Logger
	once         sync.Once
	inner        NetworkProvider
}

func newLazyProvider(customScript string, logger *zap.Logger) *lazyProvider {
	return &lazyProvider{
		customScript: customScript,
		logger:       logger,
	}
}

// resolve runs detection exactly once and returns the detected provider.
func (p *lazyProvider) resolve() NetworkProvider {
	p.once.Do(func() {
		p.inner = DetectProvider(p.customScript, p.logger)
	})
	return p.inner
}

func (p *lazyProvider) Name() ProviderName {
	return p.resolve().Name()
}

func (p *lazyProvider) IsSupported() bool {
	return p.resolve().IsSupported()
}

func (p *lazyProvider) HasPermission() bool {
	return p.resolve().HasPermission()
}

func (p *lazyProvider) ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	return p.resolve().ListPhysicalInterfaces(ctx)
}

func (p *lazyProvider) ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error {
	return p.resolve().ApplyInterfaceConfig(ctx, iface, cfg)
}

func (p *lazyProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	return p.resolve().ResetInterfaceToMaintenance(ctx, iface)
}
