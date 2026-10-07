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
		networkDir: "/etc/systemd/network",
		logger:     logger,
	}
}

func (p *SystemdNetworkdProvider) Name() string {
	return string(ProviderSystemdNetwork)
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
	filePath := filepath.Join(p.networkDir, fmt.Sprintf("10-%s.network", iface))

	var content strings.Builder
	content.WriteString(fmt.Sprintf("[Match]\nName=%s\n\n[Network]\n", iface))

	if cfg.Mode == "static" {
		cidr, err := CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return fmt.Errorf("calculate CIDR: %w", err)
		}
		content.WriteString(fmt.Sprintf("Address=%s\n", cidr))

		if cfg.SetDefault && cfg.Gateway != "" {
			content.WriteString(fmt.Sprintf("Gateway=%s\n", cfg.Gateway))
		}
		for _, dns := range cfg.DNS {
			if trimmed := strings.TrimSpace(dns); trimmed != "" {
				content.WriteString(fmt.Sprintf("DNS=%s\n", trimmed))
			}
		}
	} else {
		// DHCP mode
		content.WriteString("DHCP=yes\n")
		if !cfg.SetDefault {
			content.WriteString("\n[DHCPv4]\nUseRoutes=false\n")
		}
	}

	if err := os.MkdirAll(p.networkDir, 0755); err != nil {
		return fmt.Errorf("ensure %s directory: %w", p.networkDir, err)
	}

	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("write temporary network file: %w", err)
	}
	if err := os.Rename(tmpFile, filePath); err != nil {
		return fmt.Errorf("commit network configuration file %s: %w", filePath, err)
	}

	// Reload systemd-networkd
	if err := p.reloadNetworkd(ctx, iface); err != nil {
		return fmt.Errorf("reload networkd for %s: %w", iface, err)
	}

	p.logger.Info("systemd-networkd applied configuration successfully",
		zap.String("interface", iface),
		zap.String("file", filePath))
	return nil
}

func (p *SystemdNetworkdProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	filePath := filepath.Join(p.networkDir, fmt.Sprintf("10-%s.network", iface))
	content := fmt.Sprintf("[Match]\nName=%s\n\n[Network]\nAddress=192.168.1.168/24\n", iface)

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write maintenance configuration: %w", err)
	}

	if err := p.reloadNetworkd(ctx, iface); err != nil {
		return fmt.Errorf("reload networkd: %w", err)
	}

	p.logger.Warn("systemd-networkd reset interface to maintenance IP 192.168.1.168/24",
		zap.String("interface", iface))
	return nil
}

func (p *SystemdNetworkdProvider) reloadNetworkd(ctx context.Context, iface string) error {
	var stderr bytes.Buffer
	// Try networkctl reconfigure first
	cmd := exec.CommandContext(ctx, "networkctl", "reconfigure", iface)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Fallback to networkctl reload
	stderr.Reset()
	reloadCmd := exec.CommandContext(ctx, "networkctl", "reload")
	reloadCmd.Stderr = &stderr
	if err := reloadCmd.Run(); err != nil {
		return fmt.Errorf("networkctl reload failed: %w, stderr: %s", err, stderr.String())
	}
	return nil
}
