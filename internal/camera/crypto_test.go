package camera

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestGCMCipherEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher failed: %v", err)
	}

	plaintext := []byte("rtsp://admin:pass123@192.168.1.100:554/live/main")
	aad := MakeAAD("cam_test", StreamRoleMain)

	ciphertext, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if len(ciphertext) <= len(plaintext)+13 {
		t.Fatalf("ciphertext too short: %d", len(ciphertext))
	}

	decrypted, err := cipher.Decrypt(ciphertext, aad)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted %q does not match original %q", decrypted, plaintext)
	}
}

func TestGCMCipherTamperDetection(t *testing.T) {
	key := make([]byte, 32)
	cipher, _ := NewCipher(key)

	plaintext := []byte("secret_stream_uri")
	aad := MakeAAD("cam_test", StreamRoleMain)

	ciphertext, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Tamper with AAD
	_, err = cipher.Decrypt(ciphertext, MakeAAD("cam_tampered", StreamRoleMain))
	if err == nil {
		t.Fatal("expected decryption error with mismatched AAD")
	}

	// Tamper with ciphertext byte
	tamperedCiphertext := append([]byte(nil), ciphertext...)
	tamperedCiphertext[len(tamperedCiphertext)-1] ^= 0x01
	_, err = cipher.Decrypt(tamperedCiphertext, aad)
	if err == nil {
		t.Fatal("expected decryption error with tampered ciphertext")
	}

	// Tamper with version byte
	tamperedVersion := append([]byte(nil), ciphertext...)
	tamperedVersion[0] = 0x02
	_, err = cipher.Decrypt(tamperedVersion, aad)
	if err == nil {
		t.Fatal("expected decryption error with unknown version byte")
	}
}

func TestKeyManagerInitOrLoadKey(t *testing.T) {
	tempDir := t.TempDir()
	km := NewKeyManager(tempDir)

	// 1. First run without existing data: should generate key
	cipher1, err := km.InitOrLoadKey(false)
	if err != nil {
		t.Fatalf("InitOrLoadKey (create) failed: %v", err)
	}

	// Verify key file exists with 0600 permissions
	info, err := os.Stat(km.KeyPath())
	if err != nil {
		t.Fatalf("Stat key file failed: %v", err)
	}
	if info.Size() != 32 {
		t.Fatalf("expected 32 bytes key file, got %d", info.Size())
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("expected 0600 permissions, got %04o", perm)
		}
	}

	// 2. Encrypt some data with cipher1
	plaintext := []byte("rtsp://example.com/stream")
	aad := MakeAAD("cam_1", StreamRoleMain)
	ciphertext, err := cipher1.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// 3. Second run with existing key: should load same key
	cipher2, err := km.InitOrLoadKey(true)
	if err != nil {
		t.Fatalf("InitOrLoadKey (load) failed: %v", err)
	}

	decrypted, err := cipher2.Decrypt(ciphertext, aad)
	if err != nil {
		t.Fatalf("Decrypt with loaded key failed: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted %q != plaintext %q", decrypted, plaintext)
	}
}

func TestKeyManagerRefusesStartupIfKeyMissingWithExistingData(t *testing.T) {
	tempDir := t.TempDir()
	km := NewKeyManager(tempDir)

	// Key file does not exist, but database indicates ciphertext exists
	_, err := km.InitOrLoadKey(true)
	if err == nil {
		t.Fatal("expected error when key is missing and existing data is present")
	}
	if err != ErrKeyMissingWithExistingData {
		t.Fatalf("expected ErrKeyMissingWithExistingData, got: %v", err)
	}
}

func TestKeyManagerRejectsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping permission check on Windows")
	}

	tempDir := t.TempDir()
	km := NewKeyManager(tempDir)

	// Create insecure key file (0644)
	keyData := make([]byte, 32)
	if err := os.WriteFile(km.KeyPath(), keyData, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := km.InitOrLoadKey(false)
	if err == nil {
		t.Fatal("expected error on insecure permissions")
	}
}

func TestKeyManagerConcurrentEncryption(t *testing.T) {
	tempDir := t.TempDir()
	km := NewKeyManager(tempDir)
	cipher, err := km.InitOrLoadKey(false)
	if err != nil {
		t.Fatalf("InitOrLoadKey failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			plaintext := []byte(filepath.Join("rtsp://test", string(rune('a'+idx))))
			aad := MakeAAD("cam_conc", StreamRoleMain)
			ct, err := cipher.Encrypt(plaintext, aad)
			if err != nil {
				t.Errorf("concurrent encrypt failed: %v", err)
				return
			}
			pt, err := cipher.Decrypt(ct, aad)
			if err != nil {
				t.Errorf("concurrent decrypt failed: %v", err)
				return
			}
			if !bytes.Equal(pt, plaintext) {
				t.Errorf("concurrent decrypted mismatch")
			}
		}(i)
	}
	wg.Wait()
}
