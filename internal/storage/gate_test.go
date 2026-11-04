package storage

import (
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// testStorageConfig is the watermark policy shared by the gate tests.
func testStorageConfig() *StorageConfig {
	return &StorageConfig{
		HighWatermarkPercent: 90,
		LowWatermarkPercent:  80,
		EmergencyStopPercent: 95,
		EmergencyStopMinMB:   2048, // 2GB
	}
}

func TestEmergencyGate_Evaluate(t *testing.T) {
	const (
		gigabyte = uint64(1024 * 1024 * 1024)
		totalGB  = 100
	)

	tests := []struct {
		name         string
		usedGB       uint64
		freeGB       uint64
		readOnly     bool
		dismounted   bool
		want         StorageHealthStatus
		wantCanWrite bool
		wantStops    int32
	}{
		{
			name:         "healthy volume",
			usedGB:       70,
			freeGB:       30,
			want:         StatusHealthy,
			wantCanWrite: true,
		},
		{
			name:         "warning at high watermark",
			usedGB:       92,
			freeGB:       8,
			want:         StatusWarning,
			wantCanWrite: true,
		},
		{
			// ext4 reserved blocks: used/total is 75%, but used/(used+avail) is
			// 93.75% → 94%, so the high watermark must wake before an emergency.
			name:         "warning uses user-available space",
			usedGB:       75,
			freeGB:       5,
			want:         StatusWarning,
			wantCanWrite: true,
		},
		{
			name:         "emergency above usage percent",
			usedGB:       96,
			freeGB:       4,
			want:         StatusEmergencyStopped,
			wantCanWrite: false,
			wantStops:    1,
		},
		{
			name:         "emergency below minimum free space",
			usedGB:       90,
			freeGB:       1,
			want:         StatusEmergencyStopped,
			wantCanWrite: false,
			wantStops:    1,
		},
		{
			name:         "dismount fallthrough hazard",
			usedGB:       70,
			freeGB:       30,
			dismounted:   true,
			want:         StatusError,
			wantCanWrite: false,
		},
		{
			name:         "read-only mount",
			usedGB:       70,
			freeGB:       30,
			readOnly:     true,
			want:         StatusError,
			wantCanWrite: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gate := NewEmergencyGate(zap.NewNop())
			var stops atomic.Int32
			gate.SetCallbacks(
				func(string) { stops.Add(1) },
				func() {},
			)

			stats := &FSStats{
				TotalBytes: totalGB * gigabyte,
				UsedBytes:  tc.usedGB * gigabyte,
				FreeBytes:  tc.freeGB * gigabyte,
				IsReadOnly: tc.readOnly,
			}

			if got := gate.Evaluate(stats, testStorageConfig(), tc.dismounted); got != tc.want {
				t.Errorf("Evaluate() = %q, want %q", got, tc.want)
			}
			if got := gate.CanWrite(); got != tc.wantCanWrite {
				t.Errorf("CanWrite() = %v, want %v", got, tc.wantCanWrite)
			}
			if got := stops.Load(); got != tc.wantStops {
				t.Errorf("stop callbacks = %d, want %d", got, tc.wantStops)
			}
		})
	}
}

func TestEmergencyGate_EvaluateWithoutTelemetry(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	if got := gate.Evaluate(nil, testStorageConfig(), false); got != StatusError {
		t.Errorf("Evaluate(nil stats) = %q, want %q", got, StatusError)
	}
	if gate.CanWrite() {
		t.Error("CanWrite() = true, want false when telemetry is missing")
	}
	if gate.Reason() == "" {
		t.Error("Reason() is empty, want the telemetry failure recorded")
	}
}

func TestEmergencyGate_RecoversFromEmergencyOnce(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var stops atomic.Int32
	var resumes atomic.Int32
	gate.SetCallbacks(
		func(string) { stops.Add(1) },
		func() { resumes.Add(1) },
	)

	const gigabyte = uint64(1024 * 1024 * 1024)
	stats := &FSStats{
		TotalBytes: 100 * gigabyte,
		UsedBytes:  96 * gigabyte,
		FreeBytes:  4 * gigabyte,
	}

	if got := gate.Evaluate(stats, testStorageConfig(), false); got != StatusEmergencyStopped {
		t.Fatalf("Evaluate(emergency) = %q, want %q", got, StatusEmergencyStopped)
	}

	stats.UsedBytes = 75 * gigabyte
	stats.FreeBytes = 25 * gigabyte

	for range 2 {
		if got := gate.Evaluate(stats, testStorageConfig(), false); got != StatusHealthy {
			t.Fatalf("Evaluate(recovered) = %q, want %q", got, StatusHealthy)
		}
		if !gate.CanWrite() {
			t.Fatal("CanWrite() = false after recovery")
		}
	}

	if got := stops.Load(); got != 1 {
		t.Errorf("stop callbacks = %d, want 1", got)
	}
	if got := resumes.Load(); got != 1 {
		t.Errorf("resume callbacks = %d, want 1 (edge-triggered)", got)
	}
}

