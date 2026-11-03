package systeminfo

import (
	"testing"
	"time"
)

func TestRingBuffer_Empty(t *testing.T) {
	rb := NewRingBuffer(5)
	if rb.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rb.Len())
	}
	snap := rb.Snapshot()
	if snap == nil || len(snap) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v", snap)
	}
}

func TestRingBuffer_PushAndSnapshot(t *testing.T) {
	rb := NewRingBuffer(3)
	now := time.Now().UTC()

	rb.Push(HardwareHistoryPoint{Timestamp: now.Add(1 * time.Second), CPUUsagePercent: 10})
	rb.Push(HardwareHistoryPoint{Timestamp: now.Add(2 * time.Second), CPUUsagePercent: 20})

	if rb.Len() != 2 {
		t.Fatalf("expected len 2, got %d", rb.Len())
	}

	snap := rb.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 items, got %d", len(snap))
	}
	if snap[0].CPUUsagePercent != 10 || snap[1].CPUUsagePercent != 20 {
		t.Fatalf("unexpected order or data: %+v", snap)
	}

	// Fill to capacity
	rb.Push(HardwareHistoryPoint{Timestamp: now.Add(3 * time.Second), CPUUsagePercent: 30})
	if rb.Len() != 3 {
		t.Fatalf("expected len 3, got %d", rb.Len())
	}

	// Overflow: should evict oldest (10)
	rb.Push(HardwareHistoryPoint{Timestamp: now.Add(4 * time.Second), CPUUsagePercent: 40})
	if rb.Len() != 3 {
		t.Fatalf("expected len 3 after overflow, got %d", rb.Len())
	}

	snap2 := rb.Snapshot()
	if len(snap2) != 3 {
		t.Fatalf("expected 3 items, got %d", len(snap2))
	}
	if snap2[0].CPUUsagePercent != 20 || snap2[1].CPUUsagePercent != 30 || snap2[2].CPUUsagePercent != 40 {
		t.Fatalf("unexpected data after overflow: %+v", snap2)
	}
}
