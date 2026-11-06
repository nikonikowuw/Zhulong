package systeminfo

import (
	"context"
	"math"
	"strings"
	"time"
)

// rawCPUTimes captures tick counters from /proc/stat for one processor or total.
type rawCPUTimes struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	IOWait  uint64
	IRQ     uint64
	SoftIRQ uint64
	Steal   uint64
}

// totalTicks returns the aggregate ticks across all accounted CPU categories.
func (t rawCPUTimes) totalTicks() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// idleTicks returns the aggregate idle ticks (idle + iowait).
func (t rawCPUTimes) idleTicks() uint64 {
	return t.Idle + t.IOWait
}

// calculateUsage computes the utilization percentage between two CPU time samples.
//
// Kernel counters are monotonic, but a snapshot pair can still arrive out of order
// (NTP step, 32-bit tick wrap on older kernels), so a non-advancing total reports 0
// instead of a negative or NaN percentage.
func calculateUsage(prev, curr rawCPUTimes) float64 {
	prevTotal := prev.totalTicks()
	currTotal := curr.totalTicks()
	if currTotal <= prevTotal {
		return 0
	}

	// iowait can be accounted non-monotonically on some kernels; clamping to the
	// previous reading keeps the idle delta inside the total delta.
	currIdle := max(curr.idleTicks(), prev.idleTicks())
	idleDelta := currIdle - prev.idleTicks()
	totalDelta := currTotal - prevTotal

	usage := (1 - float64(idleDelta)/float64(totalDelta)) * 100
	return roundPercent(min(max(usage, 0), 100))
}

// roundPercent keeps a single decimal so the UI never renders 33.33333333333333%.
func roundPercent(value float64) float64 {
	return math.Round(value*10) / 10
}

// rawNetworkInterface holds byte counters from /proc/net/dev.
type rawNetworkInterface struct {
	Interface    string
	RxTotalBytes uint64
	TxTotalBytes uint64
	IsUp         bool
}

// rawHardwareSnapshot captures instantaneous hardware counter states.
type rawHardwareSnapshot struct {
	Timestamp time.Time
	Host      HostInfo
	CPUTotal  rawCPUTimes
	CPUCores  []rawCPUTimes
	CPUModel  string
	Memory    MemoryStatus
	Storage   []StorageMountStatus
	Network   map[string]rawNetworkInterface
	NPU       NPUStatus
	Thermals  []TemperatureInfo
}

// collector probes system hardware counters.
//
// Implementations must return initialized slices and maps, never nil: the JSON
// envelope promises [] rather than null, and a nil here would leak straight to the
// API. A collector may also leave optional values at their zero state — every
// /proc or /sys node it cannot read means "this host does not expose it", not an
// error, unless it is a node whose truncation would publish half a sample (see
// scanLines).
type collector interface {
	CollectSnapshot(ctx context.Context) (*rawHardwareSnapshot, error)
}

// newDefaultCollector returns the platform-appropriate collector.
func newDefaultCollector() collector {
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

// findThermalByPattern returns the temperature of the first sensor whose label
// contains keyword (case-insensitive). ok is false when nothing matched: 0°C is a
// legal reading, so the temperature itself cannot double as the "not found" sentinel.
func findThermalByPattern(thermals []TemperatureInfo, keyword string) (float64, bool) {
	lowerKeyword := strings.ToLower(keyword)
	for _, thermal := range thermals {
		if strings.Contains(strings.ToLower(thermal.Label), lowerKeyword) {
			return thermal.Temperature, true
		}
	}
	return 0, false
}
