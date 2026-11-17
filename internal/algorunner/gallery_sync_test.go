package algorunner

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
)

// gallerySyncRecordedCall is one BroadcastAIMessage call captured by the fake below.
type gallerySyncRecordedCall struct {
	streamID uint64
	msgType  uint16
	payload  []byte
}

// gallerySyncFakeBroadcaster records every push and reports whatever delivery
// count the test asks for. The default (nil report) is the healthy case: one
// runner received the payload.
//
// The zero value of a delivery report is the production failure this whole
// retry path exists for: the Broker accepted the payload while no runner was
// subscribed, so it was dropped on the floor and nothing below noticed.
type gallerySyncFakeBroadcaster struct {
	calls  []gallerySyncRecordedCall
	report func(call int, payload []byte) int
}

func (b *gallerySyncFakeBroadcaster) BroadcastAIMessage(
	_ context.Context,
	streamID uint64,
	msgType uint16,
	payload []byte,
) (int, error) {
	call := len(b.calls) + 1
	b.calls = append(b.calls, gallerySyncRecordedCall{streamID: streamID, msgType: msgType, payload: payload})
	if b.report == nil {
		return 1, nil
	}
	return b.report(call, payload), nil
}

// gallerySyncFakeProvider serves a fixed snapshot and counts how often the
// supervisor reloaded it.
type gallerySyncFakeProvider struct {
	items []GallerySyncItem
	err   error
	calls int
}

func (p *gallerySyncFakeProvider) GetGallerySyncData(context.Context) ([]GallerySyncItem, error) {
	p.calls++
	return p.items, p.err
}

func decodeGallerySyncShard(t *testing.T, payload []byte) GallerySyncShardPayload {
	t.Helper()
	var shard GallerySyncShardPayload
	if err := json.Unmarshal(payload, &shard); err != nil {
		t.Fatalf("decode gallery sync shard: %v", err)
	}
	return shard
}

// gallerySyncTestItems builds n items shaped like production data: 512 dims of
// float32 features, which is what makes a real snapshot span several shards.
func gallerySyncTestItems(n int) []GallerySyncItem {
	items := make([]GallerySyncItem, n)
	for i := range items {
		features := make([]float32, 512)
		for j := range features {
			features[j] = float32((i*7+j*3)%101) / 4096
		}
		items[i] = GallerySyncItem{
			FaceID:     fmt.Sprintf("face_%02d", i),
			PersonID:   fmt.Sprintf("person_%02d", i),
			PersonName: "员工",
			Features:   features,
		}
	}
	return items
}

// TestPushGalleryUntilDeliveredRetriesUntilARunnerTakesIt pins the fix for the
// silent gallery loss: a push that lands before the runner finishes loading its
// models must be retried, and a successful push must stop the ladder.
func TestPushGalleryUntilDeliveredRetriesUntilARunnerTakesIt(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}
	fake := &gallerySyncFakeBroadcaster{report: func(call int, _ []byte) int {
		// Two pushes land on an empty broker (the runner is still loading
		// models), the third one finds it connected.
		if call < 3 {
			return 0
		}
		return 1
	}}
	broadcaster := NewGalleryBroadcaster(fake, zap.NewNop())

	supervisor.pushGalleryUntilDelivered(provider, broadcaster, time.Millisecond, 5)

	if len(fake.calls) != 3 {
		t.Fatalf("expected 3 broadcast attempts (2 dropped, 1 delivered), got %d", len(fake.calls))
	}
	if provider.calls != 3 {
		t.Fatalf("expected a fresh snapshot per attempt (%d), got %d", len(fake.calls), provider.calls)
	}

	// A reused sync_id would land in the runner's previous 5s assembly window
	// and mix shards from two different snapshots.
	seen := make(map[string]struct{}, len(fake.calls))
	for i, call := range fake.calls {
		if call.streamID != 0 {
			t.Errorf("attempt %d: gallery push must broadcast to every stream, got stream %d", i+1, call.streamID)
		}
		if call.msgType != algoipc.MsgGallerySync {
			t.Errorf("attempt %d: expected MSG_GALLERY_SYNC (10), got %d", i+1, call.msgType)
		}
		syncID := decodeGallerySyncShard(t, call.payload).SyncID
		if _, dup := seen[syncID]; dup {
			t.Errorf("attempt %d reused sync_id %q; every attempt needs a fresh assembly window", i+1, syncID)
		}
		seen[syncID] = struct{}{}
	}
}

