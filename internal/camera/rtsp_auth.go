package camera

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/icholy/digest"
)

var (
	ErrNoSupportedAuthChallenge = errors.New("no supported authentication challenge found")
	ErrUnsupportedAuthAlgorithm = errors.New("unsupported digest authentication algorithm")
	ErrUnsupportedQOP           = errors.New("unsupported qop variant")
	ErrMalformedAuthChallenge   = errors.New("malformed authentication challenge")
)

type AuthType string

const (
	AuthTypeNone   AuthType = "none"
	AuthTypeBasic  AuthType = "basic"
	AuthTypeDigest AuthType = "digest"
)

// ParsedChallenge represents an analyzed and normalized authentication challenge.
type ParsedChallenge struct {
	Type            AuthType
	RawChallenge    string
	DigestChallenge *digest.Challenge
	Algorithm       string
	HadEmptyOpaque  bool
	Realm           string
}

// GenerateCnonce generates a cryptographically secure 16-hex-character cnonce string.
func GenerateCnonce() (string, error) {
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("read random bytes for cnonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NormalizeChallengeHeader normalizes parameter keys to lowercase and preserves quoted strings,
// handling quirks like QOP/ALGORITHM capitalization, whitespace in lists, and tracking empty opaque="".
func NormalizeChallengeHeader(header string) (normalized string, hadEmptyOpaque bool, err error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false, ErrMalformedAuthChallenge
	}

	var prefix string
	var params string
	if idx := strings.IndexByte(header, ' '); idx > 0 {
		prefix = header[:idx]
		params = strings.TrimSpace(header[idx+1:])
	} else {
		return header, false, nil
	}

	lowerPrefix := strings.ToLower(prefix)
	if lowerPrefix == "basic" {
		return "Basic " + params, false, nil
	}
	if lowerPrefix != "digest" {
		return header, false, nil
	}

	// Tokenize parameters by comma, respecting quoted strings
	var parts []string
	var current strings.Builder
	inQuote := false
	escaped := false

	for i := 0; i < len(params); i++ {
		ch := params[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			current.WriteByte(ch)
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			current.WriteByte(ch)
			continue
		}
		if ch == ',' && !inQuote {
			part := strings.TrimSpace(current.String())
			if part != "" {
				parts = append(parts, part)
			}
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		part := strings.TrimSpace(current.String())
		if part != "" {
			parts = append(parts, part)
		}
	}

	var normParts []string
	for _, part := range parts {
		eqIdx := strings.IndexByte(part, '=')
		if eqIdx < 0 {
			normParts = append(normParts, part)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(part[:eqIdx]))
		val := strings.TrimSpace(part[eqIdx+1:])

		// Check for empty opaque
		if key == "opaque" && (val == `""` || val == "") {
			hadEmptyOpaque = true
		}

		// Normalize QOP values: trim internal comma-separated whitespace
		if key == "qop" && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
			inner := strings.Trim(val, `"`)
			tokens := strings.Split(inner, ",")
			var cleanTokens []string
			for _, tok := range tokens {
				t := strings.TrimSpace(tok)
				if t != "" {
					cleanTokens = append(cleanTokens, t)
				}
			}
			val = `"` + strings.Join(cleanTokens, ",") + `"`
		}

		normParts = append(normParts, key+"="+val)
	}

	return "Digest " + strings.Join(normParts, ", "), hadEmptyOpaque, nil
}

// SelectBestChallenge parses WWW-Authenticate header values and selects the strongest supported challenge.
// Priority: Digest SHA-256 > Digest MD5 > Basic.
func SelectBestChallenge(challenges []string) (*ParsedChallenge, error) {
	if len(challenges) == 0 {
		return nil, ErrNoSupportedAuthChallenge
	}

	var bestDigest *ParsedChallenge
	var fallbackBasic *ParsedChallenge

	for _, chHeader := range challenges {
		chHeader = strings.TrimSpace(chHeader)
		if chHeader == "" {
			continue
		}

		normHeader, hadEmptyOpaque, err := NormalizeChallengeHeader(chHeader)
		if err != nil {
			continue
		}

		if strings.HasPrefix(strings.ToLower(normHeader), "basic ") {
			if fallbackBasic == nil {
				fallbackBasic = &ParsedChallenge{
					Type:         AuthTypeBasic,
					RawChallenge: chHeader,
				}
			}
			continue
		}

		if strings.HasPrefix(strings.ToLower(normHeader), "digest ") {
			chal, err := digest.ParseChallenge(normHeader)
			if err != nil {
				continue
			}

			alg := strings.ToUpper(strings.TrimSpace(chal.Algorithm))
			if alg == "" {
				alg = "MD5"
			}

			// Exclude unsupported algorithms like session variants
			if strings.HasSuffix(alg, "-SESS") {
				continue
			}
			if alg != "MD5" && alg != "SHA-256" && alg != "SHA256" {
				continue
			}

			// Validate QOP if present
			if len(chal.QOP) > 0 {
				hasSupportedQOP := false
				for _, q := range chal.QOP {
					qClean := strings.ToLower(strings.TrimSpace(q))
					if qClean == "auth" || qClean == "auth-int" {
						hasSupportedQOP = true
						break
					}
				}
				if !hasSupportedQOP {
					continue
				}
			}

			candidate := &ParsedChallenge{
				Type:            AuthTypeDigest,
				RawChallenge:    chHeader,
				DigestChallenge: chal,
				Algorithm:       alg,
				HadEmptyOpaque:  hadEmptyOpaque,
				Realm:           chal.Realm,
			}

			if alg == "SHA-256" || alg == "SHA256" {
				bestDigest = candidate
				break // Strongest found
			}

			if bestDigest == nil {
				bestDigest = candidate
			}
		}
	}

	if bestDigest != nil {
		return bestDigest, nil
	}
	if fallbackBasic != nil {
		return fallbackBasic, nil
	}

	return nil, ErrNoSupportedAuthChallenge
}

// BuildAuthorizationHeader generates the Authorization header value for a given challenge, credentials, and request context.
func BuildAuthorizationHeader(
	challenge *ParsedChallenge,
	method, uri string,
	username, password string,
	nc int,
	cnonce string,
) (string, error) {
	if challenge == nil {
		return "", errors.New("nil challenge")
	}

	switch challenge.Type {
	case AuthTypeBasic:
		payload := username + ":" + password
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(payload)), nil

	case AuthTypeDigest:
		if challenge.DigestChallenge == nil {
			return "", errors.New("missing digest challenge details")
		}

		if cnonce == "" {
			var err error
			cnonce, err = GenerateCnonce()
			if err != nil {
				return "", err
			}
		}

		opts := digest.Options{
			Method:   method,
			URI:      uri,
			Username: username,
			Password: password,
			Count:    nc,
			Cnonce:   cnonce,
		}

		cred, err := digest.Digest(challenge.DigestChallenge, opts)
		if err != nil {
			return "", fmt.Errorf("calculate digest credentials: %w", err)
		}

		headerVal := cred.String()

		// If server provided empty opaque="" and cred.String() omitted it because cred.Opaque was empty
		if challenge.HadEmptyOpaque && !strings.Contains(headerVal, "opaque=") {
			headerVal += `, opaque=""`
		}

		return headerVal, nil

	default:
		return "", fmt.Errorf("unsupported auth type %v", challenge.Type)
	}
}
