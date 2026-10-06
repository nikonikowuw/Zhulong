package camera

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// LazyCipher provides a thread-safe deferred Cipher implementation initialized during runtime lifecycle start.
type LazyCipher struct {
	mu     sync.RWMutex
	cipher Cipher
}

// NewLazyCipher creates an uninitialized LazyCipher.
func NewLazyCipher() *LazyCipher {
	return &LazyCipher{}
}

// Set sets the underlying active Cipher.
func (l *LazyCipher) Set(c Cipher) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cipher = c
}

// Encrypt encrypts plaintext using the initialized Cipher.
func (l *LazyCipher) Encrypt(plaintext []byte, aad string) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.cipher == nil {
		return nil, errors.New("camera cipher is not initialized")
	}
	return l.cipher.Encrypt(plaintext, aad)
}

// Decrypt decrypts ciphertext using the initialized Cipher.
func (l *LazyCipher) Decrypt(ciphertext []byte, aad string) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.cipher == nil {
		return nil, errors.New("camera cipher is not initialized")
	}
	return l.cipher.Decrypt(ciphertext, aad)
}

// LifecycleManager manages cipher loading and scheduler/event hub lifecycle.
type LifecycleManager struct {
	store      CameraStore
	keyMgr     *KeyManager
	lazyCipher *LazyCipher
	scheduler  *HealthScheduler
	hub        *EventHub
	streamHub  *StreamHub
}

// NewLifecycleManager creates a LifecycleManager.
func NewLifecycleManager(
	store CameraStore,
	keyMgr *KeyManager,
	lazyCipher *LazyCipher,
	scheduler *HealthScheduler,
	hub *EventHub,
	streamHub *StreamHub,
) *LifecycleManager {
	return &LifecycleManager{
		store:      store,
		keyMgr:     keyMgr,
		lazyCipher: lazyCipher,
		scheduler:  scheduler,
		hub:        hub,
		streamHub:  streamHub,
	}
}

// InitCipher validates existing data and initializes the camera encryption cipher.
func (m *LifecycleManager) InitCipher(ctx context.Context) error {
	hasData, err := m.store.HasAnyEncryptedStreams(ctx)
	if err != nil {
		return fmt.Errorf("check existing encrypted camera streams: %w", err)
	}
	cipher, err := m.keyMgr.InitOrLoadKey(hasData)
	if err != nil {
		return fmt.Errorf("initialize camera encryption key: %w", err)
	}
	m.lazyCipher.Set(cipher)
	return nil
}

// Start starts the background scheduler.
func (m *LifecycleManager) Start(ctx context.Context) error {
	return m.scheduler.Start(ctx)
}

// Stop cleanly terminates the event hub, stream hub and scheduler.
func (m *LifecycleManager) Stop() {
	if m.streamHub != nil {
		_ = m.streamHub.Close()
	}
	_ = m.hub.Close()
	m.scheduler.Stop()
}
