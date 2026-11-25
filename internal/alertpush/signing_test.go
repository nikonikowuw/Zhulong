package alertpush

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestURLSigner(t *testing.T) {
	tempDir := t.TempDir()
	signer, err := loadOrCreateSigningKey(tempDir)
	if err != nil {
		t.Fatalf("loadOrCreateSigningKey failed: %v", err)
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
	expiredSig, err := signer.Sign(relPath, pastExp)
	if err != nil {
		t.Fatalf("Sign expired failed: %v", err)
	}
	if err := signer.Verify(relPath, pastExp, expiredSig); !errors.Is(err, ErrURLExpired) {
		t.Fatalf("expected ErrURLExpired, got %v", err)
	}

	// 4. Tampered signature rejected
	if err := signer.Verify(relPath, futureExp, sig+"tamper"); !errors.Is(err, ErrURLInvalid) {
		t.Fatalf("expected ErrURLInvalid, got %v", err)
	}

	// 5. Tampered path rejected
	err = signer.Verify("snapshots/cam_01/2026-11-03/other.jpg", futureExp, sig)
	if !errors.Is(err, ErrURLInvalid) {
		t.Fatalf("expected ErrURLInvalid for tampered path, got %v", err)
	}

	// 6. Face gallery single-level path allowed, nested or other namespaces rejected
	facePath := "faces/person_alice_face_01.png"
	faceSig, err := signer.Sign(facePath, futureExp)
	if err != nil {
		t.Fatalf("Sign face path failed: %v", err)
	}
	if err := signer.Verify(facePath, futureExp, faceSig); err != nil {
		t.Fatalf("Verify valid face path failed: %v", err)
	}

	disallowedPaths := []string{
		"recordings/cam_01/test.mp4",
		"exports/clip.mp4",
		"maps/floor1.png",
		"faces/nested/secret.jpg",
		"faces/../snapshots/cam_01/a.jpg",
		"person_alice_face_01.jpg",
	}
	for _, disallowed := range disallowedPaths {
		if _, err := signer.Sign(disallowed, futureExp); !errors.Is(err, ErrPathNotAllowed) {
			t.Fatalf("Sign(%q): expected ErrPathNotAllowed, got %v", disallowed, err)
		}
		if err := signer.Verify(disallowed, futureExp, sig); !errors.Is(err, ErrPathNotAllowed) {
			t.Fatalf("Verify(%q): expected ErrPathNotAllowed, got %v", disallowed, err)
		}
	}

	// 7. Reload from existing key file produces identical signature
	reloaded, err := loadOrCreateSigningKey(tempDir)
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
	signer, err := loadOrCreateSigningKey(subDir)
	if err != nil {
		t.Fatalf("failed to create key in non-existent directory: %v", err)
	}
	if signer == nil {
		t.Fatal("expected non-nil signer")
	}
}

func TestURLSigner_VerifyWithExpiryRejectsForgedExpiredAsInvalid(t *testing.T) {
	signer, err := NewURLSigner(bytes.Repeat([]byte{0x77}, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	// Forged signature with expired timestamp must return ErrURLInvalid, not ErrURLExpired.
	_, _, err = signer.VerifyWithExpiry("snapshots/cam_01/a.jpg", "1", "forged-sig")
	if !errors.Is(err, ErrURLInvalid) {
		t.Fatalf("VerifyWithExpiry forged expired = %v, want ErrURLInvalid", err)
	}
}

func TestInitSignerFromDataDir_RejectsSymlinkAndPermissiveMode(t *testing.T) {
	t.Run("rejects symlink", func(t *testing.T) {
		dir := t.TempDir()
		realPath := filepath.Join(dir, "target.key")
		if err := os.WriteFile(realPath, bytes.Repeat([]byte{0x11}, 32), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(realPath, filepath.Join(dir, signingKeyFileName)); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if _, err := loadOrCreateSigningKey(dir); !errors.Is(err, ErrInsecureSignerKey) {
			t.Fatalf("loadOrCreateSigningKey symlink = %v, want ErrInsecureSignerKey", err)
		}
	})

	t.Run("rejects 0644 permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits skipped on Windows")
		}
		dir := t.TempDir()
		keyPath := filepath.Join(dir, signingKeyFileName)
		if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0x22}, 32), 0o644); err != nil {
			t.Fatalf("write 0644 key: %v", err)
		}
		if _, err := loadOrCreateSigningKey(dir); !errors.Is(err, ErrInsecureSignerKeyPerm) {
			t.Fatalf("loadOrCreateSigningKey 0644 = %v, want ErrInsecureSignerKeyPerm", err)
		}
	})
}
