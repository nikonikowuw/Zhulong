package camera

import (
	"testing"
	"time"
)

func TestStateRegistryLifecycleAndAggregation(t *testing.T) {
	registry := NewStateRegistry()

	cam := &Camera{
		ID:      "cam_gate",
		Name:    "Gate Camera",
		Enabled: true,
		Streams: []CameraStream{
			{Role: StreamRoleMain},
			{Role: StreamRoleSub},
		},
	}

	// 1. Initial State
	state := registry.InitCameraState(cam)
	if state.Health != HealthStateUnknown || state.Reason != "pending_check" {
		t.Fatalf("expected initial unknown pending_check, got: %+v", state)
	}

	// 2. Main stream success -> Camera becomes online
	state = registry.RecordStreamSuccess("cam_gate", 0, StreamRoleMain, EvidenceRTSPDescribe)
	if state.Health != HealthStateOnline || state.Degraded {
		t.Fatalf("expected online not degraded, got: %+v", state)
	}

	// 3. Sub stream failure -> Camera remains online but becomes degraded
	state = registry.RecordStreamFailure("cam_gate", 0, StreamRoleSub, "connect_failed", "connection refused", false)
	if state.Health != HealthStateOnline || !state.Degraded {
		t.Fatalf("expected online and degraded, got: %+v", state)
	}

	// 4. Main stream failure -> Camera becomes error or offline, degraded clears
	state = registry.RecordStreamFailure("cam_gate", 0, StreamRoleMain, "auth_failed", "bad password", false)
	if state.Health == HealthStateOnline {
		t.Fatalf("expected main failure to bring camera out of online, got: %+v", state)
	}

	// 5. Disabled camera
	cam.Enabled = false
	state = registry.InitCameraState(cam)
	if state.Health != HealthStateUnknown || state.Reason != "disabled" {
		t.Fatalf("expected disabled state unknown, got: %+v", state)
	}
}

func TestStateRegistryConsecutiveFailuresThreshold(t *testing.T) {
	registry := NewStateRegistry()
	cam := &Camera{
		ID:      "cam_01",
		Enabled: true,
		Streams: []CameraStream{{Role: StreamRoleMain}},
	}
	registry.InitCameraState(cam)

	// Previously online
	registry.RecordStreamSuccess("cam_01", 0, StreamRoleMain, EvidenceRTSPDescribe)

	// Failure 1 -> transient error
	s1 := registry.RecordStreamFailure("cam_01", 0, StreamRoleMain, "timeout", "timeout", false)
	if s1.Health != HealthStateError {
		t.Fatalf("expected error on 1st failure, got %s", s1.Health)
	}

	// Failure 2 -> still error
	s2 := registry.RecordStreamFailure("cam_01", 0, StreamRoleMain, "timeout", "timeout", false)
	if s2.Health != HealthStateError {
		t.Fatalf("expected error on 2nd failure, got %s", s2.Health)
	}

	// Failure 3 -> offline
	s3 := registry.RecordStreamFailure("cam_01", 0, StreamRoleMain, "timeout", "timeout", false)
	if s3.Health != HealthStateOffline {
		t.Fatalf("expected offline on 3rd failure, got %s", s3.Health)
	}
}

func TestStateRegistryStaleDetection(t *testing.T) {
	registry := NewStateRegistry()
	cam := &Camera{
		ID:      "cam_stale",
		Enabled: true,
		Streams: []CameraStream{{Role: StreamRoleMain}},
	}
	registry.InitCameraState(cam)
	registry.RecordStreamSuccess("cam_stale", 0, StreamRoleMain, EvidenceRTSPDescribe)

	// Manually inject older success time
	oldTime := time.Now().UTC().Add(-300 * time.Second)
	registry.mu.Lock()
	registry.states["cam_stale"].Streams[StreamRoleMain].LastSuccessAt = &oldTime
	registry.aggregateCameraStateLocked(registry.states["cam_stale"], time.Now().UTC())
	registry.mu.Unlock()

	state, ok := registry.GetState("cam_stale")
	if !ok {
		t.Fatal("state not found")
	}
	if !state.Stale || state.Health != HealthStateUnknown || state.Reason != "stale_check" {
		t.Fatalf("expected stale camera in unknown state, got: %+v", state)
	}
}

func TestStateRegistryRevisionConflictRejection(t *testing.T) {
	registry := NewStateRegistry()
	cam := &Camera{
		ID:       "cam_rev",
		Revision: 2,
		Enabled:  true,
		Streams:  []CameraStream{{Role: StreamRoleMain}},
	}
	registry.InitCameraState(cam)

	// Stale result with revision 1 should be ignored
	s := registry.RecordStreamSuccess("cam_rev", 1, StreamRoleMain, EvidenceRTSPDescribe)
	if s.Health != HealthStateUnknown {
		t.Fatalf("expected stale revision 1 to be ignored, got health=%s", s.Health)
	}

	// Matching revision 2 should be applied
	s = registry.RecordStreamSuccess("cam_rev", 2, StreamRoleMain, EvidenceRTSPDescribe)
	if s.Health != HealthStateOnline {
		t.Fatalf("expected matching revision 2 to be accepted, got health=%s", s.Health)
	}
}
