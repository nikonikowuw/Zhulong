package storage

import "testing"

func TestUsagePercent(t *testing.T) {
	tests := []struct {
		name string
		used uint64
		free uint64
		want int
	}{
		{name: "reserved blocks round up", used: 85, free: 5, want: 95},
		{name: "root-like geometry", used: 85, free: 15, want: 85},
		{name: "empty volume", used: 0, free: 0, want: 0},
		{name: "zero free", used: 10, free: 0, want: 100},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := usagePercent(&FSStats{UsedBytes: tc.used, FreeBytes: tc.free, TotalBytes: 100})
			if got != tc.want {
				t.Fatalf("usagePercent(used=%d, free=%d) = %d, want %d", tc.used, tc.free, got, tc.want)
			}
		})
	}

	if got := usagePercent(nil); got != 0 {
		t.Fatalf("usagePercent(nil) = %d, want 0", got)
	}
}
