package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultSessionTTL is the duration an inactive session remains valid.
	DefaultSessionTTL = 24 * time.Hour

	// DefaultSessionCookieMaxAge is the cookie lifetime matching DefaultSessionTTL in seconds.
	DefaultSessionCookieMaxAge = int(DefaultSessionTTL / time.Second)
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
	TTL() time.Duration
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
		ttl = DefaultSessionTTL
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
// Valid sessions have their expiration extended by the configured TTL (sliding expiration).
func (s *MemorySessionStore) Get(token string) (SessionItem, bool) {
	if token == "" {
		return SessionItem{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.sessions[token]
	if !ok {
		return SessionItem{}, false
	}

	now := s.nowFunc()
	if now.After(item.ExpiresAt) {
		delete(s.sessions, token)
		return SessionItem{}, false
	}

	item.ExpiresAt = now.Add(s.ttl)
	s.sessions[token] = item

	return item, true
}

// Delete invalidates a specific session by token.
func (s *MemorySessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// TTL returns the configured session duration.
func (s *MemorySessionStore) TTL() time.Duration {
	return s.ttl
}
