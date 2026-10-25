package app

import (
	"context"
	"errors"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/gb28181"
	"go.uber.org/zap"
)

// gbWiringCipher stands in for the AES-GCM cipher: the subject of this test is the
// probe registration, not the encryption, which camera covers on its own.
type gbWiringCipher struct{}

func (gbWiringCipher) Encrypt(plaintext []byte, _ string) ([]byte, error) {
	return append([]byte("cipher:"), plaintext...), nil
}

func (gbWiringCipher) Decrypt(ciphertext []byte, _ string) ([]byte, error) {
	return ciphertext, nil
}

// TestProvideGB28181RegistersEncryptedDataProbe pins the cross-domain key-integrity
// contract at the assembly level: a database whose only ciphertext is a GB28181
// device password must still forbid regenerating a missing camera.key.
//
// A store unit test cannot cover this, and fx.ValidateApp only checks the
// dependency graph — neither observes whether AddEncryptedDataProbe is actually
// called during assembly.
func TestProvideGB28181RegistersEncryptedDataProbe(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	// Seed exactly one GB28181 device credential and nothing else.
	seedStore := gb28181.NewStore(dbStore, gbWiringCipher{})
	password := "digest-secret"
	if _, err := seedStore.UpsertDevice(context.Background(), gb28181.DeviceUpsert{
		DeviceID: "34020000001320000010",
		Password: &password,
	}); err != nil {
		t.Fatalf("seed GB28181 device: %v", err)
	}

	manager := camera.NewLifecycleManager(
		camera.NewCameraStore(dbStore),
		camera.NewKeyManager(dataDir),
		camera.NewLazyCipher(),
		nil, nil, nil,
	)
	provideGB28181(dbStore, camera.NewLazyCipher(), manager, zap.NewNop())

	err := manager.InitCipher(context.Background())
	if !errors.Is(err, camera.ErrKeyMissingWithExistingData) {
		t.Fatalf("expected InitCipher to refuse regenerating camera.key while GB credentials exist, got %v", err)
	}
}
