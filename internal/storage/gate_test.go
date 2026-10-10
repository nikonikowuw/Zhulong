package storage

import (
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

func TestEmergencyGate_Evaluate(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var stopCalls atomic.Int32
	var resumeCalls atomic.Int32
	gate.SetCallbacks(
		func(string) { stopCalls.Add(1) },
		func() { resumeCalls.Add(1) },
	)

	cfg := &StorageConfig{
		HighWatermarkPercent: 90,
		LowWatermarkPercent:  80,
		EmergencyStopPercent: 95,
		EmergencyStopMinMB:   2048, // 2GB
	}

	// 1. Healthy: 70% used, 30GB free
	stats := &FSStats{
		TotalBytes: 100 * 1024 * 1024 * 1024,
		UsedBytes:  70 * 1024 * 1024 * 1024,
		FreeBytes:  30 * 1024 * 1024 * 1024,
	}
	status := gate.Evaluate(stats, cfg, false)
	if status != StatusHealthy {
		t.Errorf("expected StatusHealthy, got %v", status)
	}
	if !gate.CanWrite() {
		t.Errorf("expected CanWrite to be true")
	}

	// 2. Warning: 92% used (>= 90% high watermark, but < 95% emergency)
	stats.UsedBytes = 92 * 1024 * 1024 * 1024
	stats.FreeBytes = 8 * 1024 * 1024 * 1024
	status = gate.Evaluate(stats, cfg, false)
	if status != StatusWarning {
		t.Errorf("expected StatusWarning, got %v", status)
	}
	if !gate.CanWrite() {
		t.Errorf("expected CanWrite to be true")
	}

	// 3. Emergency: 96% used (>= 95% emergency stop)
	stats.UsedBytes = 96 * 1024 * 1024 * 1024
	stats.FreeBytes = 4 * 1024 * 1024 * 1024
	status = gate.Evaluate(stats, cfg, false)
	if status != StatusEmergencyStopped {
		t.Errorf("expected StatusEmergencyStopped, got %v", status)
	}
	if gate.CanWrite() {
		t.Errorf("expected CanWrite to be false")
	}
	if stopCalls.Load() != 1 {
		t.Errorf("expected stopCalls to be 1, got %d", stopCalls.Load())
	}

	// 4. Recovery: drops back to 75% used, 25GB free
	stats.UsedBytes = 75 * 1024 * 1024 * 1024
	stats.FreeBytes = 25 * 1024 * 1024 * 1024
	status = gate.Evaluate(stats, cfg, false)
	if status != StatusHealthy {
		t.Errorf("expected StatusHealthy after recovery, got %v", status)
	}
	if !gate.CanWrite() {
		t.Errorf("expected CanWrite to be true after recovery")
	}
	if resumeCalls.Load() != 1 {
		t.Errorf("expected resumeCalls to be 1, got %d", resumeCalls.Load())
	}

	// 5. Emergency by free bytes threshold (< 2GB) even if percentage < 95%
	stats.UsedBytes = 90 * 1024 * 1024 * 1024
	stats.FreeBytes = 1 * 1024 * 1024 * 1024 // 1GB free (< 2GB)
	status = gate.Evaluate(stats, cfg, false)
	if status != StatusEmergencyStopped {
		t.Errorf("expected StatusEmergencyStopped due to low free space, got %v", status)
	}
	if gate.CanWrite() {
		t.Errorf("expected CanWrite to be false")
	}

	// 6. Dismount / Fallthrough hazard
	status = gate.Evaluate(stats, cfg, true)
	if status != StatusError {
		t.Errorf("expected StatusError for dismount, got %v", status)
	}
	if gate.CanWrite() {
		t.Errorf("expected CanWrite to be false on dismount")
	}

	// 7. Read-only filesystem
	stats.IsReadOnly = true
	status = gate.Evaluate(stats, cfg, false)
	if status != StatusError {
		t.Errorf("expected StatusError for read-only, got %v", status)
	}
	if gate.CanWrite() {
		t.Errorf("expected CanWrite to be false on read-only")
	}
}
