package gb28181

import (
	"net"
	"strconv"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// This file owns everything that reasons about addresses: what a valid one looks
// like, and which one is advertised when the operator leaves it empty. Keeping it
// apart from model.go separates "what the config says" from "what the host is".

// portOutOfRange reports whether a port cannot be bound at all.
func portOutOfRange(port int) bool {
	return port < 1 || port > 65535
}

func validateListenAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return apperr.Invalid(CodeConfigInvalid,
			"sipListen must use host:port format", err)
	}
	// IPv6 is accepted here even though media_bind_address/media_advertised_ip are
	// IPv4-only (RTP sockets and the SDP c= line stay IPv4 in P0): signalling and
	// media may legitimately use different families, and refusing an IPv6 SIP bind
	// would block that deployment without protecting anything.
	if host != "" && net.ParseIP(host) == nil {
		return apperr.Invalid(CodeConfigInvalid,
			"sipListen host must be an IP address", nil)
	}
	// strconv instead of net.LookupPort: a service name such as "sip" would be
	// accepted by LookupPort and then persisted as an unbindable value.
	portNumber, err := strconv.Atoi(port)
	if err != nil || portOutOfRange(portNumber) {
		return apperr.Invalid(CodeConfigInvalid,
			"sipListen port must be numeric and within 1..65535", nil)
	}
	return nil
}

func validateIPv4(field, value string) error {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return apperr.Invalid(CodeAddressInvalid,
			field+" must be a valid IPv4 address", nil)
	}
	return nil
}

// resolveAdvertisedIP fills media_advertised_ip when it was left empty.
//
// An empty advertised address opts into local-address inference and assumes there
// is no NAT. NAT cannot be reliably inferred from local interface addresses, so
// NAT deployments must provide an explicit reachable address. Inference requires
// exactly one non-loopback IPv4, and a concrete bind address must match it.
func resolveAdvertisedIP(bindAddress, advertised string, candidates []net.IP) (string, error) {
	if advertised != "" {
		if err := validateIPv4("mediaAdvertisedIp", advertised); err != nil {
			return "", err
		}
		return advertised, nil
	}
	bindIP := net.ParseIP(bindAddress)
	if bindIP == nil || bindIP.To4() == nil {
		return "", apperr.Invalid(CodeAddressInvalid,
			"mediaBindAddress must be a valid IPv4 address", nil)
	}
	bindIP = bindIP.To4()

	usable := usableCandidates(candidates)
	switch len(usable) {
	case 0:
		return "", apperr.Invalid(CodeAddressUnresolvable,
			"mediaAdvertisedIp is empty and no non-loopback IPv4 address was found", nil)
	case 1:
		if !bindIP.IsUnspecified() && !bindIP.Equal(usable[0]) {
			return "", apperr.Invalid(CodeAddressUnresolvable,
				"mediaBindAddress does not match the host's only non-loopback IPv4 address; set mediaAdvertisedIp explicitly", nil)
		}
		return usable[0].String(), nil
	default:
		return "", apperr.Invalid(CodeAddressUnresolvable,
			"mediaAdvertisedIp is empty and the host has multiple IPv4 addresses; set it explicitly", nil)
	}
}

// usableCandidates keeps the addresses that can be advertised, de-duplicated in
// discovery order. Loopback and the unspecified address are never reachable from a
// device, so they cannot serve as the SDP c= line.
func usableCandidates(candidates []net.IP) []net.IP {
	usable := make([]net.IP, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		ip4 := candidate.To4()
		if ip4 == nil {
			continue
		}
		if ip4.IsLoopback() || ip4.IsUnspecified() {
			continue
		}
		key := ip4.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		usable = append(usable, ip4)
	}
	return usable
}
