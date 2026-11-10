package alertpush

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	signingKeyFileName = "media_url.key"
	signingKeyLength   = 32
	signingKeyPerm     = 0600
)

var (
	ErrInvalidSignerKey    = errors.New("invalid signing key length")
	ErrSignerNotConfigured = errors.New("url signer key is not configured")
	ErrPathNotAllowed      = errors.New("signed URL only supports snapshots")
	ErrURLExpired          = errors.New("signed URL has expired")
	ErrURLInvalid          = errors.New("signed URL signature is invalid")
)

// URLSigner manages HMAC-SHA256 signature generation and verification for media URLs.
type URLSigner struct {
	mu  sync.RWMutex
	key []byte
}

// NewURLSigner creates a URLSigner with the given secret key.
func NewURLSigner(key []byte) (*URLSigner, error) {
	if len(key) != signingKeyLength {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidSignerKey, signingKeyLength, len(key))
	}
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return &URLSigner{key: keyCopy}, nil
}

// NewEmptyURLSigner creates an uninitialized URLSigner to keep domain component constructors pure.
func NewEmptyURLSigner() *URLSigner {
	return &URLSigner{}
}

// SetKey safely assigns the secret signing key.
func (s *URLSigner) SetKey(key []byte) error {
	if len(key) != signingKeyLength {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidSignerKey, signingKeyLength, len(key))
	}
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = keyCopy
	return nil
}

// IsConfigured returns true if signing key is set.
func (s *URLSigner) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.key) == signingKeyLength
}

// Sign generates a base64url encoded signature for the given relative path and expiration timestamp.
func (s *URLSigner) Sign(relPath string, exp int64) (string, error) {
	normPath := normalizeRelativePath(relPath)
	if !strings.HasPrefix(normPath, "snapshots/") {
		return "", ErrPathNotAllowed
	}

	s.mu.RLock()
	key := s.key
	if len(key) == 0 {
		s.mu.RUnlock()
		return "", ErrSignerNotConfigured
	}
	mac := hmac.New(sha256.New, key)
	s.mu.RUnlock()

	payload := buildSignaturePayload(normPath, exp)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify validates whether the given signature is valid and not expired.
func (s *URLSigner) Verify(relPath string, exp int64, sig string) error {
	normPath := normalizeRelativePath(relPath)
	if !strings.HasPrefix(normPath, "snapshots/") {
		return ErrPathNotAllowed
	}

	s.mu.RLock()
	hasKey := len(s.key) > 0
	s.mu.RUnlock()
	if !hasKey {
		return ErrSignerNotConfigured
	}

	now := time.Now().Unix()
	if exp < now {
		return ErrURLExpired
	}

	expectedSig, err := s.Sign(normPath, exp)
	if err != nil {
		return err
	}

	providedBytes, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return ErrURLInvalid
	}

	expectedBytes, err := base64.RawURLEncoding.DecodeString(expectedSig)
	if err != nil {
		return ErrURLInvalid
	}

	if !hmac.Equal(providedBytes, expectedBytes) {
		return ErrURLInvalid
	}
	return nil
}

// initSignerFromDataDir loads media_url.key from dataDir or generates a new one atomically, populating the signer.
func initSignerFromDataDir(signer *URLSigner, dataDir string) error {
	keyPath := filepath.Join(dataDir, signingKeyFileName)
	data, err := os.ReadFile(keyPath)
	if err == nil {
		return signer.SetKey(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read signing key: %w", err)
	}

	key := make([]byte, signingKeyLength)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("generate signing key: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	tempFile, err := os.CreateTemp(dataDir, "media_url.key.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp signing key file: %w", err)
	}
	tempPath := tempFile.Name()
	var renamed bool
	defer func() {
		if !renamed {
			if rmErr := os.Remove(tempPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				return
			}
		}
	}()

	if runtime.GOOS != "windows" {
		if err := tempFile.Chmod(signingKeyPerm); err != nil {
			return errors.Join(fmt.Errorf("chmod temp signing key file: %w", err), tempFile.Close())
		}
	}

	if _, err := tempFile.Write(key); err != nil {
		return errors.Join(fmt.Errorf("write temp signing key file: %w", err), tempFile.Close())
	}
	if err := tempFile.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync temp signing key file: %w", err), tempFile.Close())
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temp signing key file: %w", err)
	}
	if err := os.Rename(tempPath, keyPath); err != nil {
		return fmt.Errorf("rename signing key file: %w", err)
	}
	renamed = true

	return signer.SetKey(key)
}

// loadOrCreateSigningKey loads media_url.key from dataDir or generates a new one atomically.
func loadOrCreateSigningKey(dataDir string) (*URLSigner, error) {
	signer := NewEmptyURLSigner()
	if err := initSignerFromDataDir(signer, dataDir); err != nil {
		return nil, err
	}
	return signer, nil
}

// normalizeRelativePath strips leading slashes and cleans relative media path.
func normalizeRelativePath(p string) string {
	clean := strings.TrimPrefix(filepath.ToSlash(p), "/")
	return strings.TrimPrefix(clean, "./")
}

func buildSignaturePayload(normPath string, exp int64) string {
	return "v1\n" + normPath + "\n" + strconv.FormatInt(exp, 10)
}
