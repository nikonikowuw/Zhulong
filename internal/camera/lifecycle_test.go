package camera

import (
	"context"
	"errors"
	"testing"
)

// stubProbe stands in for another domain (GB28181 device credentials) that also
// stores ciphertext protected by camera.key.
type stubProbe struct {
	has bool
	err error
}

func (s stubProbe) HasExistingEncryptedData(context.Context) (bool, error) {
	return s.has, s.err
}

func newCipherTestManager(t *testing.T, dataDir string) (*LifecycleManager, *LazyCipher) {
	t.Helper()
	store, cleanup := setupTestStore(t)
	t.Cleanup(cleanup)

	lazy := NewLazyCipher()
	manager := NewLifecycleManager(store, NewKeyManager(dataDir), lazy, nil, nil, nil)
	return manager, lazy
}

// TestInitCipherRefusesRegenerationWhenExternalCiphertextExists is the guard that
// keeps a GB-only database from losing every device password: the camera table is
// empty, so without the probe the key manager would happily mint a new key file.
func TestInitCipherRefusesRegenerationWhenExternalCiphertextExists(t *testing.T) {
	manager, _ := newCipherTestManager(t, t.TempDir())
	manager.AddEncryptedDataProbe(stubProbe{has: true})

	err := manager.InitCipher(context.Background())
	if !errors.Is(err, ErrKeyMissingWithExistingData) {
		t.Fatalf("expected %v, got %v", ErrKeyMissingWithExistingData, err)
	}
}

// TestInitCipherGeneratesKeyWhenNoCiphertextExists keeps the happy path honest:
// a genuinely empty host must still be able to create its key.
func TestInitCipherGeneratesKeyWhenNoCiphertextExists(t *testing.T) {
	dir := t.TempDir()
	manager, lazy := newCipherTestManager(t, dir)
	manager.AddEncryptedDataProbe(stubProbe{has: false})

	if err := manager.InitCipher(context.Background()); err != nil {
		t.Fatalf("InitCipher failed: %v", err)
	}
	if _, err := lazy.Encrypt([]byte("probe"), "aad"); err != nil {
		t.Fatalf("expected the cipher to be usable after init: %v", err)
	}
}

// TestInitCipherPropagatesProbeFailure refuses to guess: a probe that cannot answer
// must not be treated as "no ciphertext exists".
func TestInitCipherPropagatesProbeFailure(t *testing.T) {
	manager, _ := newCipherTestManager(t, t.TempDir())
	manager.AddEncryptedDataProbe(stubProbe{err: errors.New("database is not ready")})

	err := manager.InitCipher(context.Background())
	if err == nil {
		t.Fatal("expected a probe failure to abort key initialization")
	}
	if errors.Is(err, ErrKeyMissingWithExistingData) {
		t.Fatalf("a probe failure must be distinguishable from a missing key, got %v", err)
	}
}

// TestNewLifecycleManagerHasNoProbesByDefault pins that camera alone still drives
// the decision when no other domain registered one.
func TestNewLifecycleManagerHasNoProbesByDefault(t *testing.T) {
	manager, _ := newCipherTestManager(t, t.TempDir())
	if len(manager.extraProbes) != 0 {
		t.Fatalf("expected no extra probes, got %d", len(manager.extraProbes))
	}
	// A nil probe is ignored rather than stored, so assembly cannot inject a
	// panic-on-use sentinel by accident.
	manager.AddEncryptedDataProbe(nil)
	if len(manager.extraProbes) != 0 {
		t.Fatal("AddEncryptedDataProbe(nil) must be a no-op")
	}
}
