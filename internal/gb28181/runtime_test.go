package gb28181

import (
	"testing"
	"time"
)

func TestRuntimeInitialStateIsDisabled(t *testing.T) {
	runtime := NewRuntime()
	status := runtime.Status()
	if status.State != RuntimeStateDisabled {
		t.Fatalf("expected %q by default, got %q", RuntimeStateDisabled, status.State)
	}
	if status.RestartRequired {
		t.Fatal("expected no restart requirement before the first startup")
	}
}

func TestRuntimeRestartRequiredComparesIgnoringTimestamp(t *testing.T) {
	runtime := NewRuntime()
	startup := DefaultConfig()
	runtime.LoadStartup(startup, "192.168.1.10")

	if status := runtime.Status(); status.RestartRequired {
		t.Fatal("desired == startup must not require a restart")
	}

	// Same target, only the audit timestamp moves: still no restart.
	sameTarget := startup
	sameTarget.UpdatedAt = startup.UpdatedAt.Add(time.Hour)
	runtime.SetDesired(sameTarget)
	if status := runtime.Status(); status.RestartRequired {
		t.Fatal("UpdatedAt must not trigger restartRequired")
	}

	// A different target does require one.
	changed := startup
	changed.SIPListen = "0.0.0.0:5070"
	runtime.SetDesired(changed)
	status := runtime.Status()
	if !status.RestartRequired {
		t.Fatal("a changed listener must require a restart")
	}
	// The running view must still report the startup value, not the desired one.
	if status.Startup.SIPListen != startup.SIPListen {
		t.Fatalf("expected startup to keep %q, got %q", startup.SIPListen, status.Startup.SIPListen)
	}
}

func TestRuntimeStatusAgainstUsesRepositoryTarget(t *testing.T) {
	runtime := NewRuntime()
	startup := DefaultConfig()
	runtime.LoadStartup(startup, "")

	external := startup
	external.Enabled = true
	status := runtime.StatusAgainst(external)
	if !status.RestartRequired {
		t.Fatal("expected an out-of-band target change to be reported")
	}
	if status.Desired.Enabled != true {
		t.Fatal("expected the caller-supplied target to be echoed back")
	}
	// The cached desired must not be mutated by a read-only comparison.
	if runtime.Desired().Enabled {
		t.Fatal("StatusAgainst must not overwrite the cached desired configuration")
	}
}

func TestRuntimeSnapshotReflectsCounters(t *testing.T) {
	runtime := NewRuntime()
	startup := DefaultConfig()
	runtime.LoadStartup(startup, "192.168.1.10")
	runtime.SetDeviceCounters(12, 5)
	runtime.SetPortPoolUsage(7, 3)
	runtime.SetState(RuntimeStateRunning, "")

	snapshot := runtime.Snapshot()
	if snapshot.State != RuntimeStateRunning {
		t.Fatalf("expected state %q, got %q", RuntimeStateRunning, snapshot.State)
	}
	if snapshot.Devices.Total != 12 || snapshot.Devices.Online != 5 {
		t.Fatalf("expected 12/5 devices, got %d/%d", snapshot.Devices.Total, snapshot.Devices.Online)
	}
	// 30000..30100 is 101 ports → 50 RTP/RTCP pairs.
	if snapshot.PortPool.PairsTotal != 50 {
		t.Fatalf("expected 50 port pairs, got %d", snapshot.PortPool.PairsTotal)
	}
	if snapshot.PortPool.PairsUsed != 7 || snapshot.PortPool.ExhaustedHits != 3 {
		t.Fatalf("expected 7 used / 3 exhausted, got %d/%d",
			snapshot.PortPool.PairsUsed, snapshot.PortPool.ExhaustedHits)
	}
	if snapshot.GeneratedAt.IsZero() {
		t.Fatal("expected a generation timestamp")
	}
}
