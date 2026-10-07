package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestWatchdogBeginConfirm(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())

	txFile := filepath.Join(tmpDir, TransactionFileName)
	if _, err := os.Stat(txFile); !os.IsNotExist(err) {
		t.Fatalf("expected transaction file not to exist initially")
	}

	state := TransactionState{
		TransactionID: "tx_001",
		InterfaceName: "eth0",
		ConfirmToken:  "test_token_123",
		TargetURL:     "http://192.168.1.50:8080/#settings",
		TimeoutSec:    2,
		RollbackCfg: InterfaceConfig{
			Mode:      "static",
			IPAddress: "192.168.1.100",
		},
	}

	if err := watchdog.BeginTransaction(state); err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Verify file was written
	if _, err := os.Stat(txFile); err != nil {
		t.Fatalf("expected transaction file to exist on disk: %v", err)
	}

	active := watchdog.GetActiveTransaction()
	if active == nil || active.TransactionID != "tx_001" {
		t.Fatalf("expected active transaction tx_001, got %v", active)
	}

	// Confirm with wrong token
	if err := watchdog.ConfirmTransaction("wrong_token"); err == nil {
		t.Fatalf("expected error confirming with wrong token, got nil")
	}

	// Confirm with correct token
	if err := watchdog.ConfirmTransaction("test_token_123"); err != nil {
		t.Fatalf("ConfirmTransaction failed: %v", err)
	}

	// Verify file is removed
	if _, err := os.Stat(txFile); !os.IsNotExist(err) {
		t.Fatalf("expected transaction file to be deleted after confirm")
	}
	if watchdog.GetActiveTransaction() != nil {
		t.Fatalf("expected active transaction to be cleared")
	}
}

func TestWatchdogTimeoutRollback(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())

	state := TransactionState{
		TransactionID: "tx_timeout_test",
		InterfaceName: "eth0",
		ConfirmToken:  "tok_xyz",
		TimeoutSec:    1, // 1 second timeout
		RollbackCfg: InterfaceConfig{
			Mode:       "static",
			IPAddress:  "192.168.1.100",
			SubnetMask: "255.255.255.0",
		},
	}

	if err := watchdog.BeginTransaction(state); err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Wait for timer to trigger rollback (1s + margin)
	time.Sleep(1200 * time.Millisecond)

	// Transaction should have been cleared by rollback
	if watchdog.GetActiveTransaction() != nil {
		t.Fatalf("expected transaction to be rolled back and cleared after timeout")
	}

	txFile := filepath.Join(tmpDir, TransactionFileName)
	if _, err := os.Stat(txFile); !os.IsNotExist(err) {
		t.Fatalf("expected transaction file to be removed after rollback")
	}
}

func TestWatchdogBootAutoRollback(t *testing.T) {
	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())

	// Simulate power loss by creating transaction file on disk directly
	state := TransactionState{
		TransactionID: "tx_power_cut",
		Status:        "pending_confirm",
		InterfaceName: "eth0",
		ConfirmToken:  "token_reboot",
		RollbackCfg: InterfaceConfig{
			Mode:       "static",
			IPAddress:  "192.168.1.99",
			SubnetMask: "255.255.255.0",
		},
	}

	watchdog1 := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	if err := watchdog1.BeginTransaction(state); err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Simulate system restart with new WatchdogManager instance
	watchdog2 := NewWatchdogManager(tmpDir, provider, zap.NewNop())

	// Check on boot
	ctx := context.Background()
	if err := watchdog2.CheckPendingBootTransaction(ctx); err != nil {
		t.Fatalf("CheckPendingBootTransaction failed: %v", err)
	}

	// Verify rollback took effect on provider
	ifaces, _ := provider.ListPhysicalInterfaces(ctx)
	var eth0 InterfaceInfo
	for _, iface := range ifaces {
		if iface.Name == "eth0" {
			eth0 = iface
			break
		}
	}
	if len(eth0.IPAddresses) == 0 || eth0.IPAddresses[0] != "192.168.1.99/24" {
		t.Fatalf("expected rolled back IP 192.168.1.99/24, got %v", eth0.IPAddresses)
	}

	// File should be cleaned up
	txFile := filepath.Join(tmpDir, TransactionFileName)
	if _, err := os.Stat(txFile); !os.IsNotExist(err) {
		t.Fatalf("expected transaction file to be deleted after boot rollback")
	}
}
