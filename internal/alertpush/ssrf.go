package alertpush

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

var (
	// ErrSSRFBlocked indicates a destination host or resolved IP violates outbound SSRF policy.
	ErrSSRFBlocked = errors.New("destination address is blocked by SSRF policy")
	// ErrInvalidMQTTScheme indicates an unsupported MQTT broker URL scheme.
	ErrInvalidMQTTScheme = errors.New("unsupported mqtt broker scheme")

	unspecifiedIPv4Prefix = netip.MustParsePrefix("0.0.0.0/8")

	allowedMQTTSchemes = map[string]struct{}{
		"tcp":   {},
		"ssl":   {},
		"tls":   {},
		"tcps":  {},
		"ws":    {},
		"wss":   {},
		"mqtt":  {},
		"mqtts": {},
	}
)

// isAllowedMQTTScheme reports whether scheme is in the supported MQTT broker scheme whitelist.
func isAllowedMQTTScheme(scheme string) bool {
	_, ok := allowedMQTTSchemes[strings.ToLower(strings.TrimSpace(scheme))]
	return ok
}

// validateDestinationHostIP checks whether an outbound IP target is permitted under edge SSRF policy.
//
// Policy:
//   - Always blocks unspecified (0.0.0.0/8, ::), link-local/cloud-metadata (169.254.0.0/16, fe80::/10),
//     and multicast addresses.
//   - Blocks loopback (127.0.0.0/8, ::1) unless allowLoopback is true (reserved for unit tests).
//   - Explicitly allows RFC1918 private LAN networks (10/8, 172.16/12, 192.168/16) required by edge appliances.
func validateDestinationHostIP(ip net.IP, allowLoopback bool) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w: invalid IP address", ErrSSRFBlocked)
	}
	addr = addr.Unmap()

	if addr.IsUnspecified() || (addr.Is4() && unspecifiedIPv4Prefix.Contains(addr)) {
		return fmt.Errorf("%w: unspecified address %s", ErrSSRFBlocked, addr)
	}
	if addr.IsLoopback() {
		if allowLoopback {
			return nil
		}
		return fmt.Errorf("%w: loopback address %s", ErrSSRFBlocked, addr)
	}
	if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return fmt.Errorf("%w: link-local address %s", ErrSSRFBlocked, addr)
	}
	if addr.IsMulticast() {
		return fmt.Errorf("%w: multicast address %s", ErrSSRFBlocked, addr)
	}
	return nil
}

// validateWebhookTargetHost validates a raw host or host:port against static SSRF rules.
// Domain names pass static validation and are re-verified after DNS resolution at dial time.
func validateWebhookTargetHost(host string, allowLoopback bool) error {
	hostname := extractHostname(host)
	if hostname == "" {
		return fmt.Errorf("%w: empty host", ErrSSRFBlocked)
	}
	normalized := strings.ToLower(strings.TrimSuffix(hostname, "."))
	if !allowLoopback && (normalized == "localhost" || strings.HasSuffix(normalized, ".localhost")) {
		return fmt.Errorf("%w: loopback host %q", ErrSSRFBlocked, hostname)
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return validateDestinationHostIP(ip, allowLoopback)
	}
	return nil
}

func extractHostname(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(trimmed); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(trimmed, "[]")
}

// resolveAndValidateHost resolves host via DNS (or parses an IP literal) and validates every
// resulting IP before any socket connection is established, blocking DNS rebinding attacks.
func resolveAndValidateHost(
	ctx context.Context,
	host string,
	allowLoopback bool,
) ([]net.IP, error) {
	if err := validateWebhookTargetHost(host, allowLoopback); err != nil {
		return nil, err
	}
	hostname := extractHostname(host)
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: hostname, IsNotFound: true}
	}

	ips := make([]net.IP, 0, len(resolved))
	for _, r := range resolved {
		if err := validateDestinationHostIP(r.IP, allowLoopback); err != nil {
			return nil, err
		}
		ips = append(ips, r.IP)
	}
	return ips, nil
}

// newSSRFSafeDialContext returns a DialContext function that resolves DNS and verifies every
// candidate IP against SSRF rules before dialing the concrete validated IP directly.
func newSSRFSafeDialContext(
	base *net.Dialer,
	allowLoopback bool,
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := base
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
		}
		ips, err := resolveAndValidateHost(ctx, host, allowLoopback)
		if err != nil {
			return nil, err
		}

		var lastErr error
		for _, ip := range ips {
			target := net.JoinHostPort(ip.String(), port)
			conn, dialErr := dialer.DialContext(ctx, network, target)
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, lastErr
	}
}