func TestEmergencyGate_BlockWritesIsEdgeTriggered(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var stops atomic.Int32
	var resumes atomic.Int32
	gate.SetCallbacks(
		func(string) { stops.Add(1) },
		func() { resumes.Add(1) },
	)

	gate.BlockWrites("storage telemetry unavailable")
	gate.BlockWrites("storage telemetry unavailable")

	if gate.CanWrite() {
		t.Error("CanWrite() = true, want false after BlockWrites")
	}
	if !gate.IsEmergency() {
		t.Error("IsEmergency() = false, want true after BlockWrites")
	}
	if got := stops.Load(); got != 1 {
		t.Errorf("stop callbacks = %d, want 1 (edge-triggered)", got)
	}
	if got := resumes.Load(); got != 0 {
		t.Errorf("resume callbacks = %d, want 0 while still blocked", got)
	}
	if gate.Reason() == "" {
		t.Error("Reason() is empty, want the block reason recorded")
	}
}

func TestEmergencyGate_HealthyProbeClearsBlock(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var stops atomic.Int32
	var resumes atomic.Int32
	gate.SetCallbacks(
		func(string) { stops.Add(1) },
		func() { resumes.Add(1) },
	)

	gate.BlockWrites("storage telemetry unavailable")

	const gigabyte = uint64(1024 * 1024 * 1024)
	stats := &FSStats{
		TotalBytes: 100 * gigabyte,
		UsedBytes:  50 * gigabyte,
		FreeBytes:  50 * gigabyte,
	}

	if got := gate.Evaluate(stats, testStorageConfig(), false); got != StatusHealthy {
		t.Fatalf("Evaluate() = %q, want %q", got, StatusHealthy)
	}
	if !gate.CanWrite() {
		t.Error("CanWrite() = false, want true after a healthy probe")
	}
	if gate.IsEmergency() {
		t.Error("IsEmergency() = true, want false after a healthy probe")
	}
	if got := gate.Reason(); got != "" {
		t.Errorf("Reason() = %q, want empty after a healthy probe", got)
	}
	if got := stops.Load(); got != 1 {
		t.Errorf("stop callbacks = %d, want 1", got)
	}
	if got := resumes.Load(); got != 1 {
		t.Errorf("resume callbacks = %d, want 1", got)
	}
}

// TestEmergencyGateReportsWriteStateEdges pins the observer contract: consumers
// shed work on the transition only, because Evaluate runs on every storage tick and
// a report per tick would be the volume those consumers are trying to remove.
func TestEmergencyGateReportsWriteStateEdges(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var states []bool
	gate.RegisterObserver(func(blocked bool) { states = append(states, blocked) })

	const gigabyte = uint64(1024 * 1024 * 1024)
	cfg := testStorageConfig()
	emergency := &FSStats{TotalBytes: 100 * gigabyte, UsedBytes: 96 * gigabyte, FreeBytes: 4 * gigabyte}
	healthy := &FSStats{TotalBytes: 100 * gigabyte, UsedBytes: 70 * gigabyte, FreeBytes: 30 * gigabyte}

	gate.Evaluate(healthy, cfg, false)
	if len(states) != 0 {
		t.Fatalf("healthy probe reported a transition: %v", states)
	}
	gate.Evaluate(emergency, cfg, false)
	gate.Evaluate(emergency, cfg, false)
	gate.Evaluate(healthy, cfg, false)
	gate.Evaluate(healthy, cfg, false)

	if want := []bool{true, false}; !equalBoolSlices(states, want) {
		t.Fatalf("write state transitions = %v, want %v", states, want)
	}
}

// TestEmergencyGateReportsTelemetryBlockOnce covers the path that does not swap the
// emergency flag: a lost mount must still reach observers, and repeatedly blocked
// telemetry must not echo.
func TestEmergencyGateReportsTelemetryBlockOnce(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var states []bool
	gate.RegisterObserver(func(blocked bool) { states = append(states, blocked) })

	gate.Evaluate(nil, testStorageConfig(), false)
	gate.Evaluate(nil, testStorageConfig(), false)
	gate.BlockWrites("telemetry unavailable")
	gate.BlockWrites("telemetry unavailable again")

	if want := []bool{true}; !equalBoolSlices(states, want) {
		t.Fatalf("write state transitions = %v, want %v", states, want)
	}
}

// TestEmergencyGateKeepsAuditCallbacksWithObservers proves RegisterObserver is
// additive: installing an observer must not displace SetCallbacks, which is what the
// audit trail depends on.
func TestEmergencyGateKeepsAuditCallbacksWithObservers(t *testing.T) {
	gate := NewEmergencyGate(zap.NewNop())

	var stops atomic.Int32
	gate.SetCallbacks(func(string) { stops.Add(1) }, nil)
	var observed atomic.Int32
	gate.RegisterObserver(func(blocked bool) {
		if blocked {
			observed.Add(1)
		}
	})

	const gigabyte = uint64(1024 * 1024 * 1024)
	gate.Evaluate(&FSStats{TotalBytes: 100 * gigabyte, UsedBytes: 96 * gigabyte, FreeBytes: 4 * gigabyte},
		testStorageConfig(), false)

	if stops.Load() != 1 || observed.Load() != 1 {
		t.Fatalf("stops = %d, observed = %d, want 1 and 1", stops.Load(), observed.Load())
	}
}

func equalBoolSlices(got, want []bool) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
