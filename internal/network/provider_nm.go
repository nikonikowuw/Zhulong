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

func (p *NetworkManagerProvider) Name() ProviderName {
	return ProviderNetworkManager
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
	connName, err := p.getOrCreateConnection(ctx, iface)
	if err != nil {
		return err
	}

	args, err := buildNmcliModifyArgs(connName, cfg)
	if err != nil {
		return err
	}

	if err := p.runNmcli(ctx, args...); err != nil {
		return fmt.Errorf("modify connection %s to %s: %w", connName, cfg.Mode, err)
	}

	if err := p.runNmcli(ctx, "con", "up", connName); err != nil {
		return fmt.Errorf("activate connection %s: %w", connName, err)
	}

	p.logger.Info("NetworkManager applied interface config successfully",
		zap.String("interface", iface),
		zap.String("mode", cfg.Mode))
	return nil
}

// buildNmcliModifyArgs translates an InterfaceConfig into a single `nmcli con mod` argument list.
// DHCP is the default branch; static overrides the address related properties.
func buildNmcliModifyArgs(connName string, cfg InterfaceConfig) ([]string, error) {
	method := "auto"
	addresses := ""
	gateway := ""
	dns := ""

	if cfg.Mode == modeStatic {
		cidr, err := CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return nil, fmt.Errorf("calculate CIDR: %w", err)
		}
		method = "manual"
		addresses = cidr
		gateway = cfg.Gateway
		dns = strings.Join(cfg.DNS, " ")
	}

	neverDefault := "yes"
	if cfg.SetDefault {
		neverDefault = "no"
	}

	return []string{
		"con", "mod", connName,
		"ipv4.method", method,
		"ipv4.addresses", addresses,
		"ipv4.gateway", gateway,
		"ipv4.dns", dns,
		"ipv4.never-default", neverDefault,
	}, nil
}

func (p *NetworkManagerProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	connName, err := p.getOrCreateConnection(ctx, iface)
	if err != nil {
		return err
	}

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

func (p *NetworkManagerProvider) getOrCreateConnection(ctx context.Context, iface string) (string, error) {
	out, err := exec.CommandContext(ctx, "nmcli", "-g", "GENERAL.CONNECTION", "device", "show", iface).Output()
	conn := strings.TrimSpace(string(out))
	if err == nil && conn != "" && conn != "--" {
		return conn, nil
	}

	checkOut, checkErr := exec.CommandContext(ctx, "nmcli", "-g", "NAME", "con", "show", iface).Output()
	if checkErr == nil && strings.TrimSpace(string(checkOut)) == iface {
		return iface, nil
	}

	if err := p.runNmcli(ctx, "con", "add", "type", "ethernet", "ifname", iface, "con-name", iface); err != nil {
		return "", fmt.Errorf("create connection for %s: %w", iface, err)
	}
	return iface, nil
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
