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
	rejectMalformed        = "malformed"
	rejectUnknownOrRevoked = "unknown_or_revoked"
)

// Domain limits and lengths.
const (
	maxActiveTokens = 10
	maxTokenNameLen = 48
)

// Domain sentinel errors.
var (
	errTokenNotFound     = errors.New("api token not found")
	errTokenLimitReached = errors.New("api token limit reached")
	errTokenNameConflict = errors.New("api token name already exists")
	errUnauthorized      = errors.New("unauthorized")
	errTokenNameRequired = errors.New("api token name is required")
	errTokenNameTooLong  = errors.New("api token name is too long")
	errTokenNameInvalid  = errors.New("api token name contains invalid characters")
)

// APIToken is the persistence model for M2M API tokens in the api_tokens table.
// Outward serialization is handled exclusively by APITokenInfo.
type APIToken struct {
	ID         string     `gorm:"primaryKey;column:id"`
	Name       string     `gorm:"column:name;not null"`
	TokenHash  string     `gorm:"column:token_hash;not null"`
	MaskedKey  string     `gorm:"column:masked_key;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
}

// TableName returns the SQLite table name for APIToken.
func (APIToken) TableName() string {
	return "api_tokens"
}
