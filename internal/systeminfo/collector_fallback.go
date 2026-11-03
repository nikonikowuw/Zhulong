package systeminfo

import (
	"context"
	"os"
	"runtime"
	"time"
)

// FallbackCollector provides simulated telemetry for non-Linux platforms (e.g. macOS development).
type FallbackCollector struct{}

// NewFallbackCollector creates a new fallback hardware telemetry collector.
func NewFallbackCollector() *FallbackCollector {
	return &FallbackCollector{}
}

// CollectSnapshot returns simulated system hardware metrics.
func (c *FallbackCollector) CollectSnapshot(ctx context.Context) (*RawHardwareSnapshot, error) {
	now := time.Now().UTC()
	hostname, _ := os.Hostname()

	return &RawHardwareSnapshot{
		Timestamp: now,
		Host: HostInfo{
			Hostname:      hostname,
			OS:            runtime.GOOS,
			Arch:          runtime.GOARCH,
			KernelVersion: "dev-simulated",
			UptimeSeconds: 3600,
			BootTime:      now.Add(-1 * time.Hour),
			LoadAvg:       [3]float64{0.85, 0.72, 0.65},
		},
		CPUTotal: RawCPUTimes{
			User:   12000,
			Nice:   0,
			System: 4000,
			Idle:   24000,
		},
		CPUCores: []RawCPUTimes{
			{User: 3000, System: 1000, Idle: 6000},
			{User: 3000, System: 1000, Idle: 6000},
			{User: 3000, System: 1000, Idle: 6000},
			{User: 3000, System: 1000, Idle: 6000},
		},
		CPUModel: runtime.GOARCH + " (Simulated)",
		Memory: MemoryStatus{
			TotalBytes:      16 * 1024 * 1024 * 1024,
			UsedBytes:       8 * 1024 * 1024 * 1024,
			AvailableBytes:  8 * 1024 * 1024 * 1024,
			FreeBytes:       4 * 1024 * 1024 * 1024,
			BuffersBytes:    1 * 1024 * 1024 * 1024,
			CachedBytes:     3 * 1024 * 1024 * 1024,
			UsedPercent:     50.0,
			SwapTotalBytes:  0,
			SwapUsedBytes:   0,
			SwapUsedPercent: 0,
		},
		Storage: []StorageMountStatus{
			{
				MountPoint:  "/",
				Device:      "/dev/root",
				FSType:      "apfs",
				TotalBytes:  256 * 1024 * 1024 * 1024,
				UsedBytes:   128 * 1024 * 1024 * 1024,
				FreeBytes:   128 * 1024 * 1024 * 1024,
				UsedPercent: 50.0,
			},
		},
		Network: map[string]RawNetworkInterface{
			"en0": {
				Interface:    "en0",
				RxTotalBytes: 1024 * 1024 * 1024,
				TxTotalBytes: 512 * 1024 * 1024,
				IsUp:         true,
			},
		},
		NPU: NPUStatus{
			Supported:   false,
			Name:        "None",
			LoadPercent: 0,
			CoreLoads:   []float64{},
			Temperature: 0,
		},
		Thermals: []TemperatureInfo{
			{Label: "CPU", Temperature: 45.0},
			{Label: "SoC", Temperature: 46.0},
		},
	}, nil
}
