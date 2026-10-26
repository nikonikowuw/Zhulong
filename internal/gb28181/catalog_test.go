package gb28181

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

type mockSipSender struct {
	mu        sync.Mutex
	failCount int
	calls     int
	lastBody  []byte
	onSend    func(device Device, body []byte)
}

func (m *mockSipSender) SendMessage(ctx context.Context, device Device, body []byte) error {
	m.mu.Lock()
	m.calls++
	m.lastBody = body
	fails := m.failCount
	if m.failCount > 0 {
		m.failCount--
	}
	callback := m.onSend
	m.mu.Unlock()

	if fails > 0 {
		return errors.New("simulated network failure")
	}
	if callback != nil {
		go callback(device, body)
	}
	return nil
}

type memoryChannelStore struct {
	mu       sync.Mutex
	channels map[string][]Channel
}

func newMemoryChannelStore() *memoryChannelStore {
	return &memoryChannelStore{channels: make(map[string][]Channel)}
}

// SyncChannels mirrors the store contract: upsert by channel ID and drop rows the
// device no longer reports, keeping anything bound to a camera.
func (m *memoryChannelStore) SyncChannels(ctx context.Context, deviceID string, channels []Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.channels == nil {
		m.channels = make(map[string][]Channel)
	}
	reported := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		reported[ch.ChannelID] = struct{}{}
	}
	merged := make([]Channel, 0, len(channels))
	for _, existing := range m.channels[deviceID] {
		if _, ok := reported[existing.ChannelID]; !ok && existing.CameraID != nil {
			merged = append(merged, existing)
		}
	}
	for _, ch := range channels {
		for _, existing := range m.channels[deviceID] {
			if existing.ChannelID == ch.ChannelID {
				ch.CameraID = existing.CameraID
				break
			}
		}
		merged = append(merged, ch)
	}
	m.channels[deviceID] = merged
	return nil
}

func (m *memoryChannelStore) ListChannels(ctx context.Context, deviceID string) ([]Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.channels[deviceID], nil
}

func (m *memoryChannelStore) GetChannel(ctx context.Context, deviceID, channelID string) (*Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.channels[deviceID] {
		if ch.ChannelID == channelID {
			return &ch, nil
		}
	}
	return nil, apperr.NotFound(CodeChannelNotFound, "not found", nil)
}

func (m *memoryChannelStore) BindChannelCamera(ctx context.Context, deviceID, channelID, cameraID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.channels[deviceID] {
		if m.channels[deviceID][i].ChannelID == channelID {
			m.channels[deviceID][i].CameraID = &cameraID
			return nil
		}
	}
	return apperr.NotFound(CodeChannelNotFound, "not found", nil)
}

func TestCatalogSyncSingleFragment(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())

	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, err := decodeXML(body)
		if err != nil {
			t.Errorf("failed to decode query body: %v", err)
			return
		}
		// Send back response
		resp := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   2,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000001", Name: "Channel 1"},
				{DeviceID: "34020000001310000002", Name: "Channel 2"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	channels, err := cm.Sync(ctx, Device{DeviceID: devID})
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
	if len(store.channels[devID]) != 2 {
		t.Errorf("expected 2 channels in store, got %d", len(store.channels[devID]))
	}
}

func TestCatalogSyncMultiFragmentReorderedAndDeduplicated(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())

	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, _ := decodeXML(body)

		// Fragment 1: arrives with item 2 and item 3
		resp1 := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   3,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000002", Name: "Channel 2"},
				{DeviceID: "34020000001310000003", Name: "Channel 3"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp1)

		// Fragment 2: arrives with item 1 and duplicate item 2
		resp2 := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   3,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000001", Name: "Channel 1"},
				{DeviceID: "34020000001310000002", Name: "Channel 2 Duplicate"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	channels, err := cm.Sync(ctx, Device{DeviceID: devID})
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("expected 3 unique channels, got %d", len(channels))
	}
}

func TestCatalogSyncEmptyList(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())

	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, _ := decodeXML(body)
		resp := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   0,
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	channels, err := cm.Sync(ctx, Device{DeviceID: devID})
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("expected 0 channels, got %d", len(channels))
	}
}

func TestCatalogSyncTimeout(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())

	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, _ := decodeXML(body)
		// Send incomplete fragment (SumNum=5, but only send 1 item)
		resp := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   5,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000001", Name: "Channel 1"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := cm.Sync(ctx, Device{DeviceID: devID})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeCatalogTimeout {
		t.Errorf("expected %s, got %v", CodeCatalogTimeout, err)
	}
}

