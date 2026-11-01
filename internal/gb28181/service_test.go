package gb28181

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"go.uber.org/zap"
)

// bindFailStore keeps the memory store's behaviour except for the binding write, which
// is the one call the rollback path has to survive.
type bindFailStore struct {
	*memoryChannelStore
}

func (s *bindFailStore) BindChannelCamera(ctx context.Context, deviceID, channelID, cameraID string) error {
	return errors.New("simulated bind failure")
}

// serializedCreator records overlapping create calls: it is deliberately slow so the
// second caller has to join the in-flight singleflight.
type serializedCreator struct {
	mu    sync.Mutex
	calls int
}

func (c *serializedCreator) CreateGB28181Camera(ctx context.Context, params camera.CreateGB28181CameraParams) (*camera.CameraResponse, error) {
	c.mu.Lock()
	c.calls++
	id := fmt.Sprintf("cam_%02d", c.calls)
	c.mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	return &camera.CameraResponse{ID: id, Name: params.Name}, nil
}

func (c *serializedCreator) Delete(ctx context.Context, id string) error { return nil }

func (c *serializedCreator) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type stubCatalogSyncer struct {
	channels []Channel
	err      error
}

func (s *stubCatalogSyncer) Sync(ctx context.Context, device Device) ([]Channel, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.channels, nil
}

func TestAddChannelAsCameraRollsBackCameraWhenBindingFails(t *testing.T) {
	const (
		deviceID  = "34020000001180000001"
		channelID = "34020000001310000001"
	)
	channels := &bindFailStore{memoryChannelStore: newMemoryChannelStore()}
	channels.channels[deviceID] = []Channel{{DeviceID: deviceID, ChannelID: channelID, Name: "Gate"}}
	creator := &mockCameraCreator{}

	service := NewService(ServiceOptions{
		Channels:      channels,
		CameraCreator: creator,
		CameraDeleter: creator,
		Logger:        zap.NewNop(),
	})

	_, err := service.AddChannelAsCamera(context.Background(), deviceID, channelID)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeDatabaseError {
		t.Fatalf("expected %s when the binding write fails, got %v", CodeDatabaseError, err)
	}
	// A camera that exists without a binding can never be added again (the channel
	// looks free) and never shows up in the channel drawer, so the rollback must run.
	if len(creator.deleted) != 1 || creator.deleted[0] != creator.createdID {
		t.Fatalf("expected the created camera %q to be rolled back, got %v", creator.createdID, creator.deleted)
	}
}

func TestAddChannelAsCameraIsSerializedPerChannel(t *testing.T) {
	const (
		deviceID  = "34020000001180000001"
		channelID = "34020000001310000001"
	)
	channels := newMemoryChannelStore()
	channels.channels[deviceID] = []Channel{{DeviceID: deviceID, ChannelID: channelID, Name: "Gate"}}
	creator := &serializedCreator{}

	service := NewService(ServiceOptions{
		Channels:      channels,
		CameraCreator: creator,
		CameraDeleter: creator,
		Logger:        zap.NewNop(),
	})

	const callers = 5
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		ids    []string
		others []error
	)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := service.AddChannelAsCamera(context.Background(), deviceID, channelID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				others = append(others, err)
				return
			}
			ids = append(ids, resp.ID)
		}()
	}
	close(start)
	wg.Wait()

	if len(others) > 0 {
		t.Fatalf("unexpected errors from concurrent adds: %v", others)
	}
	// Overlapping callers join the in-flight binding and share its answer; the invariant
	// that matters is one camera row per channel, not one 409 per racing click.
	if creator.callCount() != 1 {
		t.Fatalf("expected the create path to run once, ran %d times", creator.callCount())
	}
	if len(ids) != callers {
		t.Fatalf("expected %d responses, got %d", callers, len(ids))
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("racing callers must share one camera, got %v", ids)
		}
	}
	if bound := channels.channels[deviceID][0].CameraID; bound == nil || *bound != ids[0] {
		t.Fatalf("expected the channel to be bound to %q, got %v", ids[0], bound)
	}
}

