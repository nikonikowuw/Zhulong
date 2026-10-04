package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// SessionItem represents an active in-memory session.
type SessionItem struct {
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

// SessionStore defines the in-memory session management contract.
type SessionStore interface {
	Create(userID int64, username string) (string, error)
	Get(token string) (SessionItem, bool)
	Delete(token string)
}

// MemorySessionStore provides a thread-safe in-memory session repository.
type MemorySessionStore struct {
	mu       sync.RWMutex
	ttl      time.Duration
	sessions map[string]SessionItem
	nowFunc  func() time.Time
}

// NewMemorySessionStore creates an initialized in-memory session store.
func NewMemorySessionStore(ttl time.Duration) *MemorySessionStore {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &MemorySessionStore{
		ttl:      ttl,
		sessions: make(map[string]SessionItem),
		nowFunc:  func() time.Time { return time.Now().UTC() },
	}
}

// Create generates a secure 64-character token and stores the session item.
func (s *MemorySessionStore) Create(userID int64, username string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(bytes)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFunc()
	for k, v := range s.sessions {
		if now.After(v.ExpiresAt) {
			delete(s.sessions, k)
		}
	}

	s.sessions[token] = SessionItem{
		UserID:    userID,
		Username:  username,
		ExpiresAt: now.Add(s.ttl),
	}

	return token, nil
}

// Get retrieves an active session item or false if not found or expired.
func (s *MemorySessionStore) Get(token string) (SessionItem, bool) {
	if token == "" {
		return SessionItem{}, false
	}

	s.mu.RLock()
	item, ok := s.sessions[token]
	s.mu.RUnlock()

	if !ok {
		return SessionItem{}, false
	}

	if s.nowFunc().After(item.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		return SessionItem{}, false
	}

	return item, true
}

// Delete invalidates a specific session by token.
func (s *MemorySessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}
