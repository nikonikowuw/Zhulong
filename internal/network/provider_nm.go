package network

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// NetworkManagerProvider manages network configuration through the Linux NetworkManager CLI (nmcli).
type NetworkManagerProvider struct {
	logger *zap.Logger
}

// NewNetworkManagerProvider creates a new NetworkManagerProvider.
func NewNetworkManagerProvider(logger *zap.Logger) *NetworkManagerProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &NetworkManagerProvider{logger: logger}
}

func (p *NetworkManagerProvider) Name() string {
	return string(ProviderNetworkManager)
}

func (p *NetworkManagerProvider) IsSupported() bool {
	_, err := exec.LookPath("nmcli")
	return err == nil
}

func (p *NetworkManagerProvider) HasPermission() bool {
	return checkLinuxNetworkPermission()
}

func (p *NetworkManagerProvider) ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	return readPhysicalInterfaces(ctx)
}

func (p *NetworkManagerProvider) ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error {
	connName := p.getOrCreateConnection(ctx, iface)

	if cfg.Mode == "static" {
		cidr, err := CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return fmt.Errorf("calculate CIDR: %w", err)
		}

		args := []string{"con", "mod", connName, "ipv4.method", "manual", "ipv4.addresses", cidr}
		if cfg.Gateway != "" {
			args = append(args, "ipv4.gateway", cfg.Gateway)
		} else {
			args = append(args, "ipv4.gateway", "")
		}

		if len(cfg.DNS) > 0 {
			args = append(args, "ipv4.dns", strings.Join(cfg.DNS, " "))
		} else {
			args = append(args, "ipv4.dns", "")
		}

		if cfg.SetDefault {
			args = append(args, "ipv4.never-default", "no")
		} else {
			args = append(args, "ipv4.never-default", "yes")
		}

		if err := p.runNmcli(ctx, args...); err != nil {
			return fmt.Errorf("modify connection %s to static: %w", connName, err)
		}
	} else {
		// DHCP mode
		args := []string{
			"con", "mod", connName,
			"ipv4.method", "auto",
			"ipv4.addresses", "",
			"ipv4.gateway", "",
			"ipv4.dns", "",
		}
		if cfg.SetDefault {
			args = append(args, "ipv4.never-default", "no")
		} else {
			args = append(args, "ipv4.never-default", "yes")
		}
		if err := p.runNmcli(ctx, args...); err != nil {
			return fmt.Errorf("modify connection %s to dhcp: %w", connName, err)
		}
	}

	// Apply connection
	if err := p.runNmcli(ctx, "con", "up", connName); err != nil {
		return fmt.Errorf("activate connection %s: %w", connName, err)
	}

	p.logger.Info("NetworkManager applied interface config successfully",
		zap.String("interface", iface),
		zap.String("mode", cfg.Mode))
	return nil
}

func (p *NetworkManagerProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	connName := p.getOrCreateConnection(ctx, iface)

	// Set 192.168.1.168/24 static
	args := []string{
		"con", "mod", connName,
		"ipv4.method", "manual",
		"ipv4.addresses", "192.168.1.168/24",
		"ipv4.gateway", "",
		"ipv4.dns", "",
		"ipv4.never-default", "yes",
	}
	if err := p.runNmcli(ctx, args...); err != nil {
		return fmt.Errorf("reset %s to maintenance mode: %w", connName, err)
	}

	if err := p.runNmcli(ctx, "con", "up", connName); err != nil {
		return fmt.Errorf("up %s: %w", connName, err)
	}

	p.logger.Warn("NetworkManager interface reset to factory maintenance IP 192.168.1.168/24",
		zap.String("interface", iface))
	return nil
}

func (p *NetworkManagerProvider) getOrCreateConnection(ctx context.Context, iface string) string {
	// Look up active connection name for this interface
	out, err := exec.CommandContext(ctx, "nmcli", "-g", "GENERAL.CONNECTION", "device", "show", iface).Output()
	conn := strings.TrimSpace(string(out))
	if err == nil && conn != "" && conn != "--" {
		return conn
	}

	// Check if a connection with name == iface already exists
	checkOut, _ := exec.CommandContext(ctx, "nmcli", "-g", "NAME", "con", "show", iface).Output()
	if strings.TrimSpace(string(checkOut)) == iface {
		return iface
	}

	// Create new ethernet connection
	_ = p.runNmcli(ctx, "con", "add", "type", "ethernet", "ifname", iface, "con-name", iface)
	return iface
}

func (p *NetworkManagerProvider) runNmcli(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "nmcli", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nmcli %s failed: %w, stderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}
