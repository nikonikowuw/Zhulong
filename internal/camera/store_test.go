package camera

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupTestStore(t *testing.T) (CameraStore, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := NewCameraStore(dbStore)
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

type mockNilDBProvider struct{}

func (mockNilDBProvider) DB() *gorm.DB { return nil }

func TestGORMCameraStore_UnreadyDatabase(t *testing.T) {
	ctx := context.Background()

	for name, store := range map[string]CameraStore{
		"unready": NewCameraStore(mockNilDBProvider{}),
		"nil":     NewCameraStore(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.Create(ctx, &Camera{ID: "c1"}, nil); err == nil {
				t.Errorf("%s: expected error on Create, got nil", name)
			}
			if _, err := store.GetByID(ctx, "c1"); err == nil {
				t.Errorf("%s: expected error on GetByID, got nil", name)
			}
			if _, _, err := store.List(ctx, 10, 0); err == nil {
				t.Errorf("%s: expected error on List, got nil", name)
			}
			if _, err := store.Update(ctx, UpdateCameraParams{ID: "c1"}); err == nil {
				t.Errorf("%s: expected error on Update, got nil", name)
			}
			if err := store.Delete(ctx, "c1"); err == nil {
				t.Errorf("%s: expected error on Delete, got nil", name)
			}
			if _, err := store.HasAnyEncryptedStreams(ctx); err == nil {
				t.Errorf("%s: expected error on HasAnyEncryptedStreams, got nil", name)
			}
			if _, err := store.GetNextNumericID(ctx); err == nil {
				t.Errorf("%s: expected error on GetNextNumericID, got nil", name)
			}
		})
	}
}

func TestCameraStore_GetNextNumericID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Empty database should return "1"
	nextID, err := store.GetNextNumericID(ctx)
	if err != nil {
		t.Fatalf("expected no error on empty db, got %v", err)
	}
	if nextID != "1" {
		t.Fatalf("expected next ID to be '1', got %q", nextID)
	}

	// 2. Create camera with numeric ID "1"
	cam1 := &Camera{ID: "1", Name: "Cam 1", Enabled: true, Revision: 1}
	if err := store.Create(ctx, cam1, nil); err != nil {
		t.Fatalf("failed to create camera 1: %v", err)
	}

	// Next ID should be "2"
	nextID, err = store.GetNextNumericID(ctx)
	if err != nil {
		t.Fatalf("expected no error after cam 1, got %v", err)
	}
	if nextID != "2" {
		t.Fatalf("expected next ID to be '2', got %q", nextID)
	}

	// 3. Create camera with custom non-numeric ID "cam_gate" and "test_999"
	camCustom := &Camera{ID: "cam_gate", Name: "Gate", Enabled: true, Revision: 1}
	if err := store.Create(ctx, camCustom, nil); err != nil {
		t.Fatalf("failed to create custom camera: %v", err)
	}

	// Next ID should STILL be "2", ignoring "cam_gate"
	nextID, err = store.GetNextNumericID(ctx)
	if err != nil {
		t.Fatalf("expected no error after custom cam, got %v", err)
	}
	if nextID != "2" {
		t.Fatalf("expected next ID to remain '2', got %q", nextID)
	}

	// 4. Create camera with numeric ID "2"
	cam2 := &Camera{ID: "2", Name: "Cam 2", Enabled: true, Revision: 1}
	if err := store.Create(ctx, cam2, nil); err != nil {
		t.Fatalf("failed to create camera 2: %v", err)
	}

	// Next ID should be "3"
	nextID, err = store.GetNextNumericID(ctx)
	if err != nil {
		t.Fatalf("expected no error after cam 2, got %v", err)
	}
	if nextID != "3" {
		t.Fatalf("expected next ID to be '3', got %q", nextID)
	}
}
