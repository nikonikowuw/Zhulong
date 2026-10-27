package gb28181

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Nonce defaults. The nonce is deliberately *not* single-use: UDP REGISTER
// retransmissions replay the same Authorization byte-for-byte, and consuming the
// nonce would answer a retransmission with 401 until the device gives up. A replayed
// REGISTER only re-establishes the same device's own binding, so a time-bounded,
// HMAC-signed nonce is the correct trade-off here.
const (
	defaultNonceTTL = 5 * time.Minute
	nonceSeparator  = "."

	// nonceTimeHexLen is len(hex(UnixNano)): the fixed-width prefix that carries the
	// issue time. It is parsed back by Verify, so the width is a wire contract.
	nonceTimeHexLen = 16
)

var (
	ErrDigestMissing       = errors.New("gb28181 digest: no Authorization header")
	ErrDigestMalformed     = errors.New("gb28181 digest: malformed Authorization header")
	ErrDigestUnsupported   = errors.New("gb28181 digest: unsupported algorithm")
	ErrDigestUserMismatch  = errors.New("gb28181 digest: username does not match the device identity")
	ErrDigestRealmMismatch = errors.New("gb28181 digest: realm mismatch")
	ErrDigestNonceInvalid  = errors.New("gb28181 digest: nonce is not authentic")
	ErrDigestNonceStale    = errors.New("gb28181 digest: nonce has expired")
	ErrDigestMismatch      = errors.New("gb28181 digest: response digest does not match")
)

// nonceManager issues and verifies the nonce carried in WWW-Authenticate.
//
// The nonce is `hex(issueUnixNano).hex(counter).hex(hmacSHA256(prefix))`, so it
// survives concurrent challenges without shared state, cannot be forged without the
// process-local secret, and carries its own expiry.
type nonceManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time

	mu      sync.Mutex
	counter uint64
}

// newNonceManager builds a nonce manager. A zero or negative TTL falls back to the
// documented default so a mis-wired caller cannot disable expiry by accident.
func newNonceManager(secret []byte, ttl time.Duration) *nonceManager {
	if ttl <= 0 {
		ttl = defaultNonceTTL
	}
	return &nonceManager{secret: append([]byte(nil), secret...), ttl: ttl, now: time.Now}
}

// Issue returns a fresh nonce.
func (m *nonceManager) Issue() string {
	m.mu.Lock()
	m.counter++
	counter := m.counter
	m.mu.Unlock()

	prefix := fmt.Sprintf("%x%x", m.now().UnixNano(), counter)
	return prefix + nonceSeparator + m.sign(prefix)
}

// Verify checks authenticity and lifetime. It reports stale separately from invalid
// so the 401 can carry `stale=true`, which is what lets a device retry immediately
// instead of dropping the registration.
func (m *nonceManager) Verify(nonce string) error {
	prefix, signature, found := strings.Cut(nonce, nonceSeparator)
	if !found || prefix == "" || signature == "" {
		return ErrDigestNonceInvalid
	}
	expected := m.sign(prefix)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return ErrDigestNonceInvalid
	}
	// Authentic, but the slices below index a fixed-width prefix: reject a short one
	// here rather than relying on the HMAC comparison above to keep it unreachable.
	if len(prefix) < nonceTimeHexLen {
		return ErrDigestNonceInvalid
	}
	issuedAt, err := strconv.ParseInt(prefix[:nonceTimeHexLen], 16, 64)
	if err != nil {
		return ErrDigestNonceInvalid
	}
	if m.now().Sub(time.Unix(0, issuedAt)) > m.ttl {
		return ErrDigestNonceStale
	}
	return nil
}

func (m *nonceManager) sign(prefix string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(prefix))
	return hex.EncodeToString(mac.Sum(nil))
}

// digestCredentials is a parsed Authorization header. Empty QOP means the RFC 2069
// form, which some older devices still send.
type digestCredentials struct {
	Username  string
	Realm     string
	Nonce     string
	URI       string
	Response  string
	Algorithm string
	QOP       string
	Nc        string
	CNonce    string
}

