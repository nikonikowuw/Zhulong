// Package logsafe provides typed helpers for values that reach application
// logs, plus a defensive zap core (see core.go) that applies them as a
// best-effort structural guard.
//
// Documented limits — read these before relying on any helper:
//
//   - Safe normalizes UTF-8, replaces control characters and truncates at a
//     valid UTF-8 boundary (MaxSafeBytes). It is NOT a secret detector: a
//     password, token or connection string embedded in free-form prose, base64
//     or a concatenated string passes through unchanged.
//   - URL never returns userinfo, host, path, query or fragment. It reports the
//     lower-cased scheme plus a fixed marker, or the fixed marker alone when the
//     input is empty, oversized, relative or unparseable.
//   - Path/Paths never return the input. They exist so call sites can record
//     "a path was involved" without exposing device identifiers or account
//     names, which cannot be classified as safe in general.
//   - Secret always returns the fixed placeholder.
//   - IP only ever returns a masked network summary; invalid input yields the
//     fixed placeholder.
//   - ErrorSummary never calls err.Error(). Only presence is reported.
//
// Because these helpers do not detect secrets in arbitrary text, call sites must
// not log raw exception messages, user prose, URL queries, filesystem paths or
// application-defined marshaler output. Typed helpers and field allowlists are
// the primary controls; the SanitizingCore is defense in depth only.
//
// Dependency whitelist: the standard library only — net/url, net/netip,
// strings, unicode/utf8 and unicode (core.go additionally uses
// go.uber.org/zap/zapcore). This package must never import internal/camera or
// any other business package.
package logsafe

import (
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxSafeBytes is the byte upper bound applied by Safe and by URL.
	MaxSafeBytes = 2048

	// SafeTruncatedMarker is appended when Safe truncates a value. The marker
	// bytes count against MaxSafeBytes so the result never exceeds the bound.
	SafeTruncatedMarker = "...[TRUNCATED]"

	// URLMarker replaces every part of a URL except a validated scheme.
	URLMarker = "[REDACTED_URL]"
	// ValueMarker replaces secrets and values of known sensitive keys.
	ValueMarker = "[REDACTED]"
	// PathMarker replaces filesystem or device paths.
	PathMarker = "[REDACTED_PATH]"
	// IPMarker replaces an address that cannot be parsed or summarized.
	IPMarker = "[REDACTED_IP]"
	// ErrorMarker replaces an error value.
	ErrorMarker = "[REDACTED_ERROR]"
	// NoErrorMarker reports that an error field carried no error value.
	NoErrorMarker = "[NO_ERROR]"
	// OmittedMarker replaces zap field types whose encoders may invoke
	// application code or dump opaque payloads (ObjectMarshaler,
	// ArrayMarshaler, Reflect, Stringer, Binary, Namespace).
	OmittedMarker = "[OMITTED]"

	// controlReplacement replaces control characters so a value cannot inject
	// log lines or terminal escapes.
	controlReplacement = ' '

	ipv4PrefixBits = 24
	ipv6PrefixBits = 64
)

