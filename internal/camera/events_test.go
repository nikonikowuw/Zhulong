package camera

import (
	"testing"
	"time"
)

func TestEventHubSubscribeAndSnapshot(t *testing.T) {
	reg := NewStateRegistry()
	cam := &Camera{ID: "cam_1", Enabled: true, Streams: []CameraStream{{Role: StreamRoleMain}}}
	reg.InitCameraState(cam)

	hub := NewEventHub(reg)
	defer hub.Close()

	sub, snapshot, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer hub.Unsubscribe(sub)

	if snapshot.Event != EventTypeSnapshot || snapshot.Sequence != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	dataMap, ok := snapshot.Data.(map[string]*CameraStateInfo)
	if !ok || len(dataMap) != 1 || dataMap["cam_1"] == nil {
		t.Fatalf("expected snapshot with cam_1, got: %+v", snapshot.Data)
	}
}

func TestEventHubBroadcastAndSequence(t *testing.T) {
	reg := NewStateRegistry()
	cam := &Camera{ID: "cam_1", Enabled: true, Streams: []CameraStream{{Role: StreamRoleMain}}}
	reg.InitCameraState(cam)

	hub := NewEventHub(reg)
	defer hub.Close()

	sub, _, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer hub.Unsubscribe(sub)

	updatedState := reg.RecordStreamSuccess("cam_1", 0, StreamRoleMain, EvidenceRTSPDescribe)
	hub.BroadcastChange(updatedState)

	select {
	case msg, ok := <-sub.Channel():
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		if msg.Event != EventTypeChange || msg.Sequence != 2 {
			t.Fatalf("unexpected message: %+v", msg)
		}
		sseBytes, err := msg.FormatSSE()
		if err != nil {
			t.Fatalf("FormatSSE failed: %v", err)
		}
		if len(sseBytes) == 0 {
			t.Fatal("expected non-empty SSE output")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestEventHubEvictsSlowConsumer(t *testing.T) {
	reg := NewStateRegistry()
	hub := NewEventHub(reg)
	defer hub.Close()

	sub, _, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Do not read from sub.Channel()
	// Fill queue past capacity (SSEClientQueueSize = 32)
	dummyState := &CameraStateInfo{CameraID: "cam_slow", Health: HealthStateOnline}
	for i := 0; i < SSEClientQueueSize+5; i++ {
		hub.BroadcastChange(dummyState)
	}

	// The slow consumer channel should now be closed / disconnected
	time.Sleep(50 * time.Millisecond)
	select {
	case _, ok := <-sub.Channel():
		if ok {
			// Drain remaining buffer until closed
			for range sub.Channel() {
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected channel close on slow consumer eviction")
	}
}

func TestEventHubMaxClientsLimit(t *testing.T) {
	reg := NewStateRegistry()
	hub := NewEventHub(reg)
	defer hub.Close()

	var subs []*ClientSubscription
	for i := 0; i < SSEMaxClients; i++ {
		sub, _, err := hub.Subscribe()
		if err != nil {
			t.Fatalf("Subscribe %d failed: %v", i, err)
		}
		subs = append(subs, sub)
	}

	// 17th subscriber should be rejected
	_, _, err := hub.Subscribe()
	if err != ErrSSEMaxClientsReached {
		t.Fatalf("expected ErrSSEMaxClientsReached, got: %v", err)
	}

	// Clean up
	for _, sub := range subs {
		hub.Unsubscribe(sub)
	}
}
