package algorunner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
)

// shardBudget mirrors the unexported algorunner.shardBudget. This is an external
// (black-box) test package, so it cannot reference the constant directly; it must
// still assert the same bound the implementation guarantees.
const shardBudget = algoipc.MaxIpcPayloadSize * 9 / 10

func testGalleryFeature(rng *rand.Rand) []float32 {
	feats := make([]float32, 512)
	var sumSq float64
	for i := range feats {
		v := rng.NormFloat64() / math.Sqrt(512)
		feats[i] = float32(v)
		sumSq += float64(feats[i]) * float64(feats[i])
	}
	norm := math.Sqrt(sumSq)
	for i := range feats {
		feats[i] = float32(float64(feats[i]) / norm)
	}
	return feats
}

const testULIDAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// testULIDLike returns a 26-character ULID-shaped identifier.
func testULIDLike(rng *rand.Rand) string {
	b := make([]byte, 26)
	for i := range b {
		b[i] = testULIDAlphabet[rng.Intn(len(testULIDAlphabet))]
	}
	return string(b)
}

var testCJKNames = []string{"张三", "李四", "王五", "赵六", "陈七", "刘八", "黄九", "周十"}

// testRealisticGalleryItems builds n items that match the real production shape:
// 512-dim L2-normalized features, 26-char ULID face/person IDs, a CJK name and
// a realistic image path.
func testRealisticGalleryItems(n int) []algorunner.GallerySyncItem {
	rng := rand.New(rand.NewSource(20241017))
	items := make([]algorunner.GallerySyncItem, n)
	for i := range items {
		faceID := testULIDLike(rng)
		items[i] = algorunner.GallerySyncItem{
			FaceID:     faceID,
			PersonID:   testULIDLike(rng),
			PersonName: testCJKNames[i%len(testCJKNames)],
			ImagePath:  fmt.Sprintf("faces/2024/06/17/%s_112x112.jpg", faceID),
			Features:   testGalleryFeature(rng),
		}
	}
	return items
}

func decodeShard(t *testing.T, payload []byte) algorunner.GallerySyncShardPayload {
	t.Helper()
	var p algorunner.GallerySyncShardPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("unmarshal shard payload: %v", err)
	}
	return p
}

type mockBroadcaster struct {
	messages []struct {
		streamID uint64
		msgType  uint16
		payload  []byte
	}
	err error
}

func (m *mockBroadcaster) BroadcastAIMessage(
	ctx context.Context,
	streamID uint64,
	msgType uint16,
	payload []byte,
) error {
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, struct {
		streamID uint64
		msgType  uint16
		payload  []byte
	}{streamID: streamID, msgType: msgType, payload: payload})
	return nil
}

func TestCreateGallerySyncShards_Empty(t *testing.T) {
	shards, err := algorunner.CreateGallerySyncShards("sync_empty", nil)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}
	var p algorunner.GallerySyncShardPayload
	if err := json.Unmarshal(shards[0], &p); err != nil {
		t.Fatalf("unmarshal shard payload: %v", err)
	}
	wrongSyncID := p.SyncID != "sync_empty"
	wrongShardIndex := p.ShardIndex != 0
	wrongShardTotal := p.ShardTotal != 1
	hasItems := len(p.Items) != 0
	if wrongSyncID || wrongShardIndex || wrongShardTotal || hasItems {
		t.Fatalf("unexpected shard payload: %+v", p)
	}
}

func TestCreateGallerySyncShards_MultiShardUnder64KB(t *testing.T) {
	// Realistic magnitude: 25 real-size items must pack into multiple shards,
	// each strictly within shardBudget. The old fixed 10-items-per-shard packing
	// produced ~64KB shards on this data (over budget), which this asserts.
	items := testRealisticGalleryItems(25)

	shards, err := algorunner.CreateGallerySyncShards("sync_multi", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) < 2 {
		t.Fatalf("expected multiple shards for 25 realistic items, got %d", len(shards))
	}

	for i, s := range shards {
		if len(s) > shardBudget {
			t.Errorf("shard %d exceeds shardBudget (%d): %d bytes", i, shardBudget, len(s))
		}
		p := decodeShard(t, s)
		if p.ShardIndex != i || p.ShardTotal != len(shards) {
			t.Errorf("shard %d metadata mismatch: index=%d, total=%d, want total=%d", i, p.ShardIndex, p.ShardTotal, len(shards))
		}
		if len(p.Items) == 0 {
			t.Errorf("shard %d unexpectedly empty", i)
		}
	}
}

func TestCreateGallerySyncShards_SingleItemExceedsBudget(t *testing.T) {
	// A single item larger than shardBudget cannot be sharded. The returned error
	// must be locatable (contains the face_id) so operators can find the record.
	item := testRealisticGalleryItems(1)[0]
	item.PersonName = strings.Repeat("很", 20000) // ~60KB of UTF-8, well over budget on its own

	_, err := algorunner.CreateGallerySyncShards("sync_oversized", []algorunner.GallerySyncItem{item})
	if err == nil {
		t.Fatalf("expected an error for a single item exceeding the shard budget")
	}
	if !strings.Contains(err.Error(), item.FaceID) {
		t.Fatalf("expected the error to contain the face_id %q, got: %v", item.FaceID, err)
	}
}

