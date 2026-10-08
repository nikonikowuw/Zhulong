package network

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// MockProvider provides an in-memory network adapter for testing and non-Linux development environments.
type MockProvider struct {
	mu         sync.RWMutex
	interfaces map[string]InterfaceInfo
	logger     *zap.Logger
}

// NewMockProvider creates a new MockProvider initialized with default simulated physical interfaces.
func NewMockProvider(logger *zap.Logger) *MockProvider {
	if logger == nil {
		logger = zap.NewNop()
	}

	p := &MockProvider{
		interfaces: make(map[string]InterfaceInfo),
		logger:     logger,
	}

	// Initialize mock state
	p.interfaces["eth0"] = InterfaceInfo{
		Name:        "eth0",
		MAC:         "52:54:00:12:34:56",
		LinkUp:      true,
		Mode:        "dhcp",
		IPAddresses: []string{"192.168.1.100/24"},
		Gateway:     "192.168.1.1",
		DNS:         []string{"8.8.8.8", "1.1.1.1"},
		IsDefaultGW: true,
	}

	p.interfaces["eth1"] = InterfaceInfo{
		Name:        "eth1",
		MAC:         "52:54:00:AB:CD:EF",
		LinkUp:      true,
		Mode:        "static",
		IPAddresses: []string{"10.0.0.2/24"},
		Gateway:     "",
		DNS:         []string{},
		IsDefaultGW: false,
	}

	return p
}

func (p *MockProvider) Name() string {
	return string(ProviderMock)
}

func (p *MockProvider) IsSupported() bool {
	return true
}

func (p *MockProvider) HasPermission() bool {
	return true
}

func (p *MockProvider) ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]InterfaceInfo, 0, len(p.interfaces))
	for _, iface := range p.interfaces {
		result = append(result, iface)
	}
	return result, nil
}

func (p *MockProvider) ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, exists := p.interfaces[iface]
	if !exists {
		// Auto-register iface if not yet tracked
		info = InterfaceInfo{
			Name:   iface,
			MAC:    "52:54:00:99:88:77",
			LinkUp: true,
		}
	}

	info.Mode = cfg.Mode
	if cfg.Mode == "static" {
		cidr, err := CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return fmt.Errorf("calculate CIDR: %w", err)
		}
		info.IPAddresses = []string{cidr}
		info.Gateway = cfg.Gateway
		if cfg.DNS != nil {
			info.DNS = cfg.DNS
		} else {
			info.DNS = make([]string, 0)
		}
	} else {
		// DHCP mode simulation
		info.Gateway = ""
		info.DNS = []string{"1.1.1.1"}
		if len(info.IPAddresses) == 0 {
			info.IPAddresses = []string{"192.168.1.222/24"}
		}
	}

	if cfg.SetDefault {
		for k, v := range p.interfaces {
			if k != iface {
				v.IsDefaultGW = false
				p.interfaces[k] = v
			}
		}
		info.IsDefaultGW = true
	} else if cfg.Gateway == "" {
		info.IsDefaultGW = false
	}

	p.interfaces[iface] = info
	p.logger.Info("Mock provider applied interface config",
		zap.String("iface", iface),
		zap.String("mode", cfg.Mode),
		zap.Bool("isDefault", info.IsDefaultGW))
	return nil
}

func (p *MockProvider) ResetInterfaceToMaintenance(ctx context.Context, iface string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Reset target interface to factory emergency IP: 192.168.1.168/24
	target, exists := p.interfaces[iface]
	if !exists {
		target = InterfaceInfo{
			Name:   iface,
			MAC:    "52:54:00:12:34:56",
			LinkUp: true,
		}
	}
	target.Mode = "static"
	target.IPAddresses = []string{"192.168.1.168/24"}
	target.Gateway = ""
	target.DNS = []string{}
	target.IsDefaultGW = false
	p.interfaces[iface] = target

	// Reset all other interfaces to DHCP
	for k, v := range p.interfaces {
		if k != iface {
			v.Mode = "dhcp"
			v.Gateway = ""
			v.IsDefaultGW = false
			p.interfaces[k] = v
		}
	}

	p.logger.Warn("Mock provider reset interface to maintenance mode", zap.String("iface", iface))
	return nil
}
