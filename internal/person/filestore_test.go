package person

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

func TestPersonService_PluggableFileStoreAndLegacyKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbDir := t.TempDir()

	dbStore := database.New(dbDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close test db: %v", err)
		}
	})

	memStore := filestore.NewMemoryDriver()
	svc := NewService(
		NewStore(dbStore),
		t.TempDir(),
		zap.NewNop(),
		Options{
			Extractor: defaultTestExtractor(),
			FileStore: memStore,
		},
	)

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "RemoteUser"})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	face, err := svc.AddFaceWithReader(
		ctx,
		p.ID,
		"photo.png",
		bytes.NewReader(testPNGBytes),
	)
	if err != nil {
		t.Fatalf("AddFaceWithReader: %v", err)
	}

	// Stored under faces/<filename> in filestore while keeping plain filename in DB.
	remoteKey := "faces/" + face.ImagePath
	if ok, exErr := memStore.Exists(ctx, remoteKey); exErr != nil || !ok {
		t.Fatalf(
			"expected object %q in filestore, got ok=%v err=%v",
			remoteKey,
			ok,
			exErr,
		)
	}

	rc, info, err := svc.OpenFaceImage(ctx, p.ID, face.ID)
	if err != nil {
		t.Fatalf("OpenFaceImage: %v", err)
	}
	readBack, readErr := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close reader: %v", closeErr)
	}
	if readErr != nil || !bytes.Equal(readBack, testPNGBytes) || info.SizeBytes != int64(len(testPNGBytes)) {
		t.Fatalf(
			"unexpected image content/info: len=%d info=%+v err=%v",
			len(readBack),
			info,
			readErr,
		)
	}

	// Re-extraction reads stored image cleanly via filestore.
	reextracted, procErr := svc.readStoredFaceImage(face.ImagePath)
	if procErr != nil {
		t.Fatalf("readStoredFaceImage failed: %+v", procErr)
	}
	if !bytes.Equal(reextracted, testPNGBytes) {
		t.Fatalf("reextracted bytes mismatch")
	}

	// Legacy plain filename already present in filestore under faces/ resolves cleanly.
	putErr := memStore.Put(
		ctx,
		"faces/legacy_plain.jpg",
		bytes.NewReader(testPNGBytes),
		filestore.PutOptions{Size: int64(len(testPNGBytes))},
	)
	if putErr != nil {
		t.Fatalf("seed legacy object: %v", putErr)
	}
	legacyBytes, procErr := svc.readStoredFaceImage("legacy_plain.jpg")
	if procErr != nil || !bytes.Equal(legacyBytes, testPNGBytes) {
		t.Fatalf("expected legacy plain key resolution, err=%+v", procErr)
	}

	// Removing face removes object from filestore as well.
	if err := svc.DeleteFace(ctx, p.ID, face.ID); err != nil {
		t.Fatalf("DeleteFace: %v", err)
	}
	if exists, existsErr := memStore.Exists(ctx, remoteKey); existsErr != nil || exists {
		t.Fatalf(
			"expected object %q deleted after DeleteFace, exists=%v err=%v",
			remoteKey,
			exists,
			existsErr,
		)
	}
}