// Safe normalizes value for logging: invalid UTF-8 is replaced with U+FFFD,
// control characters (including CR, LF and Unicode line/paragraph separators)
// are replaced with a space, and the result is truncated at a valid UTF-8
// boundary so it never exceeds MaxSafeBytes.
//
// A value that is already clean and within the bound is returned unchanged. A
// value whose sanitized form fits is returned without a truncation marker; only
// a value that genuinely overflows the bound is cut and marked.
//
// Safe bounds text shape only. It does not recognize credentials, tokens or
// connection strings hidden inside arbitrary text.
func Safe(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= MaxSafeBytes && utf8.ValidString(value) && !hasUnsafeRune(value) {
		return value
	}

	// Copy the clean ASCII prefix verbatim. Those bytes cannot need rewriting,
	// so a long clean run costs one scan instead of one rune append per byte.
	i := 0
	for i < len(value) && i < MaxSafeBytes {
		if c := value[i]; c < 0x20 || c >= utf8.RuneSelf || c == 0x7F {
			break
		}
		i++
	}
	buf := make([]byte, i, MaxSafeBytes+len(SafeTruncatedMarker))
	copy(buf, value[:i])

	for i < len(value) {
		var r rune
		var size int
		if c := value[i]; c < utf8.RuneSelf {
			r, size = rune(c), 1
		} else {
			r, size = utf8.DecodeRuneInString(value[i:])
		}
		if r == utf8.RuneError && size == 1 {
			// Invalid byte: keep the standard replacement rune.
		} else if isUnsafeRune(r) {
			r = controlReplacement
		}
		width := utf8.RuneLen(r)
		if width < 0 {
			r = utf8.RuneError
			width = utf8.RuneLen(r)
		}
		if len(buf)+width > MaxSafeBytes {
			break
		}
		buf = utf8.AppendRune(buf, r)
		i += size
	}
	if i == len(value) {
		return string(buf)
	}

	// The remaining input does not fit: cut back to a rune boundary that leaves
	// room for the marker, then mark the truncation.
	cut := MaxSafeBytes - len(SafeTruncatedMarker)
	if cut > len(buf) {
		cut = len(buf)
	}
	for cut > 0 && cut < len(buf) && !utf8.RuneStart(buf[cut]) {
		cut--
	}
	return string(buf[:cut]) + SafeTruncatedMarker
}

// URL reports that raw carried a URL without exposing any of it. A parseable
// absolute URL yields "<lower-cased scheme>://[REDACTED_URL]"; empty, relative,
// oversized or malformed input yields "[REDACTED_URL]".
//
// Userinfo, host, port, path, query and fragment are never returned.
func URL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > MaxSafeBytes {
		return URLMarker
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" {
		return URLMarker
	}
	return strings.ToLower(parsed.Scheme) + "://" + URLMarker
}

// Path reports that a filesystem or device path was involved without returning
// any part of it, because path segments routinely carry account names, device
// identifiers and mount points that cannot be classified as safe.
func Path(string) string { return PathMarker }

// Paths reports that a set of paths was involved without returning any part of
// it. One fixed marker is returned per input element so callers keep the
// cardinality; the values are never inspected.
func Paths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i := range out {
		out[i] = PathMarker
	}
	return out
}

// Secret always returns the fixed placeholder, regardless of input.
func Secret(string) string { return ValueMarker }

// IP returns a masked network summary of raw: the IPv4 /24 or IPv6 /64 prefix.
// Bracketed IPv6 literals and zoned addresses are accepted. Empty, oversized or
// unparseable input yields IPMarker.
func IP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > MaxSafeBytes {
		return IPMarker
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	// Idempotency matters: the handler masks an address to a /24 or /64 network
	// summary, and SanitizingCore then applies this helper again to the same
	// "clientIp" field. Without this branch the second pass cannot parse a CIDR
	// and collapses an already-safe value to IPMarker, destroying the network
	// information the first pass deliberately kept.
	if prefix, err := netip.ParsePrefix(trimmed); err == nil {
		return prefix.Masked().String()
	}

	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return IPMarker
	}
	addr = addr.Unmap()
	if addr.Zone() != "" {
		addr = addr.WithZone("")
	}
	bits := ipv6PrefixBits
	if addr.Is4() {
		bits = ipv4PrefixBits
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return IPMarker
	}
	return prefix.String()
}

// ErrorSummary reports only whether err was present. It never calls err.Error()
// and never inspects wrapped messages, so error text cannot reach a log.
func ErrorSummary(err error) string {
	if err == nil {
		return NoErrorMarker
	}
	return ErrorMarker
}

// hasUnsafeRune reports whether value contains a control character or invalid
// UTF-8 byte that Safe must replace.
func hasUnsafeRune(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < utf8.RuneSelf {
			if value[i] < 0x20 || value[i] == 0x7F {
				return true
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if isUnsafeRune(r) {
			return true
		}
		i += size - 1
	}
	return false
}

// isUnsafeRune reports whether r must be replaced to keep a value on one log
// line: C0/C1 controls, DEL, and the Unicode line/paragraph separators.
func isUnsafeRune(r rune) bool {
	if r < utf8.RuneSelf {
		return r < 0x20 || r == 0x7F
	}
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}
