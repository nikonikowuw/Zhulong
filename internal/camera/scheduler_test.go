package camera

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func TestHealthSchedulerActivePacketTimeout(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	_ = dbStore.OpenAndMigrate(context.Background())
	defer dbStore.Close()

	km := NewKeyManager(tempDir)
	cipher, _ := km.InitOrLoadKey(false)
	store := NewCameraStore(dbStore.DB)
	registry := NewStateRegistry()
	hub := NewEventHub(registry)
	defer hub.Close()

	scheduler := NewHealthScheduler(store, cipher, registry, hub, nil, zap.NewNop())

	// Create camera
	cam := &Camera{
		ID:      "cam_active_test",
		Name:    "Active Cam",
		Enabled: true,
	}
	_ = store.Create(context.Background(), cam, nil)
	registry.InitCameraState(cam)

	// Set stream to running, and simulate last packet arrived 5 seconds ago
	registry.UpdateSessionState("cam_active_test", StreamRoleMain, SessionStateRunning)
	registry.RecordStreamSuccess("cam_active_test", 0, StreamRoleMain, EvidencePacketActivity)

	// Inject old lastCheckedAt
	oldTime := time.Now().UTC().Add(-5 * time.Second)
	registry.mu.Lock()
	registry.states["cam_active_test"].Streams[StreamRoleMain].LastCheckedAt = &oldTime
	registry.mu.Unlock()

	// Run active monitor scan
	scheduler.monitorActiveStreams()

	state, ok := registry.GetState("cam_active_test")
	if !ok {
		t.Fatal("state not found")
	}

	mainStream := state.Streams[StreamRoleMain]
	if mainStream.Session != SessionStateReconnecting || mainStream.Health != HealthStateError {
		t.Fatalf("expected packet timeout to trigger reconnecting & error, got session=%s health=%s",
			mainStream.Session, mainStream.Health)
	}
}

func TestHealthSchedulerTriggerCheck(t *testing.T) {
	addr, cleanup := startMockRTSPServer(t, func(conn net.Conn) {
		defer conn.Close()
		r := bufio.NewReader(conn)
		_, _, cseq, _, _ := readMockRTSPRequest(r)
		resp := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
			cseq, len(sampleH264SDP), sampleH264SDP)
		_, _ = conn.Write([]byte(resp))
	})
	defer cleanup()

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	_ = dbStore.OpenAndMigrate(context.Background())
	defer dbStore.Close()

	km := NewKeyManager(tempDir)
	cipher, _ := km.InitOrLoadKey(false)
	store := NewCameraStore(dbStore.DB)
	registry := NewStateRegistry()
	hub := NewEventHub(registry)
	defer hub.Close()

	scheduler := NewHealthScheduler(store, cipher, registry, hub, NewDescribeClient(), zap.NewNop())

	rawURI := fmt.Sprintf("rtsp://%s/live/main", addr)
	aad := MakeAAD("cam_sched_01", StreamRoleMain)
	encrypted, _ := cipher.Encrypt([]byte(rawURI), aad)

	cam := &Camera{
		ID:      "cam_sched_01",
		Name:    "Sched Cam",
		Enabled: true,
	}
	streams := []CameraStream{
		{
			Role:         StreamRoleMain,
			Protocol:     ProtocolRTSP,
			EncryptedURI: encrypted,
			Transport:    TransportTCP,
			Codec:        "h264",
			Width:        1920,
			Height:       1080,
		},
	}
	_ = store.Create(context.Background(), cam, streams)
	registry.InitCameraState(cam)

	// Trigger immediate check
	state, err := scheduler.TriggerCheck(context.Background(), "cam_sched_01")
	if err != nil {
		t.Fatalf("TriggerCheck failed: %v", err)
	}

	if state.Health != HealthStateOnline {
		t.Fatalf("expected online state after check, got: %+v", state)
	}
}
