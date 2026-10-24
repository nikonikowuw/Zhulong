package network

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

const (
	systemdNetworkDir       = "/etc/systemd/network"
	maintenanceIPWithPrefix = "192.168.1.168/24"
)

// SystemdNetworkdProvider manages network configuration through systemd-networkd (.network files).
type SystemdNetworkdProvider struct {
	networkDir string
	logger     *zap.Logger
}

// NewSystemdNetworkdProvider creates a new SystemdNetworkdProvider.
func NewSystemdNetworkdProvider(logger *zap.Logger) *SystemdNetworkdProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SystemdNetworkdProvider{
		networkDir: systemdNetworkDir,
		logger:     logger,
	}
}

func (p *SystemdNetworkdProvider) Name() ProviderName {
	return ProviderSystemdNetwork
}

func (p *SystemdNetworkdProvider) IsSupported() bool {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("networkctl")
	return err == nil
}

func (p *SystemdNetworkdProvider) HasPermission() bool {
	return checkLinuxNetworkPermission()
}

func (p *SystemdNetworkdProvider) ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	return readPhysicalInterfaces(ctx)
}

func (p *SystemdNetworkdProvider) ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error {
	content, err := buildNetworkFileContent(iface, cfg)
	if err != nil {
		return err
	}

	filePath := p.networkFilePath(iface)
	if err := p.writeNetworkFile(filePath, content); err != nil {
		return err
	}

	if err := p.reloadNetworkd(ctx, iface); err != nil {
		return fmt.Errorf("reload networkd for %s: %w", iface, err)
	}

	p.logger.Info("systemd-networkd applied configuration successfully",
		zap.String("interface", iface),
		zap.String("file", filePath))
	return nil
}

func (p *SystemdNetworkdProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	filePath := p.networkFilePath(iface)
	content := fmt.Sprintf("[Match]\nName=%s\n\n[Network]\nAddress=%s\n", iface, maintenanceIPWithPrefix)

	if err := p.writeNetworkFile(filePath, content); err != nil {
		return err
	}

	if err := p.reloadNetworkd(ctx, iface); err != nil {
		return fmt.Errorf("reload networkd: %w", err)
	}

	p.logger.Warn("systemd-networkd reset interface to maintenance IP 192.168.1.168/24",
		zap.String("interface", iface))
	return nil
}

func (p *SystemdNetworkdProvider) networkFilePath(iface string) string {
	return filepath.Join(p.networkDir, fmt.Sprintf("10-%s.network", iface))
}

// writeNetworkFile atomically replaces a .network file so that networkd never reads a partial file.
func (p *SystemdNetworkdProvider) writeNetworkFile(filePath, content string) error {
	if err := os.MkdirAll(p.networkDir, 0o755); err != nil {
		return fmt.Errorf("ensure %s directory: %w", p.networkDir, err)
	}

	tmpFile := filePath + ".tmp"
	if err := writeFileSynced(tmpFile, []byte(content), 0o644); err != nil {
		discardTempFile(p.logger, tmpFile)
		return fmt.Errorf("write network configuration file %s: %w", filePath, err)
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		discardTempFile(p.logger, tmpFile)
		return fmt.Errorf("commit network configuration file %s: %w", filePath, err)
	}
	return nil
}

func (p *SystemdNetworkdProvider) reloadNetworkd(ctx context.Context, iface string) error {
	var stderr bytes.Buffer

	reconfigure := exec.CommandContext(ctx, "networkctl", "reconfigure", iface)
	reconfigure.Stderr = &stderr
	if err := reconfigure.Run(); err == nil {
		return nil
	}

	stderr.Reset()
	reload := exec.CommandContext(ctx, "networkctl", "reload")
	reload.Stderr = &stderr
	if err := reload.Run(); err != nil {
		return fmt.Errorf("networkctl reload failed: %w, stderr: %s", err, stderr.String())
	}
	return nil
}

// buildNetworkFileContent renders the .network file body for the requested configuration.
func buildNetworkFileContent(iface string, cfg InterfaceConfig) (string, error) {
	var content strings.Builder
	fmt.Fprintf(&content, "[Match]\nName=%s\n\n[Network]\n", iface)

	switch cfg.Mode {
	case modeStatic:
		cidr, err := CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return "", fmt.Errorf("calculate CIDR: %w", err)
		}
		fmt.Fprintf(&content, "Address=%s\n", cidr)

		if cfg.SetDefault && cfg.Gateway != "" {
			fmt.Fprintf(&content, "Gateway=%s\n", cfg.Gateway)
		}
		for _, dns := range cfg.DNS {
			if trimmed := strings.TrimSpace(dns); trimmed != "" {
				fmt.Fprintf(&content, "DNS=%s\n", trimmed)
			}
		}
	case modeDHCP:
		content.WriteString("DHCP=yes\n")
		if !cfg.SetDefault {
			content.WriteString("\n[DHCPv4]\nUseRoutes=false\n")
		}
	default:
		return "", fmt.Errorf("unsupported interface mode %q", cfg.Mode)
	}

	return content.String(), nil
}
