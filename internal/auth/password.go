package auth

import (
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Password policy. The rune bounds mirror the binding tags on InitAdminRequest
// (struct tags cannot reference constants, so requests.go repeats the numbers);
// the byte ceiling is bcrypt's own input limit rather than a product rule.
const (
	minPasswordRunes = 8
	maxPasswordRunes = 64
	maxPasswordBytes = 72
)

// validateNewPassword enforces the credential policy for a new administrator
// password. Counting runes keeps the contract identical to the request binding
// tags, while the byte ceiling is what bcrypt can actually hash: a 64-rune CJK
// password is 192 bytes and must be rejected here with a 422 instead of failing
// at hash time and surfacing as an internal error.
func validateNewPassword(password string) error {
	runes := utf8.RuneCountInString(password)
	if runes < minPasswordRunes {
		return ErrPasswordTooShort
	}
	if runes > maxPasswordRunes || len(password) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// hashPassword returns the bcrypt hash persisted for a newly created account.
func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// timingEqualizerHash lazily hashes an arbitrary value at DefaultCost. The login
// path compares against it when the account does not exist, so an unknown
// username costs the same wall-clock time as a wrong password and the response
// latency does not reveal whether the username matches.
var timingEqualizerHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("zhulong-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		// A nil hash makes the fallback comparison fail immediately, which degrades
		// to plain timing behaviour but never breaks the login path.
		return nil
	}
	return hash
})

// verifyPassword reports whether password matches the stored hash. An empty hash
// still runs a full bcrypt comparison against timingEqualizerHash and reports
// failure, so callers keep one code path for known and unknown accounts.
func verifyPassword(hash, password string) bool {
	if hash == "" {
		hash = string(timingEqualizerHash())
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
