package camera

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupTestStore(t *testing.T) (CameraStore, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := NewCameraStore(dbStore.DB)
	cleanup := func() {
		_ = dbStore.Close()
	}
	return store, cleanup
}

func TestCameraStoreCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Initially empty
	hasEncrypted, err := store.HasAnyEncryptedStreams(ctx)
	if err != nil {
		t.Fatalf("HasAnyEncryptedStreams failed: %v", err)
	}
	if hasEncrypted {
		t.Fatal("expected no encrypted streams initially")
	}

	cams, total, err := store.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 0 || len(cams) != 0 {
		t.Fatalf("expected empty list, got total=%d len=%d", total, len(cams))
	}

	// 2. Create camera with main stream
	cam := &Camera{
		ID:       "cam_gate_01",
		Name:     "Front Gate",
		Enabled:  true,
		Revision: 1,
	}
	streams := []CameraStream{
		{
			Role:           StreamRoleMain,
			Protocol:       ProtocolRTSP,
			EncryptedURI:   []byte("encrypted_main_blob"),
			Transport:      TransportTCP,
			Codec:          "h264",
			Width:          1920,
			Height:         1080,
			FPSNumerator:   25,
			FPSDenominator: 1,
		},
	}

	if err := store.Create(ctx, cam, streams); err != nil {
		t.Fatalf("Create camera failed: %v", err)
	}

	// HasAnyEncryptedStreams should now be true
	hasEncrypted, err = store.HasAnyEncryptedStreams(ctx)
	if err != nil || !hasEncrypted {
		t.Fatalf("expected HasAnyEncryptedStreams to be true, got %v (err=%v)", hasEncrypted, err)
	}

	// 3. GetByID
	fetched, err := store.GetByID(ctx, "cam_gate_01")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched.Name != "Front Gate" || len(fetched.Streams) != 1 {
		t.Fatalf("unexpected fetched camera: %+v", fetched)
	}
	if fetched.Streams[0].Role != StreamRoleMain || fetched.Streams[0].Width != 1920 {
		t.Fatalf("unexpected stream info: %+v", fetched.Streams[0])
	}

	// 4. Update with revision CAS
	newName := "Front Gate Updated"
	updatedStreams := []CameraStream{
		{
			Role:           StreamRoleMain,
			Protocol:       ProtocolRTSP,
			EncryptedURI:   []byte("encrypted_main_blob_v2"),
			Transport:      TransportTCP,
			Codec:          "h265",
			Width:          3840,
			Height:         2160,
			FPSNumerator:   30,
			FPSDenominator: 1,
		},
		{
			Role:           StreamRoleSub,
			Protocol:       ProtocolRTSP,
			EncryptedURI:   []byte("encrypted_sub_blob_v2"),
			Transport:      TransportTCP,
			Codec:          "h264",
			Width:          1280,
			Height:         720,
			FPSNumerator:   15,
			FPSDenominator: 1,
		},
	}

	// Mismatched revision should fail
	_, err = store.Update(ctx, UpdateCameraParams{
		ID:               "cam_gate_01",
		ExpectedRevision: 999,
		Name:             &newName,
		Streams:          updatedStreams,
	})
	if err != ErrRevisionConflict {
		t.Fatalf("expected ErrRevisionConflict on wrong revision, got %v", err)
	}

	// Correct revision 1 -> should succeed and bump to revision 2
	updated, err := store.Update(ctx, UpdateCameraParams{
		ID:               "cam_gate_01",
		ExpectedRevision: 1,
		Name:             &newName,
		Streams:          updatedStreams,
	})
	if err != nil {
		t.Fatalf("Update with revision 1 failed: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", updated.Revision)
	}
	if updated.Name != newName || len(updated.Streams) != 2 {
		t.Fatalf("unexpected updated camera: %+v", updated)
	}

	// 5. List
	cams, total, err = store.List(ctx, 10, 0)
	if err != nil || total != 1 || len(cams) != 1 {
		t.Fatalf("List expected 1 item, got total=%d len=%d err=%v", total, len(cams), err)
	}

	// 6. Delete
	if err := store.Delete(ctx, "cam_gate_01"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify not found
	_, err = store.GetByID(ctx, "cam_gate_01")
	if err != ErrCameraNotFound {
		t.Fatalf("expected ErrCameraNotFound, got %v", err)
	}

	hasEncrypted, err = store.HasAnyEncryptedStreams(ctx)
	if err != nil || hasEncrypted {
		t.Fatalf("expected no encrypted streams after deletion, got %v", hasEncrypted)
	}
}