// parseDigestCredentials splits a `Digest ...` Authorization value. Parameter names
// are matched case-insensitively; quoted values may contain commas.
func parseDigestCredentials(header string) (digestCredentials, error) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return digestCredentials{}, ErrDigestMissing
	}
	const scheme = "digest"
	if len(trimmed) < len(scheme) || !strings.EqualFold(trimmed[:len(scheme)], scheme) {
		return digestCredentials{}, ErrDigestMalformed
	}
	params, err := parseDigestParams(strings.TrimSpace(trimmed[len(scheme):]))
	if err != nil {
		return digestCredentials{}, err
	}
	if missing := missingParam(params, "username", "nonce", "uri", "response"); missing != "" {
		return digestCredentials{}, fmt.Errorf("%w: missing %s", ErrDigestMalformed, missing)
	}

	return digestCredentials{
		Username:  params["username"],
		Realm:     params["realm"],
		Nonce:     params["nonce"],
		URI:       params["uri"],
		Response:  params["response"],
		Algorithm: strings.ToUpper(strings.TrimSpace(params["algorithm"])),
		QOP:       strings.ToLower(strings.TrimSpace(params["qop"])),
		Nc:        params["nc"],
		CNonce:    params["cnonce"],
	}, nil
}

// parseDigestParams splits the parameter list of a `Digest ...` value. Names are
// lowercased for case-insensitive matching; quoted values may contain commas, which
// is why a quoted value is scanned to its closing quote instead of the next comma.
func parseDigestParams(rest string) (map[string]string, error) {
	params := make(map[string]string, 8)
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " \t,")
		if rest == "" {
			break
		}
		keyEnd := strings.IndexByte(rest, '=')
		if keyEnd <= 0 {
			return nil, ErrDigestMalformed
		}
		key := strings.ToLower(strings.TrimSpace(rest[:keyEnd]))
		rest = strings.TrimSpace(rest[keyEnd+1:])

		value, remainder, err := nextParamValue(rest)
		if err != nil {
			return nil, err
		}
		params[key] = value
		rest = remainder
	}
	return params, nil
}

// nextParamValue consumes one parameter value and returns what follows it.
func nextParamValue(rest string) (string, string, error) {
	if !strings.HasPrefix(rest, "\"") {
		end := strings.IndexByte(rest, ',')
		if end < 0 {
			return strings.TrimSpace(rest), "", nil
		}
		return strings.TrimSpace(rest[:end]), rest[end:], nil
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return "", "", ErrDigestMalformed
	}
	return rest[1 : 1+end], rest[end+2:], nil
}

// missingParam returns the first required parameter that carries no value, so the
// error can name it instead of hiding the failure behind a boolean wall.
func missingParam(params map[string]string, names ...string) string {
	for _, name := range names {
		if params[name] == "" {
			return name
		}
	}
	return ""
}

// verifyDigestResponse recomputes the digest for the request and compares it in
// constant time.
//
// Supported algorithms are MD5 (what GB28181 devices use) and SHA-256 from RFC 7616.
// MD5-sess is rejected rather than half-implemented: accepting a name we compute
// differently from the device would lock the device out with a misleading 401.
func verifyDigestResponse(credentials digestCredentials, method, realm, password string) error {
	if credentials.Username == "" || credentials.Realm != realm {
		return ErrDigestRealmMismatch
	}
	algorithm := credentials.Algorithm
	if algorithm == "" {
		algorithm = "MD5"
	}

	var hash func([]byte) string
	switch algorithm {
	case "MD5":
		hash = func(data []byte) string {
			sum := md5.Sum(data)
			return hex.EncodeToString(sum[:])
		}
	case "SHA-256":
		hash = func(data []byte) string {
			sum := sha256.Sum256(data)
			return hex.EncodeToString(sum[:])
		}
	default:
		return fmt.Errorf("%w: %q", ErrDigestUnsupported, credentials.Algorithm)
	}

	ha1 := hash([]byte(credentials.Username + ":" + credentials.Realm + ":" + password))
	ha2 := hash([]byte(method + ":" + credentials.URI))

	expected, err := expectedDigestResponse(credentials, ha1, ha2, hash)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(strings.ToLower(credentials.Response))) != 1 {
		return ErrDigestMismatch
	}
	return nil
}

// expectedDigestResponse recomputes what the device should have sent, handling the
// two response forms: RFC 2069 (empty QOP, still used by older devices) and
// RFC 2617 qop=auth.
func expectedDigestResponse(
	credentials digestCredentials,
	ha1, ha2 string,
	hash func([]byte) string,
) (string, error) {
	if credentials.QOP == "" {
		return hash([]byte(ha1 + ":" + credentials.Nonce + ":" + ha2)), nil
	}
	if credentials.QOP != "auth" {
		return "", fmt.Errorf("%w: qop=%q", ErrDigestUnsupported, credentials.QOP)
	}
	if credentials.Nc == "" || credentials.CNonce == "" {
		return "", ErrDigestMalformed
	}
	return hash([]byte(ha1 + ":" + credentials.Nonce + ":" + credentials.Nc +
		":" + credentials.CNonce + ":auth:" + ha2)), nil
}
