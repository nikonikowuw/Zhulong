package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// ScriptProvider executes an external custom hook script for custom BSP / embedded distributions.
type ScriptProvider struct {
	scriptPath string
	logger     *zap.Logger
}

// NewScriptProvider creates a new ScriptProvider.
func NewScriptProvider(scriptPath string, logger *zap.Logger) *ScriptProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ScriptProvider{
		scriptPath: scriptPath,
		logger:     logger,
	}
}

func (p *ScriptProvider) Name() string {
	return string(ProviderCustomScript)
}

func (p *ScriptProvider) IsSupported() bool {
	return p.scriptPath != ""
}

func (p *ScriptProvider) HasPermission() bool {
	return checkLinuxNetworkPermission()
}

func (p *ScriptProvider) ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	// Try asking script with 'list' action
	cmd := exec.CommandContext(ctx, p.scriptPath, "list")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil && stdout.Len() > 0 {
		var list []InterfaceInfo
		if err := json.Unmarshal(stdout.Bytes(), &list); err == nil && len(list) > 0 {
			return list, nil
		}
	}

	// Fallback to kernel sysfs / netlink reader
	return readPhysicalInterfaces(ctx)
}

func (p *ScriptProvider) ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error {
	cidr := ""
	if cfg.Mode == "static" {
		var err error
		cidr, err = CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return fmt.Errorf("calculate CIDR: %w", err)
		}
	}

	dnsStr := strings.Join(cfg.DNS, ",")
	setDefaultStr := "0"
	if cfg.SetDefault {
		setDefaultStr = "1"
	}

	args := []string{"apply", iface, cfg.Mode, cidr, cfg.Gateway, dnsStr, setDefaultStr}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.scriptPath, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("custom script %s apply failed: %w, stderr: %s", p.scriptPath, err, stderr.String())
	}

	p.logger.Info("Custom hook script applied interface configuration successfully",
		zap.String("script", p.scriptPath),
		zap.String("interface", iface))
	return nil
}

func (p *ScriptProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, p.scriptPath, "reset", iface)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("custom script %s reset failed: %w, stderr: %s", p.scriptPath, err, stderr.String())
	}

	p.logger.Warn("Custom hook script reset interface to maintenance mode",
		zap.String("script", p.scriptPath),
		zap.String("interface", iface))
	return nil
}
