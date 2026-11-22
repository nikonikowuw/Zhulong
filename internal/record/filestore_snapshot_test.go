package record_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

func TestSnapshotStore_WithMemoryFileStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	memStore := filestore.NewMemoryDriver()
	putInvalidErr := memStore.Put(
		ctx,
		"../invalid.jpg",
		bytes.NewReader(nil),
		filestore.PutOptions{
			Size:        0,
			ContentType: "image/jpeg",
		},
	)
	if putInvalidErr == nil {
		t.Fatalf("expected invalid escaping key put to fail")
	}

	payload := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	putValidErr := memStore.Put(
		ctx,
		"snapshots/cam-01/2026-11-22/alm-1.jpg",
		bytes.NewReader(payload),
		filestore.PutOptions{
			Size:        int64(len(payload)),
			ContentType: "image/jpeg",
			Protected:   true,
		},
	)
	if putValidErr != nil {
		t.Fatalf("Put failed: %v", putValidErr)
	}

	sink := record.NewSnapshotStore(
		nil,
		nil,
		nil,
		0,
		zap.NewNop(),
	)
	sink.SetFileStore(memStore)
	if sink.Backend() != filestore.BackendMemory {
		t.Fatalf("expected backend memory, got %q", sink.Backend())
	}

	// Non-local stores rely on EvidenceGuard instead of ".lock" sidecar files.
	written, err := sink.EnsureEvidenceLocks(ctx, []string{"snapshots/cam-01/2026-11-22/alm-1.jpg"})
	if err != nil {
		t.Fatalf("EnsureEvidenceLocks failed on remote backend: %v", err)
	}
	if written != 0 {
		t.Fatalf("expected 0 sidecar locks created on non-local store, got %d", written)
	}
}
