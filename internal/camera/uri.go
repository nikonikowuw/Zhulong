package camera

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrInvalidScheme        = errors.New("only 'rtsp' scheme is supported")
	ErrEmptyHost            = errors.New("host cannot be empty")
	ErrInvalidPort          = errors.New("invalid port number")
	ErrAmbiguousCredentials = errors.New("ambiguous userinfo in RTSP URL")
	ErrUnsupportedTransport = errors.New("transport must be 'tcp' or 'udp'")
)

// StreamConnectionInfo represents validated and normalized stream connection parameters.
type StreamConnectionInfo struct {
	NormalizedURI string // full normalized URI including credentials (if present)
	CleanURI      string // absolute URI without userinfo (for RTSP request line & Digest auth)
	Username      string
	Password      string
	Transport     string // "tcp" or "udp"
}

// BuildRTSPURL constructs a normalized RTSP URL from individual connection components.
func BuildRTSPURL(host string, port int, path string, query url.Values, username, password string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", ErrEmptyHost
	}

	// Validate host (IPv4, IPv6, or domain name)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		// Bare IPv6 address needs brackets
		host = "[" + host + "]"
	}

	if port <= 0 {
		port = 554
	}
	if port > 65535 {
		return "", ErrInvalidPort
	}

	hostPort := host
	if port != 554 {
		hostPort = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
		if strings.Contains(host, ":") {
			hostPort = "[" + strings.Trim(host, "[]") + "]:" + strconv.Itoa(port)
		}
	}

	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	u := &url.URL{
		Scheme: "rtsp",
		Host:   hostPort,
		Path:   path,
	}

	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	if username != "" || password != "" {
		u.User = url.UserPassword(username, password)
	}

	return u.String(), nil
}

// ParseAndNormalizeRTSP parses an RTSP URL and returns normalized connection info.
// Ensures userinfo is cleanly extracted without ambiguity and host/scheme are standardized.
func ParseAndNormalizeRTSP(raw string, transport string) (*StreamConnectionInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("RTSP URL cannot be empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid RTSP URL format: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != ProtocolRTSP {
		return nil, ErrInvalidScheme
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return nil, ErrEmptyHost
	}

	// Canonicalize hostname (lowercase)
	hostname = strings.ToLower(hostname)

	portStr := parsed.Port()
	port := 554
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p <= 0 || p > 65535 {
			return nil, ErrInvalidPort
		}
		port = p
	}

	hostPort := hostname
	if strings.Contains(hostname, ":") {
		hostPort = "[" + hostname + "]"
	}
	if port != 554 {
		hostPort = net.JoinHostPort(hostname, strconv.Itoa(port))
	}

	var username, password string
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}

	transport = strings.ToLower(strings.TrimSpace(transport))
	if transport == "" {
		transport = TransportTCP
	} else if transport != TransportTCP && transport != TransportUDP {
		return nil, ErrUnsupportedTransport
	}

	// Construct clean URI (no userinfo)
	cleanURL := &url.URL{
		Scheme:   ProtocolRTSP,
		Host:     hostPort,
		Path:     parsed.Path,
		RawPath:  parsed.RawPath,
		RawQuery: parsed.RawQuery,
	}
	cleanURI := cleanURL.String()

	// Construct full normalized URI (with userinfo if present)
	fullURL := &url.URL{
		Scheme:   ProtocolRTSP,
		Host:     hostPort,
		Path:     parsed.Path,
		RawPath:  parsed.RawPath,
		RawQuery: parsed.RawQuery,
	}
	if username != "" || password != "" {
		fullURL.User = url.UserPassword(username, password)
	}
	normalizedURI := fullURL.String()

	return &StreamConnectionInfo{
		NormalizedURI: normalizedURI,
		CleanURI:      cleanURI,
		Username:      username,
		Password:      password,
		Transport:     transport,
	}, nil
}

// SanitizeURL masks credentials in any URL for safe logging and public display using standard library Redacted.
func SanitizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "[REDACTED_URL]"
	}

	return parsed.Redacted()
}
