package apitoken

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

// Identity represents the authenticated caller identity injected into Gin context.
type Identity struct {
	ID   string
	Name string
}

// Service provides high-level business operations for managing and authenticating API tokens.
type Service struct {
	store       *Store
	logger      *zap.Logger
	stopCh      chan struct{}
	closeOnce   sync.Once
	wg          sync.WaitGroup
	mu          sync.Mutex
	closed      bool
	lastUsedMap map[string]time.Time
}

// NewService creates a new API token service.
func NewService(store *Store, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:       store,
		logger:      logger,
		stopCh:      make(chan struct{}),
		lastUsedMap: make(map[string]time.Time),
	}
}

// CreateToken creates a new API token. The raw token is returned once and never persisted in plaintext.
func (s *Service) CreateToken(ctx context.Context, rawName string) (*APIToken, string, error) {
	cleanName, err := normalizeTokenName(rawName)
	if err != nil {
		switch {
		case errors.Is(err, errTokenNameRequired):
			return nil, "", apperr.Invalid("API_TOKEN_NAME_REQUIRED", "Token name is required", err)
		case errors.Is(err, errTokenNameTooLong):
			return nil, "", apperr.Invalid("API_TOKEN_NAME_TOO_LONG", "Token name exceeds 48 characters", err)
		case errors.Is(err, errTokenNameInvalid):
			return nil, "", apperr.Invalid("API_TOKEN_NAME_INVALID", "Token name contains invalid characters", err)
		default:
			return nil, "", apperr.Invalid("VALIDATION_FAILED", "Invalid token name", err)
		}
	}

	raw, hash, masked, err := GenerateToken()
	if err != nil {
		return nil, "", apperr.Internal("INTERNAL_ERROR", "Failed to generate token", err)
	}

	token := &APIToken{
		ID:        idutil.New(),
		Name:      cleanName,
		TokenHash: hash,
		MaskedKey: masked,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.store.CreateToken(ctx, token); err != nil {
		switch {
		case errors.Is(err, errTokenLimitReached):
			return nil, "", apperr.Conflict("API_TOKEN_LIMIT_REACHED", "Active API token limit (10) reached", err)
		case errors.Is(err, errTokenNameConflict):
			return nil, "", apperr.Conflict(
				"API_TOKEN_NAME_ALREADY_EXISTS",
				"API token name already exists (including revoked tokens)",
				err,
			)
		default:
			return nil, "", apperr.Internal("INTERNAL_ERROR", "Failed to persist API token", err)
		}
	}

	return token, raw, nil
}

// ListTokens returns tokens. If includeRevoked is false, only active tokens are returned.
func (s *Service) ListTokens(ctx context.Context, includeRevoked bool) ([]APIToken, error) {
	tokens, err := s.store.ListTokens(ctx, includeRevoked)
	if err != nil {
		return nil, apperr.Internal("INTERNAL_ERROR", "Failed to list API tokens", err)
	}
	return tokens, nil
}

// RevokeToken soft-deletes a token by ID.
func (s *Service) RevokeToken(ctx context.Context, id string) error {
	if err := s.store.Revoke(ctx, id); err != nil {
		if errors.Is(err, errTokenNotFound) {
			return apperr.NotFound("API_TOKEN_NOT_FOUND", "API token not found", err)
		}
		return apperr.Internal("INTERNAL_ERROR", "Failed to revoke API token", err)
	}
	return nil
}

// Authenticate validates a raw bearer token against active tokens in the database.
// It returns errUnauthorized on invalid/revoked tokens, and propagates database errors.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Identity, error) {
	hash := hashToken(rawToken)
	token, err := s.store.FindActiveByHash(ctx, hash)
	if errors.Is(err, errTokenNotFound) {
		return Identity{}, errUnauthorized
	}
	if err != nil {
		return Identity{}, err // 500 error, not 401
	}

	s.touchLastUsed(token.ID)
	return Identity{ID: token.ID, Name: token.Name}, nil
}

// touchLastUsed initiates an asynchronous update of last_used_at, coalesced to
// at most once per 60s per token.
func (s *Service) touchLastUsed(id string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}

	if last, ok := s.lastUsedMap[id]; ok && time.Since(last) < 60*time.Second {
		s.mu.Unlock()
		return
	}
	s.lastUsedMap[id] = time.Now()
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		select {
		case <-s.stopCh:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := s.store.TouchLastUsed(ctx, id, time.Now()); err != nil && !errors.Is(err, errDatabaseClosed) {
			s.logger.Warn("failed to touch token last_used_at", zap.String("tokenId", id), zap.Error(err))
		}
	}()
}

// Close gracefully stops the service and waits for in-flight touchLastUsed writes to drain.
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.closeOnce.Do(func() {
		close(s.stopCh)
	})
	s.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(500 * time.Millisecond):
		s.logger.Warn("timed out waiting for touchLastUsed goroutines to drain")
	}
	return nil
}