// TestPushGalleryUntilDeliveredGivesUpAfterBudget keeps the ladder bounded: a
// runner that never shows up must not leave a goroutine probing forever.
func TestPushGalleryUntilDeliveredGivesUpAfterBudget(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}
	fake := &gallerySyncFakeBroadcaster{report: func(int, []byte) int { return 0 }}
	broadcaster := NewGalleryBroadcaster(fake, zap.NewNop())

	supervisor.pushGalleryUntilDelivered(provider, broadcaster, time.Millisecond, 3)

	if len(fake.calls) != 3 {
		t.Fatalf("expected the ladder to stop at the attempt budget (3), got %d attempts", len(fake.calls))
	}
	if provider.calls != 3 {
		t.Fatalf("expected 3 provider reloads, got %d", provider.calls)
	}
}

// TestPushGalleryUntilDeliveredStopsOnShutdown proves the retry loop honours
// StopAll instead of only its own budget.
func TestPushGalleryUntilDeliveredStopsOnShutdown(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}
	fake := &gallerySyncFakeBroadcaster{report: func(call int, _ []byte) int {
		if call == 2 {
			close(supervisor.stopCh)
		}
		return 0
	}}
	broadcaster := NewGalleryBroadcaster(fake, zap.NewNop())

	supervisor.pushGalleryUntilDelivered(provider, broadcaster, 50*time.Millisecond, 100)

	if len(fake.calls) != 2 {
		t.Fatalf("expected the loop to exit after shutdown on attempt 2, got %d attempts", len(fake.calls))
	}
}

// TestPushGalleryUntilDeliveredSerializesSnapshots pins the one-snapshot-at-a-time
// invariant. Interleaved ladders hand the runner shards tagged with different
// sync_ids, and its assembly context resets on every id change: the snapshot
// never completes while both ladders think they delivered.
func TestPushGalleryUntilDeliveredSerializesSnapshots(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}

	var inFlight, peak atomic.Int32
	fake := &gallerySyncFakeBroadcaster{report: func(int, []byte) int {
		now := inFlight.Add(1)
		if now > peak.Load() {
			peak.Store(now)
		}
		// Long enough that an unserialized pair is guaranteed to overlap.
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		return 0
	}}
	broadcaster := NewGalleryBroadcaster(fake, zap.NewNop())

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			supervisor.pushGalleryUntilDelivered(provider, broadcaster, time.Millisecond, 2)
		}()
	}
	wg.Wait()

	if got := peak.Load(); got != 1 {
		t.Fatalf("expected at most 1 snapshot in flight, observed %d concurrent pushes", got)
	}
	if len(fake.calls) != 4 {
		t.Fatalf("expected both ladders to exhaust their budget (4 pushes), got %d", len(fake.calls))
	}
}

// TestGalleryBroadcastSyncRequiresEveryShardDelivered covers the shard-level
// gate: a runner that connects halfway through the sequence receives only the
// tail, so a partial broadcast must report 0 rather than the shards that landed.
func TestGalleryBroadcastSyncRequiresEveryShardDelivered(t *testing.T) {
	ctx := context.Background()
	items := gallerySyncTestItems(40)

	probe, err := CreateGallerySyncShards("probe", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(probe) < 3 {
		t.Fatalf("test needs a multi-shard snapshot, got %d shard(s)", len(probe))
	}

	fake := &gallerySyncFakeBroadcaster{report: func(_ int, payload []byte) int {
		if decodeGallerySyncShard(t, payload).ShardIndex == 1 {
			return 0 // the runner missed this shard: nothing can complete
		}
		return 2
	}}
	broadcaster := NewGalleryBroadcaster(fake, zap.NewNop())

	delivered, err := broadcaster.BroadcastSync(ctx, "sync_partial", items)
	if err != nil {
		t.Fatalf("BroadcastSync failed: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("expected 0 delivered when a shard reached no one, got %d", delivered)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected the broadcast to stop at the unclaimed shard (2 pushes), got %d", len(fake.calls))
	}
}

// TestGalleryBroadcastSyncReportsMinimumDelivered pins the multi-runner rule:
// one runner per stream, so the snapshot is only as delivered as the least
// served stream.
func TestGalleryBroadcastSyncReportsMinimumDelivered(t *testing.T) {
	ctx := context.Background()
	items := gallerySyncTestItems(40)

	probe, err := CreateGallerySyncShards("probe", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(probe) < 3 {
		t.Fatalf("test needs a multi-shard snapshot, got %d shard(s)", len(probe))
	}

	want := len(probe)
	fake := &gallerySyncFakeBroadcaster{report: func(_ int, payload []byte) int {
		return want - decodeGallerySyncShard(t, payload).ShardIndex
	}}
	broadcaster := NewGalleryBroadcaster(fake, zap.NewNop())

	delivered, err := broadcaster.BroadcastSync(ctx, "sync_min", items)
	if err != nil {
		t.Fatalf("BroadcastSync failed: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("expected the minimum across shards (1), got %d", delivered)
	}
	if len(fake.calls) != want {
		t.Fatalf("expected %d pushes (one per shard), got %d", want, len(fake.calls))
	}
}
