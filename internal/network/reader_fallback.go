//go:build !linux

package network

import (
	"context"
	"net"
	"strings"
)

// readPhysicalInterfaces reads host physical network interfaces on non-Linux platforms.
func readPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	return readFallbackPhysicalInterfaces(ctx)
}

// readFallbackPhysicalInterfaces returns interface states on non-Linux platforms (e.g. Darwin, Windows).
func readFallbackPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return fallbackMockInterfaces(), nil
	}

	var result []InterfaceInfo
	for _, iface := range ifaces {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		name := iface.Name
		if isIgnoredFallback(name, iface.Flags) {
			continue
		}

		addrs, _ := iface.Addrs()
		var ipAddrs []string
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
					ipAddrs = append(ipAddrs, ipNet.String())
				}
			}
		}

		// Only include interfaces that have MAC addresses
		mac := strings.ToUpper(iface.HardwareAddr.String())
		if mac == "" {
			continue
		}

		info := InterfaceInfo{
			Name:        name,
			MAC:         mac,
			LinkUp:      (iface.Flags & net.FlagUp) != 0,
			Mode:        "dhcp",
			IPAddresses: ipAddrs,
			Gateway:     "",
			DNS:         []string{"8.8.8.8", "1.1.1.1"},
			IsDefaultGW: false,
		}
		result = append(result, info)
	}

	if len(result) == 0 {
		return fallbackMockInterfaces(), nil
	}

	// Designate the first interface with an IP as default gateway for dev convenience
	for i := range result {
		if len(result[i].IPAddresses) > 0 {
			result[i].IsDefaultGW = true
			result[i].Gateway = "192.168.1.1"
			break
		}
	}

	return result, nil
}

func isIgnoredFallback(name string, flags net.Flags) bool {
	if flags&net.FlagLoopback != 0 {
		return true
	}
	ignoredPrefixes := []string{
		"lo", "docker", "utun", "bridge", "veth", "gif", "stf", "llw", "awdl", "p2p", "ap",
	}
	for _, prefix := range ignoredPrefixes {
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
			Mode:        "dhcp",
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
			Mode:        "static",
			IPAddresses: []string{"10.0.0.2/24"},
			Gateway:     "",
			DNS:         nil,
			IsDefaultGW: false,
			IsCurrent:   false,
		},
	}
}
