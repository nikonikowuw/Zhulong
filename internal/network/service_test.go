package network

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestServiceListInterfacesCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, "8000", zap.NewNop())

	ctx := context.Background()

	// 1. Client IP matching eth0 (192.168.1.55 within 192.168.1.100/24)
	ifaces, err := svc.ListInterfaces(ctx, RequestOrigin{
		ClientIP: "192.168.1.55:54321",
	})
	if err != nil {
		t.Fatalf("ListInterfaces failed: %v", err)
	}

	var eth0, eth1 InterfaceInfo
	for _, iface := range ifaces {
		if iface.Name == "eth0" {
			eth0 = iface
		}
		if iface.Name == "eth1" {
			eth1 = iface
		}
	}

	if !eth0.IsCurrent {
		t.Errorf("expected eth0 to be marked current for client IP 192.168.1.55")
	}
	if eth1.IsCurrent {
		t.Errorf("expected eth1 not to be marked current")
	}

	// 2. Client IP matching eth1 (10.0.0.100 within 10.0.0.2/24)
	ifaces2, _ := svc.ListInterfaces(ctx, RequestOrigin{
		ClientIP: "10.0.0.100",
	})
	for _, iface := range ifaces2 {
		if iface.Name == "eth1" && !iface.IsCurrent {
			t.Errorf("expected eth1 to be current for 10.0.0.100")
		}
	}

	// 3. Socket Local Address matching eth0 explicitly
	ifaces3, _ := svc.ListInterfaces(ctx, RequestOrigin{
		LocalAddr: "192.168.1.100:8080",
		Host:      "localhost",
		ClientIP:  "172.16.0.5",
	})
	for _, iface := range ifaces3 {
		if iface.Name == "eth0" && !iface.IsCurrent {
			t.Errorf("expected eth0 to be current for local socket address 192.168.1.100:8080")
		}
	}
	// 4. Host IP matching eth0 directly
	hostMatched, err := svc.ListInterfaces(ctx, RequestOrigin{
		Host:     "192.168.1.100",
		ClientIP: "172.16.0.5",
	})
	if err != nil {
		t.Fatalf("ListInterfaces failed: %v", err)
	}
	for _, iface := range hostMatched {
		if iface.Name == "eth0" && !iface.IsCurrent {
			t.Errorf("expected eth0 to be current for Host IP 192.168.1.100")
		}
	}

	// 5. An unmatched or ambiguous client address must remain unmarked.
	unmatched, err := svc.ListInterfaces(ctx, RequestOrigin{
		ClientIP: "172.16.0.5",
	})
	if err != nil {
		t.Fatalf("ListInterfaces failed: %v", err)
	}
	for _, iface := range unmatched {
		if iface.IsCurrent {
			t.Errorf("expected no current interface without a unique match, got %q", iface.Name)
		}
	}
}

func TestServiceListInterfacesDoesNotGuessAmbiguousSubnet(t *testing.T) {
	provider := NewMockProvider(zap.NewNop())
	provider.mu.Lock()
	eth1 := provider.interfaces["eth1"]
	eth1.IPAddresses = append(eth1.IPAddresses, "192.168.1.200/24")
	provider.interfaces["eth1"] = eth1
	provider.mu.Unlock()

	svc := NewNetworkService(provider, NewWatchdogManager(t.TempDir(), provider, zap.NewNop()), "8000", zap.NewNop())
	ifaces, err := svc.ListInterfaces(context.Background(), RequestOrigin{
		ClientIP: "192.168.1.55",
	})
	if err != nil {
		t.Fatalf("ListInterfaces failed: %v", err)
	}
	for _, iface := range ifaces {
		if iface.IsCurrent {
			t.Errorf("expected no current interface for an ambiguous subnet, got %q", iface.Name)
		}
	}
}

// emptyProvider simulates a host that exposes no physical network interface.
type emptyProvider struct{}

func (emptyProvider) Name() ProviderName  { return ProviderMock }
func (emptyProvider) IsSupported() bool   { return true }
func (emptyProvider) HasPermission() bool { return true }

