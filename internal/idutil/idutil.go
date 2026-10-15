package idutil

import (
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	defaultEntropyMu sync.Mutex
	defaultEntropy   io.Reader = ulid.Monotonic(rand.Reader, 0)
)

// New generates a new 26-character Crockford Base32 monotonic ULID using cryptographically
// secure entropy and UTC millisecond timestamp. It is thread-safe.
func New() string {
	defaultEntropyMu.Lock()
	defer defaultEntropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now().UTC()), defaultEntropy).String()
}

// Parse parses a 26-character string into a ulid.ULID strictly validating base32 characters.
func Parse(s string) (ulid.ULID, error) {
	return ulid.ParseStrict(s)
}

// IsValid checks if s is a valid Crockford Base32 26-character ULID.
func IsValid(s string) bool {
	if len(s) != 26 {
		return false
	}
	_, err := ulid.ParseStrict(s)
	return err == nil
}

// Time extracts the embedded UTC timestamp from a ULID string.
func Time(s string) (time.Time, error) {
	u, err := ulid.ParseStrict(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ulid: %w", err)
	}
	return time.UnixMilli(int64(u.Time())).UTC(), nil
}
