package systeminfo

import (
	"testing"
)

func TestRingBuffer_Empty(t *testing.T) {
	rb := newRingBuffer(5)
	if rb.length() != 0 {
		t.Fatalf("expected len 0, got %d", rb.length())
	}
	snap := rb.snapshot()
	if snap == nil || len(snap) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v", snap)
	}
}

func TestRingBuffer_PushAndSnapshot(t *testing.T) {
	rb := newRingBuffer(3)

	rb.push(HardwareHistoryPoint{CPUUsagePercent: 10})
	rb.push(HardwareHistoryPoint{CPUUsagePercent: 20})

	if rb.length() != 2 {
		t.Fatalf("expected len 2, got %d", rb.length())
	}

	snap := rb.snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 items, got %d", len(snap))
	}
	if snap[0].CPUUsagePercent != 10 || snap[1].CPUUsagePercent != 20 {
		t.Fatalf("unexpected order or data: %+v", snap)
	}

	// Fill to capacity
	rb.push(HardwareHistoryPoint{CPUUsagePercent: 30})
	if rb.length() != 3 {
		t.Fatalf("expected len 3, got %d", rb.length())
	}

	// Overflow: should evict oldest (10)
	rb.push(HardwareHistoryPoint{CPUUsagePercent: 40})
	if rb.length() != 3 {
		t.Fatalf("expected len 3 after overflow, got %d", rb.length())
	}

	snap2 := rb.snapshot()
	if len(snap2) != 3 {
		t.Fatalf("expected 3 items, got %d", len(snap2))
	}
	if snap2[0].CPUUsagePercent != 20 || snap2[1].CPUUsagePercent != 30 || snap2[2].CPUUsagePercent != 40 {
		t.Fatalf("unexpected data after overflow: %+v", snap2)
	}
}

func TestRingBuffer_NonPositiveCapacityDegradesToOne(t *testing.T) {
	// A zero capacity would panic on make(); the buffer must stay usable instead.
	for _, capacity := range []int{0, -1} {
		rb := newRingBuffer(capacity)
		rb.push(HardwareHistoryPoint{CPUUsagePercent: 1})
		rb.push(HardwareHistoryPoint{CPUUsagePercent: 2})

		snap := rb.snapshot()
		if len(snap) != 1 || snap[0].CPUUsagePercent != 2 {
			t.Fatalf("capacity %d: expected only the newest point, got %+v", capacity, snap)
		}
	}
}

func TestRingBuffer_SnapshotIsCopy(t *testing.T) {
	rb := newRingBuffer(2)
	rb.push(HardwareHistoryPoint{CPUUsagePercent: 10})

	snap := rb.snapshot()
	snap[0].CPUUsagePercent = 99

	if got := rb.snapshot()[0].CPUUsagePercent; got != 10 {
		t.Fatalf("callers must not mutate the stored point, got %v", got)
	}
}
