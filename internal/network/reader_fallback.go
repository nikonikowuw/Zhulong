//go:build !linux

package network

import (
	"context"
	"net"
	"strings"
)

// fallbackIgnoredPrefixes lists interface name prefixes that are not physical links on dev hosts.
var fallbackIgnoredPrefixes = []string{
	"lo", "docker", "utun", "bridge", "veth", "gif", "stf", "llw", "awdl", "p2p", "ap",
}

// readPhysicalInterfaces returns interface states on non-Linux platforms (e.g. Darwin, Windows).
func readPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return fallbackMockInterfaces(), nil
	}

	result := make([]InterfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if isIgnoredFallback(iface.Name, iface.Flags) {
			continue
		}

		// Interfaces without a MAC address are not physical links.
		mac := strings.ToUpper(iface.HardwareAddr.String())
		if mac == "" {
			continue
		}

		result = append(result, InterfaceInfo{
			Name:        iface.Name,
			MAC:         mac,
			LinkUp:      (iface.Flags & net.FlagUp) != 0,
			Mode:        modeDHCP,
			IPAddresses: ipv4Networks(iface),
			Gateway:     "",
			DNS:         []string{"8.8.8.8", "1.1.1.1"},
			IsDefaultGW: false,
		})
	}

	if len(result) == 0 {
		return fallbackMockInterfaces(), nil
	}

	// Designate the first interface with an IP as default gateway for dev convenience.
	for i := range result {
		if len(result[i].IPAddresses) == 0 {
			continue
		}
		result[i].IsDefaultGW = true
		result[i].Gateway = "192.168.1.1"
		break
	}

	return result, nil
}

// ipv4Networks returns the non-loopback IPv4 addresses of iface in CIDR notation.
func ipv4Networks(iface net.Interface) []string {
	addrs, err := iface.Addrs()
	if err != nil {
		return make([]string, 0)
	}

	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
			result = append(result, ipNet.String())
		}
	}
	return result
}

func isIgnoredFallback(name string, flags net.Flags) bool {
	if flags&net.FlagLoopback != 0 {
		return true
	}

	for _, prefix := range fallbackIgnoredPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func fallbackMockInterfaces() []InterfaceInfo {
	return []InterfaceInfo{
		{
			Name:        "eth0",
			MAC:         "52:54:00:12:34:56",
			LinkUp:      true,
			Mode:        modeDHCP,
			IPAddresses: []string{"192.168.1.100/24"},
			Gateway:     "192.168.1.1",
			DNS:         []string{"8.8.8.8", "1.1.1.1"},
			IsDefaultGW: true,
			IsCurrent:   false,
		},
		{
			Name:        "eth1",
			MAC:         "52:54:00:AB:CD:EF",
			LinkUp:      false,
			Mode:        modeStatic,
			IPAddresses: []string{"10.0.0.2/24"},
			Gateway:     "",
			DNS:         []string{},
			IsDefaultGW: false,
			IsCurrent:   false,
		},
	}
}