func TestCatalogSyncRetryOnSendFailure(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{failCount: 1} // Fail 1st attempt, succeed 2nd attempt
	cm := NewCatalogManager(sender, store, zap.NewNop())

	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, _ := decodeXML(body)
		resp := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   1,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000001", Name: "Channel 1"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	channels, err := cm.Sync(ctx, Device{DeviceID: devID})
	if err != nil {
		t.Fatalf("Sync failed after retry: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	if sender.calls != 2 {
		t.Errorf("expected 2 send attempts, got %d", sender.calls)
	}
}

func TestCatalogSyncSendFailureExhausted(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{failCount: 5} // Always fail
	cm := NewCatalogManager(sender, store, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := cm.Sync(ctx, Device{DeviceID: "34020000001180000001"})
	if err == nil {
		t.Fatalf("expected send error")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeCatalogSyncFailed {
		t.Errorf("expected %s, got %v", CodeCatalogSyncFailed, err)
	}
}

func TestCatalogSyncAppliesOwnDeadline(t *testing.T) {
	store := newMemoryChannelStore()
	// The sender succeeds but the device never answers: without an internal deadline the
	// exchange would wait for the caller's context, and an HTTP request has none.
	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())
	cm.timeout = 30 * time.Millisecond

	start := time.Now()
	_, err := cm.Sync(context.Background(), Device{DeviceID: "34020000001180000001"})
	elapsed := time.Since(start)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeCatalogTimeout {
		t.Fatalf("expected %s, got %v", CodeCatalogTimeout, err)
	}
	if elapsed > time.Second {
		t.Fatalf("Sync did not honour its own deadline: %v", elapsed)
	}
}

func TestCatalogSyncRejectsMalformedChannelIDs(t *testing.T) {
	store := newMemoryChannelStore()
	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())

	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, _ := decodeXML(body)
		resp := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   3,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000001", Name: "Good"},
				{DeviceID: "channel-2", Name: "Not a code"},
				{DeviceID: "", Name: "Empty"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// SumNum counts the malformed entries, so the session never completes: the point of
	// this test is that the two bad codes never reach the store.
	_, err := cm.Sync(ctx, Device{DeviceID: devID})
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeCatalogTimeout {
		t.Fatalf("expected %s, got %v", CodeCatalogTimeout, err)
	}
	if len(store.channels[devID]) != 0 {
		t.Errorf("malformed catalog must not be persisted, got %+v", store.channels[devID])
	}
}

func TestCatalogSyncPrunesStaleChannelsButKeepsBoundOnes(t *testing.T) {
	store := newMemoryChannelStore()
	bound := "cam_01JTEST0000000000000000"
	store.channels["34020000001180000001"] = []Channel{
		{DeviceID: "34020000001180000001", ChannelID: "34020000001310000001", CameraID: &bound},
		{DeviceID: "34020000001180000001", ChannelID: "34020000001310000009", Name: "Removed by device"},
	}

	sender := &mockSipSender{}
	cm := NewCatalogManager(sender, store, zap.NewNop())
	devID := "34020000001180000001"
	sender.onSend = func(device Device, body []byte) {
		msg, _ := decodeXML(body)
		resp := gbCatalogResponse{
			CmdType:  "Catalog",
			SN:       msg.SN,
			DeviceID: devID,
			SumNum:   2,
			DeviceList: []gbCatalogItem{
				{DeviceID: "34020000001310000001", Name: "Still there"},
				{DeviceID: "34020000001310000002", Name: "New"},
			},
		}
		_ = cm.HandleResponse(context.Background(), device.DeviceID, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := cm.Sync(ctx, Device{DeviceID: devID}); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	listed, err := store.ListChannels(context.Background(), devID)
	if err != nil {
		t.Fatalf("ListChannels failed: %v", err)
	}
	seen := make(map[string]*string, len(listed))
	for _, ch := range listed {
		seen[ch.ChannelID] = ch.CameraID
	}
	if _, ok := seen["34020000001310000009"]; ok {
		t.Errorf("stale unbound channel survived the sync: %+v", listed)
	}
	if _, ok := seen["34020000001310000002"]; !ok {
		t.Errorf("new channel missing after sync: %+v", listed)
	}
	if camID, ok := seen["34020000001310000001"]; !ok || camID == nil || *camID != bound {
		t.Errorf("bound channel lost its camera binding: %+v", listed)
	}
}