func (emptyProvider) ListPhysicalInterfaces(context.Context) ([]InterfaceInfo, error) {
	return nil, nil
}

func (emptyProvider) ApplyInterfaceConfig(context.Context, string, InterfaceConfig) error { return nil }
func (emptyProvider) ResetInterfaceToMaintenance(context.Context, string) error           { return nil }

func TestServiceListInterfacesWithoutPhysicalNICs(t *testing.T) {
	provider := emptyProvider{}
	svc := NewNetworkService(provider, NewWatchdogManager(t.TempDir(), provider, zap.NewNop()), "8000", zap.NewNop())

	ifaces, err := svc.ListInterfaces(context.Background(), RequestOrigin{})
	if err != nil {
		t.Fatalf("ListInterfaces failed: %v", err)
	}
	if ifaces == nil {
		t.Fatal("expected a non-nil slice so the API encodes [] instead of null")
	}
	if len(ifaces) != 0 {
		t.Fatalf("expected no interfaces, got %d", len(ifaces))
	}
}

func TestComputeTargetURL(t *testing.T) {
	staticCfg := InterfaceConfig{Mode: modeStatic, IPAddress: "192.168.1.150"}
	dhcpCfg := InterfaceConfig{Mode: modeDHCP}

	tests := []struct {
		name        string
		cfg         InterfaceConfig
		currentHost string
		defaultPort string
		want        string
	}{
		{
			name:        "static mode targets the new address on the request port",
			cfg:         staticCfg,
			currentHost: "192.168.1.100:9000",
			defaultPort: "8000",
			want:        "http://192.168.1.150:9000/#settings?token=tok",
		},
		{
			name:        "static mode falls back to the configured port",
			cfg:         staticCfg,
			currentHost: "192.168.1.100",
			defaultPort: "8000",
			want:        "http://192.168.1.150:8000/#settings?token=tok",
		},
		{
			name:        "dhcp mode keeps the request host",
			cfg:         dhcpCfg,
			currentHost: "192.168.1.100:8000",
			defaultPort: "8000",
			want:        "http://192.168.1.100:8000/#settings?token=tok",
		},
		{
			name:        "missing request host falls back to loopback",
			cfg:         dhcpCfg,
			currentHost: "",
			defaultPort: "8000",
			want:        "http://127.0.0.1:8000/#settings?token=tok",
		},
		{
			name:        "unknown port omits the separator",
			cfg:         dhcpCfg,
			currentHost: "",
			defaultPort: "",
			want:        "http://127.0.0.1/#settings?token=tok",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := computeTargetURL(test.cfg, test.currentHost, test.defaultPort, "tok")
			if got != test.want {
				t.Fatalf("computeTargetURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPortOf(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{address: "127.0.0.1:8000", want: "8000"},
		{address: "0.0.0.0:9001", want: "9001"},
		{address: "127.0.0.1", want: ""},
		{address: "", want: ""},
	}

	for _, test := range tests {
		if got := portOf(test.address); got != test.want {
			t.Errorf("portOf(%q) = %q, want %q", test.address, got, test.want)
		}
	}
}

func TestServiceValidationAndGatewayConflict(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, "8000", zap.NewNop())
	ctx := context.Background()

	// 1. Invalid IP
	_, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "invalid-ip",
		SubnetMask: "255.255.255.0",
	}, "127.0.0.1:8080")
	if err == nil {
		t.Errorf("expected error for invalid IP")
	}

	// 2. Gateway out of subnet
	_, err = svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.100",
		SubnetMask: "255.255.255.0",
		Gateway:    "10.0.0.1", // outside 192.168.1.0/24!
	}, "127.0.0.1:8080")
	if err == nil {
		t.Errorf("expected error for gateway out of subnet")
	}

	// 3. Single default gateway conflict
	// eth0 is already default gateway in MockProvider.
	// Try configuring eth1 with SetDefault: true
	_, err = svc.ApplyConfig(ctx, "eth1", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "10.0.0.50",
		SubnetMask: "255.255.255.0",
		Gateway:    "10.0.0.1",
		SetDefault: true,
	}, "127.0.0.1:8080")
	if err == nil {
		t.Errorf("expected DEFAULT_GATEWAY_CONFLICT when setting another default gateway on eth1")
	}
}

func TestServiceApplyAndConfirmWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, "8000", zap.NewNop())
	ctx := context.Background()

	// Valid static apply on eth0
	resp, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.150",
		SubnetMask: "255.255.255.0",
		Gateway:    "192.168.1.1",
		DNS:        []string{"8.8.8.8"},
		SetDefault: true,
	}, "192.168.1.100:8080")
	if err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	if resp.ConfirmToken == "" || resp.TransactionID == "" {
		t.Fatalf("expected non-empty token and txID, got %+v", resp)
	}

	// Wait for 300ms delayed apply goroutine to execute
	time.Sleep(350 * time.Millisecond)

	// Confirm
	if err := svc.Confirm(ctx, resp.ConfirmToken); err != nil {
		t.Fatalf("Confirm failed: %v", err)
	}

	// Verify provider has new IP
	ifaces, _ := provider.ListPhysicalInterfaces(ctx)
	var eth0 InterfaceInfo
	for _, iface := range ifaces {
		if iface.Name == "eth0" {
			eth0 = iface
			break
		}
	}
	if len(eth0.IPAddresses) == 0 || eth0.IPAddresses[0] != "192.168.1.150/24" {
		t.Fatalf("expected IP 192.168.1.150/24, got %v", eth0.IPAddresses)
	}
}

func TestServiceResetNetwork(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, "8000", zap.NewNop())
	ctx := context.Background()

	if err := svc.ResetNetwork(ctx); err != nil {
		t.Fatalf("ResetNetwork failed: %v", err)
	}

	ifaces, _ := provider.ListPhysicalInterfaces(ctx)
	var eth0, eth1 InterfaceInfo
	for _, iface := range ifaces {
		if iface.Name == "eth0" {
			eth0 = iface
		}
		if iface.Name == "eth1" {
			eth1 = iface
		}
	}
	if len(eth0.IPAddresses) == 0 || eth0.IPAddresses[0] != "192.168.1.168/24" {
		t.Fatalf("expected eth0 to be reset to 192.168.1.168/24, got %v", eth0.IPAddresses)
	}
	if eth1.Mode != "dhcp" {
		t.Fatalf("expected eth1 secondary interface to be restored to dhcp, got %v", eth1.Mode)
	}
}

type unprivilegedMockProvider struct {
	*MockProvider
}

func (p *unprivilegedMockProvider) HasPermission() bool {
	return false
}

func TestServicePermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	provider := &unprivilegedMockProvider{MockProvider: NewMockProvider(zap.NewNop())}
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, "8000", zap.NewNop())
	ctx := context.Background()

	_, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode: "dhcp",
	}, "127.0.0.1:8080")
	if err == nil {
		t.Errorf("expected permission denied error on ApplyConfig")
	}

	if err := svc.Confirm(ctx, "token"); err == nil {
		t.Errorf("expected permission denied error on Confirm")
	}

	if err := svc.Rollback(ctx, "token"); err == nil {
		t.Errorf("expected permission denied error on Rollback")
	}

	if err := svc.ResetNetwork(ctx); err == nil {
		t.Errorf("expected permission denied error on ResetNetwork")
	}
}

func TestServiceStopLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, "8000", zap.NewNop())
	ctx := context.Background()

	// Apply then stop immediately before 300ms delayed apply executes
	_, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.222",
		SubnetMask: "255.255.255.0",
		SetDefault: true,
	}, "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	time.Sleep(350 * time.Millisecond)

	// Since it was stopped before delay elapsed, eth0 shouldn't be 192.168.1.222
	ifaces, _ := provider.ListPhysicalInterfaces(ctx)
	for _, iface := range ifaces {
		if iface.Name == "eth0" && len(iface.IPAddresses) > 0 && iface.IPAddresses[0] == "192.168.1.222/24" {
			t.Errorf("delayed apply should have been canceled by Stop()")
		}
	}
}
