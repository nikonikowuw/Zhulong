package systeminfo

import (
	"context"
	"testing"
)

func TestCalculateUsage(t *testing.T) {
	prev := RawCPUTimes{
		User:   100,
		System: 50,
		Idle:   850,
	} // Total = 1000, Idle = 850
	curr := RawCPUTimes{
		User:   200,
		System: 100,
		Idle:   1700,
	} // Total = 2000, Idle = 1700
	// Delta Total = 1000, Delta Idle = 850 -> Usage = 15.0%

	usage := CalculateUsage(prev, curr)
	if usage != 15.0 {
		t.Fatalf("expected usage 15.0, got %v", usage)
	}

	// Boundary: identical times
	if zero := CalculateUsage(curr, curr); zero != 0.0 {
		t.Fatalf("expected 0 usage when times unchanged, got %v", zero)
	}

	// Boundary: inverted
	if zero := CalculateUsage(curr, prev); zero != 0.0 {
		t.Fatalf("expected 0 usage when curr <= prev, got %v", zero)
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
		got := formatThermalLabel(tt.input)
		if got != tt.expected {
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

	if temp := findThermalByPattern(thermals, "cpu"); temp != 53.2 {
		t.Fatalf("expected CPU temp 53.2, got %v", temp)
	}
	if temp := findThermalByPattern(thermals, "npu"); temp != 49.8 {
		t.Fatalf("expected NPU temp 49.8, got %v", temp)
	}
	if temp := findThermalByPattern(thermals, "nonexistent"); temp != 0 {
		t.Fatalf("expected 0 for nonexistent thermal, got %v", temp)
	}
}

func TestFallbackCollector(t *testing.T) {
	col := NewFallbackCollector()
	snap, err := col.CollectSnapshot(context.Background())
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
}
