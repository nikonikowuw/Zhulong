//go:build linux

package network

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// readPhysicalInterfaces reads host physical network interfaces on Linux.
func readPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	return readLinuxPhysicalInterfaces(ctx)
}

// readLinuxPhysicalInterfaces reads physical interface states from Linux sysfs, netlink and /proc/net/route.
func readLinuxPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, fmt.Errorf("read /sys/class/net: %w", err)
	}

	defaultRoutes := parseLinuxDefaultRoutes()
	dnsServers := parseLinuxResolvDNS()

	var result []InterfaceInfo
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		name := entry.Name()
		if isVirtualOrIgnored(name) {
			continue
		}

		// Verify physical hardware backing by checking /sys/class/net/<iface>/device
		devicePath := filepath.Join("/sys/class/net", name, "device")
		if _, err := os.Stat(devicePath); err != nil {
			// Not a physical hardware device
			continue
		}

		info := InterfaceInfo{
			Name:        name,
			Mode:        "dhcp", // default unless detected static
			DNS:         dnsServers,
			IPAddresses: make([]string, 0),
		}

		// MAC Address
		if macBytes, err := os.ReadFile(filepath.Join("/sys/class/net", name, "address")); err == nil {
			info.MAC = strings.ToUpper(strings.TrimSpace(string(macBytes)))
		}

		// Carrier (cable connected)
		if carrierBytes, err := os.ReadFile(filepath.Join("/sys/class/net", name, "carrier")); err == nil {
			info.LinkUp = strings.TrimSpace(string(carrierBytes)) == "1"
		} else {
			// Fallback to operstate
			if operBytes, err := os.ReadFile(filepath.Join("/sys/class/net", name, "operstate")); err == nil {
				info.LinkUp = strings.TrimSpace(string(operBytes)) == "up"
			}
		}

		// IP Addresses
		if netIface, err := net.InterfaceByName(name); err == nil {
			if addrs, err := netIface.Addrs(); err == nil {
				for _, addr := range addrs {
					if ipNet, ok := addr.(*net.IPNet); ok {
						if ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
							info.IPAddresses = append(info.IPAddresses, ipNet.String())
						}
					}
				}
			}
		}

		// Default Gateway & Route
		if gw, isDefault := defaultRoutes[name]; isDefault {
			info.Gateway = gw
			info.IsDefaultGW = true
		}

		// Detect mode if systemd-networkd or NetworkManager files exist
		if isStaticConfigured(name) {
			info.Mode = "static"
		}

		result = append(result, info)
	}

	return result, nil
}

// isVirtualOrIgnored checks if an interface name matches known virtual patterns.
func isVirtualOrIgnored(name string) bool {
	if name == "lo" {
		return true
	}
	ignoredPrefixes := []string{
		"docker", "br-", "veth", "tun", "tap", "virbr",
		"cilium_", "flannel", "kube-ipvs", "dummy", "tailscale",
	}
	for _, prefix := range ignoredPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// parseLinuxDefaultRoutes parses /proc/net/route to find interfaces carrying default route 0.0.0.0/0.
func parseLinuxDefaultRoutes() map[string]string {
	result := make(map[string]string)
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return result
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		iface := fields[0]
		dest := fields[1]
		gwHex := fields[2]
		mask := fields[7]

		// Default gateway has Destination == 00000000 and Mask == 00000000
		if dest == "00000000" && mask == "00000000" {
			gwIP := parseHexIP(gwHex)
			result[iface] = gwIP
		}
	}
	return result
}

// parseHexIP converts little-endian 32-bit hex (e.g. 0101A8C0) to dotted decimal IPv4 (192.168.1.1).
func parseHexIP(hexStr string) string {
	if len(hexStr) != 8 {
		return ""
	}
	bytes, err := hex.DecodeString(hexStr)
	if err != nil || len(bytes) != 4 {
		return ""
	}
	// Little endian: bytes[3].bytes[2].bytes[1].bytes[0]
	return fmt.Sprintf("%d.%d.%d.%d", bytes[3], bytes[2], bytes[1], bytes[0])
}

// parseLinuxResolvDNS parses nameservers from /etc/resolv.conf.
func parseLinuxResolvDNS() []string {
	dns := make([]string, 0)
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return dns
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "nameserver ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ip := fields[1]
				if net.ParseIP(ip) != nil {
					dns = append(dns, ip)
				}
			}
		}
	}
	return dns
}

// isStaticConfigured checks if systemd-networkd or NetworkManager configured this interface as static.
func isStaticConfigured(iface string) bool {
	// Check systemd-networkd config
	networkdPath := filepath.Join("/etc/systemd/network", fmt.Sprintf("10-%s.network", iface))
	if content, err := os.ReadFile(networkdPath); err == nil {
		if strings.Contains(string(content), "Address=") && !strings.Contains(string(content), "DHCP=yes") {
			return true
		}
	}

	// Check NetworkManager connection file
	nmPath := filepath.Join("/etc/NetworkManager/system-connections", fmt.Sprintf("%s.nmconnection", iface))
	if content, err := os.ReadFile(nmPath); err == nil {
		if strings.Contains(string(content), "method=manual") {
			return true
		}
	}

	return false
}
