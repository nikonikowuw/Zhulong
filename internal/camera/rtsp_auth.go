package camera

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
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
	DigestChallenge *digest.Challenge
	Type            AuthType
	RawChallenge    string
	Algorithm       string
	Realm           string
	HadEmptyOpaque  bool
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

	idx := strings.IndexByte(header, ' ')
	if idx <= 0 {
		return header, false, nil
	}
	prefix := header[:idx]
	params := strings.TrimSpace(header[idx+1:])

	switch strings.ToLower(prefix) {
	case "basic":
		return "Basic " + params, false, nil
	case "digest":
		// Continue to digest normalization
	default:
		return header, false, nil
	}

	// Tokenize parameters by comma, respecting quoted strings
	parts := []string{}
	var current strings.Builder
	var inQuote, escaped bool

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

	normParts := []string{}
	for _, part := range parts {
		before, after, ok := strings.Cut(part, "=")
		if !ok {
			normParts = append(normParts, part)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(before))
		val := strings.TrimSpace(after)

		// Check for empty opaque
		if key == "opaque" && (val == `""` || val == "") {
			hadEmptyOpaque = true
		}

		// Normalize QOP values: trim internal comma-separated whitespace
		if key == "qop" && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
			inner := strings.Trim(val, `"`)
			tokens := strings.Split(inner, ",")
			cleanTokens := []string{}
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

		lowerNorm := strings.ToLower(normHeader)
		switch {
		case strings.HasPrefix(lowerNorm, "basic "):
			if fallbackBasic == nil {
				fallbackBasic = &ParsedChallenge{
					Type:         AuthTypeBasic,
					RawChallenge: chHeader,
				}
			}
			continue
		case strings.HasPrefix(lowerNorm, "digest "):
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
			switch alg {
			case "MD5", "SHA-256", "SHA256":
			default:
				continue
			}

			// Validate QOP if present
			if len(chal.QOP) > 0 {
				hasSupportedQOP := slices.ContainsFunc(chal.QOP, func(q string) bool {
					qClean := strings.ToLower(strings.TrimSpace(q))
					return qClean == "auth" || qClean == "auth-int"
				})
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

// AuthorizationParams encapsulates parameters required to construct an Authorization header.
type AuthorizationParams struct {
	Challenge *ParsedChallenge
	Method    string
	URI       string
	Username  string
	Password  string
	Cnonce    string
	NC        int
}

// BuildAuthorizationHeader generates the Authorization header value for a given challenge, credentials, and request context.
func BuildAuthorizationHeader(params AuthorizationParams) (string, error) {
	if params.Challenge == nil {
		return "", errors.New("nil challenge")
	}

	switch params.Challenge.Type {
	case AuthTypeBasic:
		payload := params.Username + ":" + params.Password
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(payload)), nil

	case AuthTypeDigest:
		if params.Challenge.DigestChallenge == nil {
			return "", errors.New("missing digest challenge details")
		}

		cnonce := params.Cnonce
		if cnonce == "" {
			var err error
			cnonce, err = GenerateCnonce()
			if err != nil {
				return "", err
			}
		}

		opts := digest.Options{
			Method:   params.Method,
			URI:      params.URI,
			Username: params.Username,
			Password: params.Password,
			Count:    params.NC,
			Cnonce:   cnonce,
		}

		cred, err := digest.Digest(params.Challenge.DigestChallenge, opts)
		if err != nil {
			return "", fmt.Errorf("calculate digest credentials: %w", err)
		}

		headerVal := cred.String()

		// If server provided empty opaque="" and cred.String() omitted it because cred.Opaque was empty
		if params.Challenge.HadEmptyOpaque && !strings.Contains(headerVal, "opaque=") {
			headerVal += `, opaque=""`
		}

		return headerVal, nil

	default:
		return "", fmt.Errorf("unsupported auth type %q", params.Challenge.Type)
	}
}
