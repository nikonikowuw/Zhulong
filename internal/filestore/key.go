package filestore

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

const (
	// MaxKeyLength bounds logical keys, matching mediafile and DB path limits.
	MaxKeyLength = 512
)

var (
	// keySegmentPattern allows ASCII letters, digits, underscore, hyphen, and dot,
	// up to 128 characters per path segment.
	keySegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

	mediaNamespaces = map[string]bool{
		NamespaceRecordings: true,
		NamespaceSnapshots:  true,
		NamespaceExports:    true,
	}

	knownNamespaces = map[string]bool{
		NamespaceRecordings: true,
		NamespaceSnapshots:  true,
		NamespaceExports:    true,
		NamespaceFaces:      true,
		NamespaceMaps:       true,
	}
)

// IsMediaNamespace reports whether ns is exposed through the /media/* HTTP surface.
func IsMediaNamespace(ns string) bool {
	return mediaNamespaces[ns]
}

// IsKnownNamespace reports whether ns is a managed filestore namespace.
func IsKnownNamespace(ns string) bool {
	return knownNamespaces[ns]
}

// ValidateKey validates a logical object key against all managed namespaces
// (recordings, snapshots, exports, faces, maps).
func ValidateKey(key string) (string, error) {
	return ValidateKeyInNamespaces(key, knownNamespaces)
}

// ValidateMediaKey validates a logical object key restricted to media namespaces
// (recordings, snapshots, exports).
func ValidateMediaKey(key string) (string, error) {
	return ValidateKeyInNamespaces(key, mediaNamespaces)
}

// ValidateKeyInNamespaces enforces strict shape and namespace-whitelist validation
// without relying on filepath.Clean (which would normalize away traversal shapes).
func ValidateKeyInNamespaces(key string, allowed map[string]bool) (string, error) {
	if key == "" {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path must not be empty", ErrInvalidKey)
	}
	if strings.ContainsRune(key, '\x00') {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path must not contain NUL bytes", ErrInvalidKey)
	}
	if len(key) > MaxKeyLength {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path is too long", ErrInvalidKey)
	}
	if filepath.IsAbs(key) || strings.HasPrefix(key, "/") || strings.HasPrefix(key, "\\") {
		return "", apperr.Invalid(
			"MEDIA_PATH_INVALID",
			"Media path must be relative to the media root",
			ErrInvalidKey,
		)
	}

	segments := strings.Split(key, "/")
	for _, segment := range segments {
		if segment == "." || segment == ".." {
			return "", apperr.Invalid(
				"MEDIA_PATH_INVALID",
				"Media path must not contain relative segments",
				ErrInvalidKey,
			)
		}
		if !keySegmentPattern.MatchString(segment) {
			return "", apperr.Invalid(
				"MEDIA_PATH_INVALID",
				"Media path contains an invalid segment",
				ErrInvalidKey,
			)
		}
	}

	if !allowed[segments[0]] {
		return "", apperr.Invalid(
			"MEDIA_KIND_INVALID",
			"Media path must start with a known media directory",
			ErrInvalidNamespace,
		)
	}

	return key, nil
}

// ValidateObjectKeyForWrite validates a logical key for object writes (Put),
// requiring both a known namespace and at least one object segment beneath it.
func ValidateObjectKeyForWrite(key string) (string, error) {
	clean, err := ValidateKey(key)
	if err != nil {
		return "", err
	}
	if _, rest := SplitKey(clean); rest == "" {
		return "", apperr.Invalid(
			"MEDIA_PATH_INVALID",
			"Media path must include an object name",
			ErrInvalidKey,
		)
	}
	return clean, nil
}

// SplitKey splits a validated logical key into its leading namespace and remainder.
func SplitKey(key string) (string, string) {
	ns, rest, ok := strings.Cut(key, "/")
	if !ok {
		return key, ""
	}
	return ns, rest
}

// ContentTypeForKey maps well-known asset extensions to deterministic MIME types.
func ContentTypeForKey(key string) string {
	switch strings.ToLower(path.Ext(key)) {
	case ".mp4":
		return "video/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
