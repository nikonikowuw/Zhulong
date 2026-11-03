package apitoken

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

var ErrDatabaseClosed = errors.New("database connection is not available")

// Store manages persistence of API tokens in SQLite.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, ErrDatabaseClosed
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	return db.WithContext(ctx), nil
}

// CreateToken inserts a new token inside a transaction that enforces the MaxActiveTokens limit.
// Notice: Within db.Transaction, tx MUST be used exclusively to prevent deadlock under MaxOpenConns=1.
func (s *Store) CreateToken(ctx context.Context, token *APIToken) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var activeCount int64
		if err := tx.Model(&APIToken{}).Where("revoked_at IS NULL").Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount >= MaxActiveTokens {
			return ErrTokenLimitReached
		}

		if err := tx.Create(token).Error; err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "api_tokens.name") || strings.Contains(errStr, "ux_api_tokens_name") {
				return ErrTokenNameConflict
			}
			return err
		}
		return nil
	})
}

// ListTokens returns active tokens (or all tokens including revoked if includeRevoked is true).
func (s *Store) ListTokens(ctx context.Context, includeRevoked bool) ([]APIToken, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	query := db.Model(&APIToken{}).Order("created_at DESC")
	if !includeRevoked {
		query = query.Where("revoked_at IS NULL")
	}

	var items []APIToken
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// FindActiveByHash looks up a non-revoked token by its SHA-256 hash.
func (s *Store) FindActiveByHash(ctx context.Context, hash string) (*APIToken, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var token APIToken
	if err := db.Where("token_hash = ? AND revoked_at IS NULL", hash).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	return &token, nil
}

// Revoke soft-deletes a token by setting revoked_at. It is idempotent if the token is already revoked.
func (s *Store) Revoke(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	var token APIToken
	if err := db.Where("id = ?", id).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTokenNotFound
		}
		return err
	}
	if token.RevokedAt != nil {
		return nil
	}

	now := time.Now().UTC()
	return db.Model(&APIToken{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
}

// TouchLastUsed updates the last_used_at timestamp of a token.
func (s *Store) TouchLastUsed(ctx context.Context, id string, t time.Time) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	return db.Model(&APIToken{}).Where("id = ?", id).Update("last_used_at", t.UTC()).Error
}
