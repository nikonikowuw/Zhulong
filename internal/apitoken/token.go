package apitoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// GenerateToken produces a high-entropy bearer token, its SHA-256 hash for database
// lookup, and a masked key representation safe for display in administrative UI.
func GenerateToken() (raw, hash, masked string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("read token entropy: %w", err)
	}
	raw = "sk-" + hex.EncodeToString(buf)
	hash = HashToken(raw)
	masked = MaskToken(raw)
	return raw, hash, masked, nil
}

// HashToken computes the SHA-256 hex string of a raw token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// MaskToken produces the masked preview "sk-xxxx...yyyy" of a raw token.
func MaskToken(raw string) string {
	if len(raw) <= 11 {
		return raw
	}
	return raw[:7] + "..." + raw[len(raw)-4:]
}

// normalizeTokenName validates and cleans a human-readable API token name.
// Name must be between 1 and 48 characters and must not contain control characters.
func normalizeTokenName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ErrTokenNameRequired
	}
	if len([]rune(trimmed)) > MaxTokenNameLen {
		return "", ErrTokenNameTooLong
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", ErrTokenNameRequired
		}
	}
	return trimmed, nil
}
