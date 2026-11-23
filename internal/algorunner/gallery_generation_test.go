package algorunner

import (
	"context"
	"math"
	"testing"

	"go.uber.org/zap"
)

// TestGalleryBroadcasterGeneration pins the staleness counter the full-snapshot
// push compares before and after a broadcast: if it moved, the snapshot raced an
// enrollment or a deletion and has to be republished.
func TestGalleryBroadcasterGeneration(t *testing.T) {
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{}, zap.NewNop())
	ctx := context.Background()

	if got := broadcaster.Generation(); got != 0 {
		t.Fatalf("fresh broadcaster generation = %d, want 0", got)
	}

	if err := broadcaster.BroadcastUpsert(ctx, GalleryUpsertPayload{FaceID: "face_1", PersonID: "person_1"}); err != nil {
		t.Fatalf("broadcast upsert: %v", err)
	}
	if got := broadcaster.Generation(); got != 1 {
		t.Fatalf("generation after upsert = %d, want 1", got)
	}

	if err := broadcaster.BroadcastDelete(ctx, "face_1", ""); err != nil {
		t.Fatalf("broadcast delete: %v", err)
	}
	if got := broadcaster.Generation(); got != 2 {
		t.Fatalf("generation after delete = %d, want 2", got)
	}

	// A full snapshot republishes the source of truth, it does not change it.
	if _, err := broadcaster.BroadcastSync(ctx, "sync_1", gallerySyncTestItems(1)); err != nil {
		t.Fatalf("broadcast full sync: %v", err)
	}
	if got := broadcaster.Generation(); got != 2 {
		t.Fatalf("generation after full sync = %d, want 2 (unchanged)", got)
	}
}

// TestGalleryBroadcasterGenerationAdvancesBeforePublish covers the failure half
// of the contract: an unsendable payload must still leave the generation dirty,
// so the reconcile pass republishes instead of assuming the mutation landed.
func TestGalleryBroadcasterGenerationAdvancesBeforePublish(t *testing.T) {
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{}, zap.NewNop())

	err := broadcaster.BroadcastUpsert(context.Background(), GalleryUpsertPayload{
		FaceID:   "face_1",
		PersonID: "person_1",
		Features: []float32{float32(math.NaN())},
	})
	if err == nil {
		t.Fatal("upsert with a NaN feature encoded successfully, want a marshal error")
	}
	if got := broadcaster.Generation(); got != 1 {
		t.Fatalf("generation after a failed upsert = %d, want 1 (advances before publish)", got)
	}
}

// TestGalleryBroadcasterGenerationIgnoresMissingTransport keeps an unwired
// broadcaster at generation zero: with no transport there is nothing a snapshot
// could reconcile, and moving the counter would only mislead the guard.
func TestGalleryBroadcasterGenerationIgnoresMissingTransport(t *testing.T) {
	broadcaster := NewGalleryBroadcaster(nil, nil)

	if err := broadcaster.BroadcastUpsert(context.Background(), GalleryUpsertPayload{FaceID: "face_1"}); err != nil {
		t.Fatalf("broadcast upsert without transport: %v", err)
	}
	if err := broadcaster.BroadcastDelete(context.Background(), "face_1", ""); err != nil {
		t.Fatalf("broadcast delete without transport: %v", err)
	}
	if got := broadcaster.Generation(); got != 0 {
		t.Fatalf("generation without transport = %d, want 0", got)
	}
}
