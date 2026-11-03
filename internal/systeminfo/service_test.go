package systeminfo

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

type mockCollector struct {
	mu        sync.Mutex
	snapCount int
}

func (m *mockCollector) CollectSnapshot(ctx context.Context) (*RawHardwareSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapCount++

	now := time.Now().UTC()
	return &RawHardwareSnapshot{
		Timestamp: now,
		Host: HostInfo{
			Hostname:      "mock-box",
			OS:            "linux",
			Arch:          "arm64",
			KernelVersion: "5.10.0",
			UptimeSeconds: 120,
		},
		CPUTotal: RawCPUTimes{
			User:   uint64(100 * m.snapCount),
			System: uint64(50 * m.snapCount),
			Idle:   uint64(850 * m.snapCount),
		},
		CPUCores: []RawCPUTimes{
			{
				User:   uint64(50 * m.snapCount),
				System: uint64(25 * m.snapCount),
				Idle:   uint64(425 * m.snapCount),
			},
		},
		CPUModel: "Mock Cortex-A55",
		Memory: MemoryStatus{
			TotalBytes:     4000000000,
			UsedBytes:      2000000000,
			AvailableBytes: 2000000000,
			UsedPercent:    50.0,
		},
		Storage: []StorageMountStatus{
			{
				MountPoint:  "/",
				Device:      "/dev/mmcblk0p1",
				FSType:      "ext4",
				TotalBytes:  32000000000,
				UsedBytes:   16000000000,
				FreeBytes:   16000000000,
				UsedPercent: 50.0,
			},
		},
		Network: map[string]RawNetworkInterface{
			"wlan0": {
				Interface:    "wlan0",
				RxTotalBytes: uint64(500000 * m.snapCount),
				TxTotalBytes: uint64(250000 * m.snapCount),
				IsUp:         true,
			},
			"eth0": {
				Interface:    "eth0",
				RxTotalBytes: uint64(1000000 * m.snapCount),
				TxTotalBytes: uint64(500000 * m.snapCount),
				IsUp:         true,
			},
			"can0": {
				Interface:    "can0",
				RxTotalBytes: uint64(10000 * m.snapCount),
				TxTotalBytes: uint64(5000 * m.snapCount),
				IsUp:         false,
			},
		},
		NPU: NPUStatus{
			Supported:   true,
			Name:        "Rockchip NPU",
			LoadPercent: 25.0,
			CoreLoads:   []float64{25.0},
			Temperature: 48.0,
		},
		Thermals: []TemperatureInfo{
			{Label: "CPU", Temperature: 50.0},
			{Label: "NPU", Temperature: 48.0},
		},
	}, nil
}

func TestService_SampleAndGetHardwareStatus(t *testing.T) {
	logger := zaptest.NewLogger(t)
	col := &mockCollector{}
	svc := NewService(col, logger)

	// Before Start: GetHardwareStatus should trigger initial sample
	status, err := svc.GetHardwareStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Host.Hostname != "mock-box" {
		t.Fatalf("expected hostname mock-box, got %q", status.Host.Hostname)
	}
	if len(status.History) != 1 {
		t.Fatalf("expected 1 history point, got %d", len(status.History))
	}
	isSorted := len(status.Network) == 3 &&
		status.Network[0].Interface == "can0" &&
		status.Network[1].Interface == "eth0" &&
		status.Network[2].Interface == "wlan0"
	if !isSorted {
		t.Fatalf("expected network interfaces sorted deterministically [can0, eth0, wlan0], got %+v", status.Network)
	}

	// Start service and let it sample once
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("failed to start service: %v", err)
	}

	// Trigger manual sample to simulate second sample
	_ = svc.sample()

	status2, err := svc.GetHardwareStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status2.CPU.UsagePercent != 15.0 {
		t.Fatalf("expected 15.0%% CPU usage, got %v", status2.CPU.UsagePercent)
	}
	if len(status2.History) < 2 {
		t.Fatalf("expected at least 2 history points, got %d", len(status2.History))
	}

	// Test concurrent access under load
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.GetHardwareStatus(context.Background())
		}()
	}
	wg.Wait()

	// Stop cleanly and test idempotency
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop service: %v", err)
	}
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("expected second Stop call to be idempotent without error, got: %v", err)
	}
}
