package camera

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const (
	KeyLength       = 32
	NonceLength     = 12
	CipherVersionV1 = 0x01
	KeyFileName     = "camera.key"
	ExpectedKeyPerm = 0600
)

var (
	ErrKeyNotFound                = errors.New("camera encryption key not found")
	ErrKeyMissingWithExistingData = errors.New("camera encryption key missing while encrypted data exists in database")
	ErrInvalidKeySize             = errors.New("camera encryption key must be exactly 32 bytes")
	ErrInsecureKeyPermissions     = errors.New("camera encryption key file permissions must be 0600")
	ErrCiphertextTooShort         = errors.New("ciphertext too short")
	ErrUnsupportedCipherVersion   = errors.New("unsupported ciphertext format version")
	ErrDecryptionFailed           = errors.New("decryption failed or data tampered")
)

// Cipher manages AES-256-GCM encryption and decryption of sensitive camera credentials.
type Cipher interface {
	Encrypt(plaintext []byte, aad string) ([]byte, error)
	Decrypt(ciphertext []byte, aad string) ([]byte, error)
}

type gcmCipher struct {
	aead cipher.AEAD
}

// NewCipher creates a new Cipher using a 32-byte AES key.
func NewCipher(key []byte) (Cipher, error) {
	if len(key) != KeyLength {
		return nil, ErrInvalidKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &gcmCipher{aead: aead}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with a random 12-byte nonce and AAD.
// Output layout: [1-byte version 0x01] [12-byte nonce] [ciphertext + 16-byte tag]
func (c *gcmCipher) Encrypt(plaintext []byte, aad string) ([]byte, error) {
	nonce := make([]byte, NonceLength)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate random nonce: %w", err)
	}

	capacity := 1 + NonceLength + len(plaintext) + c.aead.Overhead()
	result := make([]byte, 1, capacity)
	result[0] = CipherVersionV1
	result = append(result, nonce...)

	result = c.aead.Seal(result, nonce, plaintext, []byte(aad))
	return result, nil
}

// Decrypt verifies and decrypts ciphertext using AES-256-GCM and AAD.
func (c *gcmCipher) Decrypt(ciphertext []byte, aad string) ([]byte, error) {
	minLen := 1 + NonceLength + c.aead.Overhead()
	if len(ciphertext) < minLen {
		return nil, ErrCiphertextTooShort
	}

	version := ciphertext[0]
	if version != CipherVersionV1 {
		return nil, ErrUnsupportedCipherVersion
	}

	nonce := ciphertext[1 : 1+NonceLength]
	encryptedData := ciphertext[1+NonceLength:]

	plaintext, err := c.aead.Open(nil, nonce, encryptedData, []byte(aad))
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

// KeyManager handles initialization, loading, and verification of the camera encryption master key.
type KeyManager struct {
	keyPath string
}

// NewKeyManager constructs a KeyManager targeting the specified data directory.
func NewKeyManager(dataDir string) *KeyManager {
	return &KeyManager{
		keyPath: filepath.Join(dataDir, KeyFileName),
	}
}

// KeyPath returns the absolute path to the key file.
func (km *KeyManager) KeyPath() string {
	return km.keyPath
}

// InitOrLoadKey initializes a new key if absent (provided no existing encrypted data exists),
// or loads and validates the existing key file.
func (km *KeyManager) InitOrLoadKey(hasExistingData bool) (Cipher, error) {
	info, err := os.Lstat(km.keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if hasExistingData {
				return nil, ErrKeyMissingWithExistingData
			}
			return km.generateAndSaveKey()
		}
		return nil, fmt.Errorf("stat camera key: %w", err)
	}

	// Disallow symlinks, pipes, dirs, or non-regular files
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("camera key at %s is not a regular file", km.keyPath)
	}

	// Verify file permissions on non-Windows platforms
	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm&0o077 != 0 {
			return nil, fmt.Errorf("%w: current perm is %04o, expected 0600", ErrInsecureKeyPermissions, perm)
		}
	}

	keyData, err := os.ReadFile(km.keyPath)
	if err != nil {
		return nil, fmt.Errorf("read camera key: %w", err)
	}
	if len(keyData) != KeyLength {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKeySize, len(keyData))
	}

	return NewCipher(keyData)
}

func (km *KeyManager) generateAndSaveKey() (Cipher, error) {
	key := make([]byte, KeyLength)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate random key: %w", err)
	}

	dir := filepath.Dir(km.keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}

	tempFile, err := os.CreateTemp(dir, "camera.key.tmp.*")
	if err != nil {
		return nil, fmt.Errorf("create temp key file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if runtime.GOOS != "windows" {
		if err := tempFile.Chmod(ExpectedKeyPerm); err != nil {
			_ = tempFile.Close()
			return nil, fmt.Errorf("chmod temp key file: %w", err)
		}
	}

	if _, err := tempFile.Write(key); err != nil {
		_ = tempFile.Close()
		return nil, fmt.Errorf("write temp key file: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return nil, fmt.Errorf("sync temp key file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp key file: %w", err)
	}

	if err := os.Rename(tempPath, km.keyPath); err != nil {
		return nil, fmt.Errorf("atomic rename key file: %w", err)
	}

	return NewCipher(key)
}

// MakeAAD constructs standard Additional Authenticated Data for camera stream encryption.
func MakeAAD(cameraID, role string) string {
	return fmt.Sprintf("v1:%s:%s", cameraID, role)
}
