package systeminfo

import (
	"context"
	"testing"
)

func TestCalculateUsage(t *testing.T) {
	prev := rawCPUTimes{
		User:   100,
		System: 50,
		Idle:   850,
	} // Total = 1000, Idle = 850
	curr := rawCPUTimes{
		User:   200,
		System: 100,
		Idle:   1700,
	} // Total = 2000, Idle = 1700
	// Delta Total = 1000, Delta Idle = 850 -> Usage = 15.0%

	if usage := calculateUsage(prev, curr); usage != 15.0 {
		t.Fatalf("expected usage 15.0, got %v", usage)
	}

	// Boundary: identical times
	if zero := calculateUsage(curr, curr); zero != 0.0 {
		t.Fatalf("expected 0 usage when times unchanged, got %v", zero)
	}

	// Boundary: inverted
	if zero := calculateUsage(curr, prev); zero != 0.0 {
		t.Fatalf("expected 0 usage when curr <= prev, got %v", zero)
	}
}

func TestCalculateUsage_ClampsOutOfRange(t *testing.T) {
	// iowait accounted non-monotonically: idle delta exceeds total delta, which would
	// otherwise produce a negative percentage.
	prev := rawCPUTimes{User: 100, Idle: 100, IOWait: 500}
	curr := rawCPUTimes{User: 150, Idle: 100, IOWait: 0}

	if usage := calculateUsage(prev, curr); usage < 0 || usage > 100 {
		t.Fatalf("expected usage clamped into [0,100], got %v", usage)
	}
}

func TestFormatThermalLabel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"soc_thermal", "SoC"},
		{"cpu-thermal", "CPU"},
		{"npu_thermal", "NPU"},
		{"gpu-thermal", "GPU"},
		{"tsadc_channel0", "TSADC"},
		{"battery", "BATTERY"},
		{"", "Thermal"},
	}

	for _, tt := range tests {
		if got := formatThermalLabel(tt.input); got != tt.expected {
			t.Errorf("formatThermalLabel(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFindThermalByPattern(t *testing.T) {
	thermals := []TemperatureInfo{
		{Label: "SoC", Temperature: 50.5},
		{Label: "CPU", Temperature: 53.2},
		{Label: "NPU", Temperature: 49.8},
	}

	tests := []struct {
		keyword   string
		wantTemp  float64
		wantFound bool
	}{
		{keyword: "cpu", wantTemp: 53.2, wantFound: true},
		{keyword: "npu", wantTemp: 49.8, wantFound: true},
		{keyword: "nonexistent", wantTemp: 0, wantFound: false},
	}

	for _, tt := range tests {
		temp, found := findThermalByPattern(thermals, tt.keyword)
		if found != tt.wantFound || temp != tt.wantTemp {
			t.Errorf("findThermalByPattern(%q) = (%v, %v); want (%v, %v)",
				tt.keyword, temp, found, tt.wantTemp, tt.wantFound)
		}
	}
}

func TestFindThermalByPattern_ZeroReadingIsFound(t *testing.T) {
	// 0°C is a legal sensor reading, so presence cannot be inferred from the value.
	if _, found := findThermalByPattern([]TemperatureInfo{{Label: "CPU"}}, "cpu"); !found {
		t.Fatal("a 0°C CPU sensor must still report as found")
	}
}

func TestFallbackCollector(t *testing.T) {
	snap, err := newFallbackCollector().CollectSnapshot(context.Background())
	if err != nil {
		t.Fatalf("fallback collector failed: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.Memory.TotalBytes == 0 {
		t.Fatal("expected positive memory total")
	}
	if len(snap.Storage) == 0 {
		t.Fatal("expected non-empty storage mounts")
	}
	if snap.Network == nil || snap.NPU.CoreLoads == nil || snap.Thermals == nil {
		t.Fatal("fallback snapshot must initialize every slice and map")
	}
}

func TestFallbackCollector_HonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := newFallbackCollector().CollectSnapshot(ctx); err == nil {
		t.Fatal("expected a cancelled context to abort the sample")
	}
}
