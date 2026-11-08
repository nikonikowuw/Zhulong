package algorunner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
)

// Gallery IPC payloads. The host is the only producer: runners receive them as
// MSG_GALLERY_* and never answer with these shapes, so there is exactly one
// serializer per message (see GalleryBroadcaster) rather than a payload helper
// plus a broadcaster that marshals the same struct again.

// GallerySyncShardPayload is one MSG_GALLERY_SYNC shard, sized to stay inside
// algoipc.MaxIpcPayloadSize.
type GallerySyncShardPayload struct {
	SyncID     string            `json:"sync_id"`
	ShardIndex int               `json:"shard_index"`
	ShardTotal int               `json:"shard_total"`
	Items      []GallerySyncItem `json:"items"`
}

// GalleryUpsertPayload represents MSG_GALLERY_UPSERT.
type GalleryUpsertPayload struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}

// GalleryDeletePayload represents MSG_GALLERY_DELETE.
type GalleryDeletePayload struct {
	FaceID   string `json:"face_id,omitempty"`
	PersonID string `json:"person_id,omitempty"`
}

// shardBudget caps a single gallery sync shard at 90% of the protocol limit so
// the shard header and marshalling drift cannot push a payload past
// algoipc.MaxIpcPayloadSize. Measured sizes: a 512-dim L2-normalized float32 embedding
// serializes to ~12.3 bytes per component (~6.3KB per item), so the old fixed
// 10-items-per-shard packing left under 1% headroom on real data (10 items
// measured at 64204~65373 bytes against the 65536 limit).
const shardBudget = algoipc.MaxIpcPayloadSize * 9 / 10

// galleryShardOverheadProbeIndex is the widest header the packer must budget for:
// six digits covers any realistic shard count, so the budget measured once here
// stays valid for every shard the packer can actually produce.
const galleryShardOverheadProbeIndex = 999999

// CreateGallerySyncShards fragments gallery items into a sequence of JSON
// payloads, each strictly within shardBudget (and therefore well under the 64KB
// IPC limit).
//
// Items are packed by measured byte size, not by a fixed count: a shard is
// closed as soon as adding the next item would exceed the budget. An empty
// input still yields exactly one shard with no items (preserved for existing
// callers). If a single item cannot fit on its own, an error naming that item's
// face_id is returned rather than silently dropping it.
func CreateGallerySyncShards(syncID string, items []GallerySyncItem) ([][]byte, error) {
	if len(items) == 0 {
		payload, err := marshalGalleryShardPayload(syncID, 0, 1, []GallerySyncItem{})
		if err != nil {
			return nil, err
		}
		return [][]byte{payload}, nil
	}

	// Marshal each item exactly once. The JSON encoding of an item is identical
	// whether it is serialized alone or inside a shard's items array, so these
	// measured sizes can be summed arithmetically instead of re-marshalling the
	// whole shard on every packing step (which would be O(n^2) on 10k faces).
	itemSizes := make([]int, len(items))
	for i, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("marshal gallery sync item %q: %w", item.FaceID, err)
		}
		itemSizes[i] = len(encoded)
	}

	overhead, err := galleryShardOverhead(syncID)
	if err != nil {
		return nil, err
	}

	ranges, err := packGalleryShards(items, itemSizes, overhead)
	if err != nil {
		return nil, err
	}
	return marshalGalleryShards(syncID, items, ranges)
}

// galleryShardRange is a half-open item range that becomes one shard.
type galleryShardRange struct{ start, end int }

// galleryShardOverhead measures the per-shard cost of the widest possible header.
func galleryShardOverhead(syncID string) (int, error) {
	payload, err := json.Marshal(GallerySyncShardPayload{
		SyncID:     syncID,
		ShardIndex: galleryShardOverheadProbeIndex,
		ShardTotal: galleryShardOverheadProbeIndex,
		Items:      []GallerySyncItem{},
	})
	if err != nil {
		return 0, fmt.Errorf("marshal gallery sync shard header: %w", err)
	}
	return len(payload), nil
}

