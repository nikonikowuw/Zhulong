package network

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

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

// NetworkProvider defines the interface for interacting with heterogeneous Linux host network stacks.
type NetworkProvider interface {
	Name() string
	IsSupported() bool
	HasPermission() bool
	ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error)
	ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error
	ResetInterfaceToMaintenance(ctx context.Context, iface string) error
}

func checkLinuxNetworkPermission() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if os.Geteuid() == 0 {
		return true
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				val, err := strconv.ParseUint(fields[1], 16, 64)
				if err == nil {
					// CAP_NET_ADMIN is bit 12 (1 << 12)
					return (val & (1 << 12)) != 0
				}
			}
		}
	}
	return false
}

// DetectProvider automatically detects the appropriate network provider based on host runtime environment.
func DetectProvider(customScript string, logger *zap.Logger) NetworkProvider {
	if logger == nil {
		logger = zap.NewNop()
	}

	// 1. Explicit Custom Script Hook
	if customScript != "" {
		if fi, err := os.Stat(customScript); err == nil && fi.Mode().Perm()&0111 != 0 {
			logger.Info("Using custom script network provider", zap.String("script", customScript))
			return NewScriptProvider(customScript, logger)
		}
		logger.Warn("Custom network script configured but not executable, continuing auto-detection",
			zap.String("script", customScript))
	}

	// 2. Linux environment detection
	if runtime.GOOS == "linux" {
		// Detect NetworkManager
		if _, err := exec.LookPath("nmcli"); err == nil {
			logger.Info("Detected NetworkManager provider (nmcli available)")
			return NewNetworkManagerProvider(logger)
		}

		// Detect systemd-networkd
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			if _, err := exec.LookPath("networkctl"); err == nil {
				logger.Info("Detected systemd-networkd provider (networkctl available)")
				return NewSystemdNetworkdProvider(logger)
			}
		}
	}

	// 3. Fallback to In-Memory Mock provider (for dev / macOS / non-linux)
	logger.Warn("Using mock network provider (runtime environment has no supported Linux network stack)",
		zap.String("os", runtime.GOOS))
	return NewMockProvider(logger)
}
