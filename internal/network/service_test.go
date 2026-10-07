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
	svc := NewNetworkService(provider, watchdog, zap.NewNop())

	ctx := context.Background()

	// 1. Client IP matching eth0 (192.168.1.55 within 192.168.1.100/24)
	ifaces, err := svc.ListInterfaces(ctx, "", "", "192.168.1.55:54321")
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
	ifaces2, _ := svc.ListInterfaces(ctx, "", "", "10.0.0.100")
	for _, iface := range ifaces2 {
		if iface.Name == "eth1" && !iface.IsCurrent {
			t.Errorf("expected eth1 to be current for 10.0.0.100")
		}
	}

	// 3. Socket Local Address matching eth0 explicitly
	ifaces3, _ := svc.ListInterfaces(ctx, "192.168.1.100:8080", "localhost", "172.16.0.5")
	for _, iface := range ifaces3 {
		if iface.Name == "eth0" && !iface.IsCurrent {
			t.Errorf("expected eth0 to be current for local socket address 192.168.1.100:8080")
		}
	}
}

func TestServiceValidationAndGatewayConflict(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, zap.NewNop())
	ctx := context.Background()

	// 1. Invalid IP
	_, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "invalid-ip",
		SubnetMask: "255.255.255.0",
	}, "127.0.0.1:8080", "127.0.0.1")
	if err == nil {
		t.Errorf("expected error for invalid IP")
	}

	// 2. Gateway out of subnet
	_, err = svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.100",
		SubnetMask: "255.255.255.0",
		Gateway:    "10.0.0.1", // outside 192.168.1.0/24!
	}, "127.0.0.1:8080", "127.0.0.1")
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
	}, "127.0.0.1:8080", "127.0.0.1")
	if err == nil {
		t.Errorf("expected DEFAULT_GATEWAY_CONFLICT when setting another default gateway on eth1")
	}
}

func TestServiceApplyAndConfirmWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	svc := NewNetworkService(provider, watchdog, zap.NewNop())
	ctx := context.Background()

	// Valid static apply on eth0
	resp, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.150",
		SubnetMask: "255.255.255.0",
		Gateway:    "192.168.1.1",
		DNS:        []string{"8.8.8.8"},
		SetDefault: true,
	}, "192.168.1.100:8080", "192.168.1.50")
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
	svc := NewNetworkService(provider, watchdog, zap.NewNop())
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
	svc := NewNetworkService(provider, watchdog, zap.NewNop())
	ctx := context.Background()

	_, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode: "dhcp",
	}, "127.0.0.1:8080", "127.0.0.1")
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
	svc := NewNetworkService(provider, watchdog, zap.NewNop())
	ctx := context.Background()

	// Apply then stop immediately before 300ms delayed apply executes
	_, err := svc.ApplyConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.222",
		SubnetMask: "255.255.255.0",
		SetDefault: true,
	}, "127.0.0.1:8080", "127.0.0.1")
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
