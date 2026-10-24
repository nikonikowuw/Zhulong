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

	return &MockProvider{
		interfaces: map[string]InterfaceInfo{
			"eth0": {
				Name:        "eth0",
				MAC:         "52:54:00:12:34:56",
				LinkUp:      true,
				Mode:        modeDHCP,
				IPAddresses: []string{"192.168.1.100/24"},
				Gateway:     "192.168.1.1",
				DNS:         []string{"8.8.8.8", "1.1.1.1"},
				IsDefaultGW: true,
			},
			"eth1": {
				Name:        "eth1",
				MAC:         "52:54:00:AB:CD:EF",
				LinkUp:      true,
				Mode:        modeStatic,
				IPAddresses: []string{"10.0.0.2/24"},
				Gateway:     "",
				DNS:         []string{},
				IsDefaultGW: false,
			},
		},
		logger: logger,
	}
}

func (p *MockProvider) Name() ProviderName {
	return ProviderMock
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
	for _, ifc := range p.interfaces {
		result = append(result, ifc)
	}
	return result, nil
}

func (p *MockProvider) ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, exists := p.interfaces[iface]
	if !exists {
		info = InterfaceInfo{
			Name:   iface,
			MAC:    "52:54:00:99:88:77",
			LinkUp: true,
		}
	}

	info.Mode = cfg.Mode
	switch cfg.Mode {
	case modeStatic:
		cidr, err := CIDRFromIPAndMask(cfg.IPAddress, cfg.SubnetMask)
		if err != nil {
			return fmt.Errorf("calculate CIDR: %w", err)
		}
		info.IPAddresses = []string{cidr}
		info.Gateway = cfg.Gateway
		info.DNS = normalizeStrings(cfg.DNS)
	case modeDHCP:
		info.Gateway = ""
		info.DNS = []string{"1.1.1.1"}
		if len(info.IPAddresses) == 0 {
			info.IPAddresses = []string{"192.168.1.222/24"}
		}
	}

	if cfg.SetDefault {
		for name, other := range p.interfaces {
			if name == iface {
				continue
			}
			other.IsDefaultGW = false
			p.interfaces[name] = other
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

	// Reset target interface to factory emergency IP 192.168.1.168/24.
	target, exists := p.interfaces[iface]
	if !exists {
		target = InterfaceInfo{
			Name:   iface,
			MAC:    "52:54:00:12:34:56",
			LinkUp: true,
		}
	}
	target.Mode = modeStatic
	target.IPAddresses = []string{"192.168.1.168/24"}
	target.Gateway = ""
	target.DNS = []string{}
	target.IsDefaultGW = false
	p.interfaces[iface] = target

	for name, other := range p.interfaces {
		if name == iface {
			continue
		}
		other.Mode = modeDHCP
		other.Gateway = ""
		other.IsDefaultGW = false
		p.interfaces[name] = other
	}

	p.logger.Warn("Mock provider reset interface to maintenance mode", zap.String("iface", iface))
	return nil
}

// normalizeStrings returns a non-nil copy of values so JSON serialization emits [] instead of null.
func normalizeStrings(values []string) []string {
	if values == nil {
		return make([]string, 0)
	}
	return values
}