func TestSyncCatalogRecordsRuntimeCounters(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	ctx := context.Background()
	const deviceID = "34020000001180000001"
	password := "testpass"
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{DeviceID: deviceID, Password: &password}); err != nil {
		t.Fatalf("provision device: %v", err)
	}
	if err := store.dbProvider.DB().Model(&Device{}).
		Where("device_id = ?", deviceID).
		Update("state", DeviceStateOnline).Error; err != nil {
		t.Fatalf("mark device online: %v", err)
	}

	runtime := NewRuntime()
	syncer := &stubCatalogSyncer{channels: []Channel{
		{DeviceID: deviceID, ChannelID: "34020000001310000001"},
	}}
	service := NewService(ServiceOptions{
		Devices:  store,
		Channels: store,
		Catalog:  syncer,
		Runtime:  runtime,
		Logger:   zap.NewNop(),
	})

	if _, err := service.SyncCatalog(ctx, deviceID); err != nil {
		t.Fatalf("sync catalog: %v", err)
	}
	snapshot := runtime.Snapshot()
	if snapshot.Catalog.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", snapshot.Catalog.Attempts)
	}
	if snapshot.Catalog.LastChannels != 1 {
		t.Errorf("expected 1 channel in the last sync, got %d", snapshot.Catalog.LastChannels)
	}
	if snapshot.Catalog.LastSyncAt == nil {
		t.Error("expected lastSyncAt to be set after a successful sync")
	}

	syncer.err = apperr.Invalid(CodeCatalogTimeout, "timed out", nil)
	if _, err := service.SyncCatalog(ctx, deviceID); err == nil {
		t.Fatal("expected the stub failure to surface")
	}
	snapshot = runtime.Snapshot()
	if snapshot.Catalog.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", snapshot.Catalog.Failures)
	}
	// A timeout is the case an operator must be able to tell apart from a transport
	// failure, so it is counted separately rather than folded into Failures.
	if snapshot.Catalog.Timeouts != 1 {
		t.Errorf("expected 1 timeout, got %d", snapshot.Catalog.Timeouts)
	}
}

// blockingCatalogSyncer keeps the shared sync body running until the test releases it,
// so a second caller is guaranteed to join instead of starting a second exchange.
type blockingCatalogSyncer struct {
	started chan struct{}
	release chan struct{}
	calls   int32
}

func (s *blockingCatalogSyncer) Sync(context.Context, Device) ([]Channel, error) {
	atomic.AddInt32(&s.calls, 1)
	s.started <- struct{}{}
	<-s.release
	return []Channel{}, nil
}

// A joined caller does not start an exchange, so it must not be counted as an attempt:
// the counter answers "how many catalog queries left this box", not "how many times the
// button was clicked".
func TestSyncCatalogCountsOneAttemptPerExchange(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	ctx := context.Background()
	const deviceID = "34020000001180000002"
	password := "testpass"
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{DeviceID: deviceID, Password: &password}); err != nil {
		t.Fatalf("provision device: %v", err)
	}
	if err := store.dbProvider.DB().Model(&Device{}).
		Where("device_id = ?", deviceID).
		Update("state", DeviceStateOnline).Error; err != nil {
		t.Fatalf("mark device online: %v", err)
	}

	syncer := &blockingCatalogSyncer{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	runtime := NewRuntime()
	service := NewService(ServiceOptions{
		Devices:  store,
		Channels: store,
		Catalog:  syncer,
		Runtime:  runtime,
		Logger:   zap.NewNop(),
	})

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if _, err := service.SyncCatalog(ctx, deviceID); err != nil {
				t.Errorf("sync catalog: %v", err)
			}
		}()
	}

	<-syncer.started
	// Give the second caller time to join the running exchange.
	time.Sleep(50 * time.Millisecond)
	close(syncer.release)
	wg.Wait()

	if calls := atomic.LoadInt32(&syncer.calls); calls != 1 {
		t.Fatalf("expected the syncer to run once, got %d", calls)
	}
	if attempts := runtime.Snapshot().Catalog.Attempts; attempts != 1 {
		t.Fatalf("expected 1 attempt for 2 callers, got %d", attempts)
	}
}

// The snapshot endpoint is the operator's field diagnostic: the protection counters have
// to reach it, otherwise a firing cap is invisible without reading the log.
func TestSnapshotCarriesIngressProtectionCounters(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	runtime := NewRuntime()
	provider := &stubIngressStats{stats: IngressStats{
		OversizedMessages: 3,
		RejectedConns:     2,
		InboundLimited:    11,
		OutboundLimited:   4,
		ActiveConns:       1,
	}}
	service := NewService(ServiceOptions{
		Devices: store,
		Runtime: runtime,
		Logger:  zap.NewNop(),
		Ingress: provider,
	})

	snapshot := service.Snapshot(context.Background())
	want := IngressCounters{
		OversizedMessages: 3,
		RejectedConns:     2,
		InboundLimited:    11,
		OutboundLimited:   4,
		ActiveConns:       1,
	}
	if snapshot.Ingress != want {
		t.Fatalf("expected %+v, got %+v", want, snapshot.Ingress)
	}
}

type stubIngressStats struct {
	stats IngressStats
}

func (s *stubIngressStats) Stats() IngressStats { return s.stats }
