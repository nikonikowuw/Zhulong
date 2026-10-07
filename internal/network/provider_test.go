package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestCIDRConversion(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		mask     string
		expected string
		wantErr  bool
	}{
		{
			name:     "valid /24",
			ip:       "192.168.1.100",
			mask:     "255.255.255.0",
			expected: "192.168.1.100/24",
			wantErr:  false,
		},
		{
			name:     "valid /16",
			ip:       "172.16.0.5",
			mask:     "255.255.0.0",
			expected: "172.16.0.5/16",
			wantErr:  false,
		},
		{
			name:     "valid /8",
			ip:       "10.0.0.1",
			mask:     "255.0.0.0",
			expected: "10.0.0.1/8",
			wantErr:  false,
		},
		{
			name:     "valid /30",
			ip:       "192.168.1.1",
			mask:     "255.255.255.252",
			expected: "192.168.1.1/30",
			wantErr:  false,
		},
		{
			name:    "invalid IP",
			ip:      "999.999.999.999",
			mask:    "255.255.255.0",
			wantErr: true,
		},
		{
			name:    "invalid mask",
			ip:      "192.168.1.100",
			mask:    "255.0.255.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := CIDRFromIPAndMask(tt.ip, tt.mask)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil result: %s", res)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, res)
			}

			// Inverse test
			parsedIP, parsedMask, err := ParseCIDRToIPAndMask(res)
			if err != nil {
				t.Fatalf("ParseCIDRToIPAndMask failed: %v", err)
			}
			if parsedIP != tt.ip {
				t.Errorf("parsed IP: expected %s, got %s", tt.ip, parsedIP)
			}
			if parsedMask != tt.mask {
				t.Errorf("parsed mask: expected %s, got %s", tt.mask, parsedMask)
			}
		})
	}
}

func TestMockProviderLifecycle(t *testing.T) {
	ctx := context.Background()
	provider := NewMockProvider(zap.NewNop())

	ifaces, err := provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		t.Fatalf("ListPhysicalInterfaces failed: %v", err)
	}
	if len(ifaces) < 2 {
		t.Fatalf("expected at least 2 mock interfaces, got %d", len(ifaces))
	}

	// Apply static config to eth0
	err = provider.ApplyInterfaceConfig(ctx, "eth0", InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.10.50",
		SubnetMask: "255.255.255.0",
		Gateway:    "192.168.10.1",
		DNS:        []string{"8.8.8.8"},
		SetDefault: true,
	})
	if err != nil {
		t.Fatalf("ApplyInterfaceConfig failed: %v", err)
	}

	// Verify updated state
	updated, err := provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		t.Fatalf("ListPhysicalInterfaces failed: %v", err)
	}
	var eth0 InterfaceInfo
	for _, iface := range updated {
		if iface.Name == "eth0" {
			eth0 = iface
			break
		}
	}
	if eth0.Mode != "static" {
		t.Errorf("expected mode 'static', got %s", eth0.Mode)
	}
	if len(eth0.IPAddresses) == 0 || eth0.IPAddresses[0] != "192.168.10.50/24" {
		t.Errorf("expected IP '192.168.10.50/24', got %v", eth0.IPAddresses)
	}
	if !eth0.IsDefaultGW {
		t.Errorf("expected eth0 to be default gateway")
	}

	// Reset eth0 to maintenance
	err = provider.ResetInterfaceToMaintenance(ctx, "eth0")
	if err != nil {
		t.Fatalf("ResetInterfaceToMaintenance failed: %v", err)
	}

	updated, _ = provider.ListPhysicalInterfaces(ctx)
	for _, iface := range updated {
		if iface.Name == "eth0" {
			if len(iface.IPAddresses) == 0 || iface.IPAddresses[0] != "192.168.1.168/24" {
				t.Errorf("expected maintenance IP '192.168.1.168/24', got %v", iface.IPAddresses)
			}
		}
	}
}

func TestDetectProviderFallback(t *testing.T) {
	logger := zap.NewNop()
	// Test non-existent custom script falls back
	p := DetectProvider("/path/that/does/not/exist.sh", logger)
	if p == nil {
		t.Fatal("expected provider, got nil")
	}

	// Test valid executable script detected
	tmpDir := t.TempDir()
	scriptFile := filepath.Join(tmpDir, "mock_network_hook.sh")
	if err := os.WriteFile(scriptFile, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	sp := DetectProvider(scriptFile, logger)
	if sp.Name() != string(ProviderCustomScript) {
		t.Errorf("expected %s, got %s", ProviderCustomScript, sp.Name())
	}
}
