package alertpush

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestURLSigner(t *testing.T) {
	tempDir := t.TempDir()
	signer, err := LoadOrCreateSigningKey(tempDir)
	if err != nil {
		t.Fatalf("LoadOrCreateSigningKey failed: %v", err)
	}

	relPath := "snapshots/cam_01/2026-11-03/01J8Z.jpg"
	futureExp := time.Now().Add(1 * time.Hour).Unix()

	sig, err := signer.Sign(relPath, futureExp)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if sig == "" {
		t.Fatal("expected non-empty signature")
	}

	// 1. Verify valid signature
	if err := signer.Verify(relPath, futureExp, sig); err != nil {
		t.Fatalf("Verify valid signature failed: %v", err)
	}

	// 2. Leading slash normalized
	if err := signer.Verify("/"+relPath, futureExp, sig); err != nil {
		t.Fatalf("Verify with leading slash failed: %v", err)
	}

	// 3. Expired signature rejected
	pastExp := time.Now().Add(-10 * time.Second).Unix()
	expiredSig, _ := signer.Sign(relPath, pastExp)
	if err := signer.Verify(relPath, pastExp, expiredSig); err != ErrURLExpired {
		t.Fatalf("expected ErrURLExpired, got %v", err)
	}

	// 4. Tampered signature rejected
	if err := signer.Verify(relPath, futureExp, sig+"tamper"); err != ErrURLInvalid {
		t.Fatalf("expected ErrURLInvalid, got %v", err)
	}

	// 5. Tampered path rejected
	if err := signer.Verify("snapshots/cam_01/2026-11-03/other.jpg", futureExp, sig); err != ErrURLInvalid {
		t.Fatalf("expected ErrURLInvalid for tampered path, got %v", err)
	}

	// 6. Path outside snapshots rejected
	if _, err := signer.Sign("recordings/cam_01/test.mp4", futureExp); err != ErrPathNotAllowed {
		t.Fatalf("expected ErrPathNotAllowed, got %v", err)
	}
	if err := signer.Verify("recordings/cam_01/test.mp4", futureExp, sig); err != ErrPathNotAllowed {
		t.Fatalf("expected ErrPathNotAllowed, got %v", err)
	}

	// 7. Reload from existing key file produces identical signature
	reloaded, err := LoadOrCreateSigningKey(tempDir)
	if err != nil {
		t.Fatalf("reloading signing key failed: %v", err)
	}
	sigReloaded, err := reloaded.Sign(relPath, futureExp)
	if err != nil {
		t.Fatalf("Sign on reloaded failed: %v", err)
	}
	if sigReloaded != sig {
		t.Fatalf("expected identical signature after reload, got %s vs %s", sigReloaded, sig)
	}
}

func TestURLSignerInvalidKeyLength(t *testing.T) {
	if _, err := NewURLSigner([]byte("too-short")); !errors.Is(err, ErrInvalidSignerKey) {
		t.Fatalf("expected ErrInvalidSignerKey, got %v", err)
	}
}

func TestLoadOrCreateSigningKeyExistingDir(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "keys")
	signer, err := LoadOrCreateSigningKey(subDir)
	if err != nil {
		t.Fatalf("failed to create key in non-existent directory: %v", err)
	}
	if signer == nil {
		t.Fatal("expected non-nil signer")
	}
}
