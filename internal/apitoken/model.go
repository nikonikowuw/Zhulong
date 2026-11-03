package apitoken

import (
	"errors"
	"time"
)

// Standard audit actions for the api_token domain.
// Dotted lowercase values match the regex in audit action scanner.
const (
	ActionTokenCreate       = "api_token.create"
	ActionTokenRevoke       = "api_token.revoke"
	ActionTokenAuthRejected = "api_token.auth_rejected"
)

// Rejection reasons for auth_rejected audit entries.
// Detail must only contain these coarse reasons, never token fragments, prefixes or hashes.
const (
	RejectMalformed        = "malformed"
	RejectUnknownOrRevoked = "unknown_or_revoked"
)

// Domain sentinel errors.
var (
	ErrTokenNotFound     = errors.New("api token not found")
	ErrTokenLimitReached = errors.New("api token limit reached")
	ErrTokenNameConflict = errors.New("api token name already exists")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrTokenNameRequired = errors.New("api token name is required")
	ErrTokenNameTooLong  = errors.New("api token name is too long")
)

const (
	MaxActiveTokens = 10
	MaxTokenNameLen = 48
)

// APIToken is the persistence model for M2M API tokens in the api_tokens table.
type APIToken struct {
	ID         string     `gorm:"primaryKey;column:id" json:"id"`
	Name       string     `gorm:"column:name;not null" json:"name"`
	TokenHash  string     `gorm:"column:token_hash;not null" json:"-"`
	MaskedKey  string     `gorm:"column:masked_key;not null" json:"maskedKey"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null" json:"createdAt"`
	LastUsedAt *time.Time `gorm:"column:last_used_at" json:"lastUsedAt"`
	RevokedAt  *time.Time `gorm:"column:revoked_at" json:"-"`
}

// TableName returns the SQLite table name for APIToken.
func (APIToken) TableName() string {
	return "api_tokens"
}
