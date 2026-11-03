package systeminfo

import (
	"context"
	"math"
	"strings"
	"time"
)

// RawCPUTimes captures tick counters from /proc/stat for one processor or total.
type RawCPUTimes struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	IOWait  uint64
	IRQ     uint64
	SoftIRQ uint64
	Steal   uint64
}

// TotalTicks returns the aggregate ticks across all accounted CPU categories.
func (t RawCPUTimes) TotalTicks() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// IdleTicks returns the aggregate idle ticks (idle + iowait).
func (t RawCPUTimes) IdleTicks() uint64 {
	return t.Idle + t.IOWait
}

// CalculateUsage calculates the percentage between previous and current CPU times.
func CalculateUsage(prev, curr RawCPUTimes) float64 {
	prevTotal := prev.TotalTicks()
	currTotal := curr.TotalTicks()
	if currTotal <= prevTotal {
		return 0.0
	}

	prevIdle := prev.IdleTicks()
	currIdle := curr.IdleTicks()
	if currIdle < prevIdle {
		currIdle = prevIdle
	}

	deltaTotal := float64(currTotal - prevTotal)
	deltaIdle := float64(currIdle - prevIdle)

	usage := (1.0 - (deltaIdle / deltaTotal)) * 100.0
	if usage < 0.0 {
		return 0.0
	}
	if usage > 100.0 {
		return 100.0
	}
	return math.Round(usage*10) / 10
}

// RawNetworkInterface holds byte counters from /proc/net/dev.
type RawNetworkInterface struct {
	Interface    string
	RxTotalBytes uint64
	TxTotalBytes uint64
	IsUp         bool
}

// RawHardwareSnapshot captures instantaneous hardware counter states.
type RawHardwareSnapshot struct {
	Timestamp time.Time
	Host      HostInfo
	CPUTotal  RawCPUTimes
	CPUCores  []RawCPUTimes
	CPUModel  string
	Memory    MemoryStatus
	Storage   []StorageMountStatus
	Network   map[string]RawNetworkInterface
	NPU       NPUStatus
	Thermals  []TemperatureInfo
}

// Collector defines the interface for probing system hardware counters.
type Collector interface {
	CollectSnapshot(ctx context.Context) (*RawHardwareSnapshot, error)
}

// NewDefaultCollector returns the platform-appropriate Collector.
func NewDefaultCollector() Collector {
	return newPlatformCollector()
}

func formatThermalLabel(rawType string) string {
	rawType = strings.TrimSpace(rawType)
	lower := strings.ToLower(rawType)
	switch {
	case strings.Contains(lower, "cpu"):
		return "CPU"
	case strings.Contains(lower, "npu"):
		return "NPU"
	case strings.Contains(lower, "gpu"):
		return "GPU"
	case strings.Contains(lower, "soc"):
		return "SoC"
	case strings.Contains(lower, "tsadc"):
		return "TSADC"
	default:
		if rawType == "" {
			return "Thermal"
		}
		return strings.ToUpper(rawType)
	}
}

func findThermalByPattern(thermals []TemperatureInfo, keyword string) float64 {
	lowerKey := strings.ToLower(keyword)
	for _, t := range thermals {
		if strings.Contains(strings.ToLower(t.Label), lowerKey) {
			return t.Temperature
		}
	}
	return 0
}