// packGalleryShards greedily groups items: a shard is closed as soon as adding
// the next item would exceed the budget. Each additional item costs its own bytes
// plus one comma.
func packGalleryShards(items []GallerySyncItem, itemSizes []int, overhead int) ([]galleryShardRange, error) {
	ranges := []galleryShardRange{}
	start := 0
	used := 0
	for i, size := range itemSizes {
		if overhead+size > shardBudget {
			return nil, fmt.Errorf("gallery sync item %q exceeds the shard budget (%d bytes > %d)",
				items[i].FaceID, overhead+size, shardBudget)
		}
		additional := size
		if i > start {
			additional++ // comma separating this item from the previous one
		}
		if used+additional > shardBudget-overhead {
			ranges = append(ranges, galleryShardRange{start: start, end: i})
			start = i
			used = size
			continue
		}
		used += additional
	}
	return append(ranges, galleryShardRange{start: start, end: len(items)}), nil
}

func marshalGalleryShards(syncID string, items []GallerySyncItem, ranges []galleryShardRange) ([][]byte, error) {
	shardTotal := len(ranges)
	shards := make([][]byte, 0, shardTotal)
	for i, r := range ranges {
		payload, err := marshalGalleryShardPayload(
			syncID,
			i,
			shardTotal,
			items[r.start:r.end],
		)
		if err != nil {
			return nil, err
		}
		// Defensive gate: the packing math above should make this unreachable, but
		// it is the last line of defence before the 64KB protocol limit.
		if len(payload) > int(algoipc.MaxIpcPayloadSize) {
			return nil, fmt.Errorf("gallery sync shard %d exceeds 64KB (%d bytes)", i, len(payload))
		}
		shards = append(shards, payload)
	}
	return shards, nil
}

func marshalGalleryShardPayload(syncID string, index, total int, items []GallerySyncItem) ([]byte, error) {
	payload, err := json.Marshal(GallerySyncShardPayload{
		SyncID:     syncID,
		ShardIndex: index,
		ShardTotal: total,
		Items:      items,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery sync shard %d/%d: %w", index, total, err)
	}
	return payload, nil
}

// GalleryBroadcaster distributes full and incremental gallery updates to running algorithm processes.
type GalleryBroadcaster struct {
	broadcaster AIMessageBroadcaster
	logger      *zap.Logger
}

// NewGalleryBroadcaster creates a GalleryBroadcaster instance.
func NewGalleryBroadcaster(broadcaster AIMessageBroadcaster, logger *zap.Logger) *GalleryBroadcaster {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GalleryBroadcaster{
		broadcaster: broadcaster,
		logger:      logger.Named("algorithm.gallery_broadcaster"),
	}
}

// BroadcastSync performs cold-start or full synchronization of the face gallery to all active runners.
func (b *GalleryBroadcaster) BroadcastSync(ctx context.Context, syncID string, items []GallerySyncItem) error {
	if b.broadcaster == nil {
		return nil
	}
	shards, err := CreateGallerySyncShards(syncID, items)
	if err != nil {
		return fmt.Errorf("create gallery sync shards: %w", err)
	}
	for i, shard := range shards {
		if err := b.broadcaster.BroadcastAIMessage(
			ctx,
			0,
			algoipc.MsgGallerySync,
			shard,
		); err != nil {
			return fmt.Errorf("broadcast sync shard %d/%d: %w", i, len(shards), err)
		}
	}
	return nil
}

// BroadcastUpsert broadcasts an incremental face template enrollment or update.
func (b *GalleryBroadcaster) BroadcastUpsert(ctx context.Context, item GalleryUpsertPayload) error {
	if b.broadcaster == nil {
		return nil
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal gallery upsert: %w", err)
	}
	return b.broadcaster.BroadcastAIMessage(
		ctx,
		0,
		algoipc.MsgGalleryUpsert,
		payload,
	)
}

// BroadcastDelete broadcasts face or person deletion.
func (b *GalleryBroadcaster) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	if b.broadcaster == nil {
		return nil
	}
	payload, err := json.Marshal(GalleryDeletePayload{FaceID: faceID, PersonID: personID})
	if err != nil {
		return fmt.Errorf("marshal gallery delete: %w", err)
	}
	return b.broadcaster.BroadcastAIMessage(
		ctx,
		0,
		algoipc.MsgGalleryDelete,
		payload,
	)
}