func TestCreateGallerySyncShards_RoundTripIntegrity(t *testing.T) {
	// 137 is deliberately not a multiple of any packing size: assert the shard
	// metadata and that concatenating the shards reproduces the input exactly
	// (no loss, no duplication, no reordering).
	items := testRealisticGalleryItems(137)

	shards, err := algorunner.CreateGallerySyncShards("sync_roundtrip", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}

	rebuilt := []algorunner.GallerySyncItem{}
	for i, s := range shards {
		if len(s) > shardBudget {
			t.Errorf("shard %d exceeds shardBudget: %d bytes", i, len(s))
		}
		p := decodeShard(t, s)
		if p.SyncID != "sync_roundtrip" {
			t.Errorf("shard %d sync_id = %q", i, p.SyncID)
		}
		if p.ShardIndex != i {
			t.Errorf("shard %d shard_index = %d, want %d", i, p.ShardIndex, i)
		}
		if p.ShardTotal != len(shards) {
			t.Errorf("shard %d shard_total = %d, want %d", i, p.ShardTotal, len(shards))
		}
		rebuilt = append(rebuilt, p.Items...)
	}

	if len(rebuilt) != len(items) {
		t.Fatalf("rebuilt item count = %d, want %d", len(rebuilt), len(items))
	}
	for i := range items {
		if !reflect.DeepEqual(rebuilt[i], items[i]) {
			t.Errorf("item %d mismatch after round trip:\n got %+v\nwant %+v", i, rebuilt[i], items[i])
		}
	}
}

func TestCreateGallerySyncShards_ShardBudgetHoldsForRealisticGallery(t *testing.T) {
	// Full-scale cold start: 200 real-size items. This MUST fail against the old
	// fixed 10-items-per-shard implementation (each ~64KB > shardBudget).
	items := testRealisticGalleryItems(200)

	shards, err := algorunner.CreateGallerySyncShards("sync_full", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}

	maxSize := 0
	total := 0
	for i, s := range shards {
		if len(s) > maxSize {
			maxSize = len(s)
		}
		if len(s) > shardBudget {
			t.Errorf("shard %d exceeds shardBudget (%d): %d bytes", i, shardBudget, len(s))
		}
		if len(s) > int(algoipc.MaxIpcPayloadSize) {
			t.Errorf("shard %d exceeds the protocol limit: %d bytes", i, len(s))
		}
		p := decodeShard(t, s)
		total += len(p.Items)
	}
	if total != len(items) {
		t.Errorf("packed %d items across shards, want %d", total, len(items))
	}
	t.Logf("shardBudget=%d, shards=%d, maxShardSize=%d", shardBudget, len(shards), maxSize)
}

func TestGalleryBroadcaster_SyncUpsertDelete(t *testing.T) {
	mock := &mockBroadcaster{}
	broadcaster := algorunner.NewGalleryBroadcaster(mock, zap.NewNop())
	ctx := context.Background()

	// 1. Sync
	items := []algorunner.GallerySyncItem{{FaceID: "f1", PersonID: "p1", PersonName: "张三"}}
	if err := broadcaster.BroadcastSync(ctx, "sync_test", items); err != nil {
		t.Fatalf("BroadcastSync failed: %v", err)
	}

	// 2. Upsert
	upsertItem := algorunner.GalleryUpsertPayload{FaceID: "f2", PersonID: "p2", PersonName: "李四"}
	if err := broadcaster.BroadcastUpsert(ctx, upsertItem); err != nil {
		t.Fatalf("BroadcastUpsert failed: %v", err)
	}

	// 3. Delete
	if err := broadcaster.BroadcastDelete(ctx, "f2", "p2"); err != nil {
		t.Fatalf("BroadcastDelete failed: %v", err)
	}

	if len(mock.messages) != 3 {
		t.Fatalf("expected 3 broadcast messages, got %d", len(mock.messages))
	}
	if mock.messages[0].msgType != algoipc.MsgGallerySync {
		t.Errorf("expected algoipc.MsgGallerySync (10), got %d", mock.messages[0].msgType)
	}
	if mock.messages[1].msgType != algoipc.MsgGalleryUpsert {
		t.Errorf("expected algoipc.MsgGalleryUpsert (11), got %d", mock.messages[1].msgType)
	}
	if mock.messages[2].msgType != algoipc.MsgGalleryDelete {
		t.Errorf("expected algoipc.MsgGalleryDelete (12), got %d", mock.messages[2].msgType)
	}
}

// TestRunnerProcessSpawnThreadSurvivesWhileChildRuns guards the thread-lifetime
// contract behind Pdeathsig (go.dev/issue/27505): the kernel signals the child
// when the *OS thread* that forked it exits, not when the process exits. If the
// spawn thread were released early, Go reclaiming that idle M would SIGKILL a
// perfectly healthy runner.
//
// The critical case is a SHORT-LIVED caller. Start is called from goroutines the
// host does not keep alive (HTTP handlers, Fx hooks, worker loops), and
// runtime.LockOSThread pins the goroutine that calls it — not its caller's
// creation thread. So each runner here is started from its own throwaway
// goroutine, which is allowed to exit before liveness is checked. A single
// runner hides the defect (one exiting goroutine rarely makes the runtime retire
// its M fast enough); several make it reliable, and the original implementation
// killed most of them within a second.
