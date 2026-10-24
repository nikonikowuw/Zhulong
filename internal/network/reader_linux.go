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

// virtualInterfacePrefixes lists interface name prefixes that never represent physical hardware.
var virtualInterfacePrefixes = []string{
	"docker", "br-", "veth", "tun", "tap", "virbr",
	"cilium_", "flannel", "kube-ipvs", "dummy", "tailscale",
}

// readPhysicalInterfaces reads physical interface states from Linux sysfs, netlink and /proc/net/route.
func readPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, fmt.Errorf("read /sys/class/net: %w", err)
	}

	defaultRoutes := parseLinuxDefaultRoutes()
	dnsServers := parseLinuxResolvDNS()

	result := make([]InterfaceInfo, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		name := entry.Name()
		if isVirtualOrIgnored(name) {
			continue
		}

		// Only interfaces backed by hardware expose /sys/class/net/<iface>/device.
		devicePath := filepath.Join("/sys/class/net", name, "device")
		if _, err := os.Stat(devicePath); err != nil {
			continue
		}

		result = append(result, readLinuxInterface(name, defaultRoutes[name], dnsServers))
	}

	return result, nil
}

// readLinuxInterface collects the runtime state of a single physical interface.
func readLinuxInterface(name, gateway string, dnsServers []string) InterfaceInfo {
	info := InterfaceInfo{
		Name:        name,
		Mode:        modeDHCP, // default unless a static configuration is detected
		DNS:         dnsServers,
		IPAddresses: make([]string, 0),
		Gateway:     gateway,
		IsDefaultGW: gateway != "",
	}

	if macBytes, err := os.ReadFile(filepath.Join("/sys/class/net", name, "address")); err == nil {
		info.MAC = strings.ToUpper(strings.TrimSpace(string(macBytes)))
	}

	info.LinkUp = readLinkUp(name)

	if netIface, err := net.InterfaceByName(name); err == nil {
		if addrs, err := netIface.Addrs(); err == nil {
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				if ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
					info.IPAddresses = append(info.IPAddresses, ipNet.String())
				}
			}
		}
	}

	if isStaticConfigured(name) {
		info.Mode = modeStatic
	}

	return info
}

// readLinkUp prefers the carrier flag and falls back to operstate when carrier is unavailable.
func readLinkUp(name string) bool {
	if carrier, err := os.ReadFile(filepath.Join("/sys/class/net", name, "carrier")); err == nil {
		return strings.TrimSpace(string(carrier)) == "1"
	}

	if operState, err := os.ReadFile(filepath.Join("/sys/class/net", name, "operstate")); err == nil {
		return strings.TrimSpace(string(operState)) == "up"
	}
	return false
}

// isVirtualOrIgnored checks if an interface name matches known virtual patterns.
func isVirtualOrIgnored(name string) bool {
	if name == "lo" {
		return true
	}

	for _, prefix := range virtualInterfacePrefixes {
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
	if !scanner.Scan() { // skip the header line
		return result
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}

		iface, dest, gwHex, mask := fields[0], fields[1], fields[2], fields[7]
		if dest == "00000000" && mask == "00000000" {
			result[iface] = parseHexIP(gwHex)
		}
	}
	return result
}

// parseHexIP converts little-endian 32-bit hex (e.g. 0101A8C0) to dotted decimal IPv4 (192.168.1.1).
func parseHexIP(hexStr string) string {
	if len(hexStr) != 8 {
		return ""
	}

	decoded, err := hex.DecodeString(hexStr)
	if err != nil || len(decoded) != 4 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", decoded[3], decoded[2], decoded[1], decoded[0])
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
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		if net.ParseIP(fields[1]) != nil {
			dns = append(dns, fields[1])
		}
	}
	return dns
}

// isStaticConfigured checks if systemd-networkd or NetworkManager configured this interface as static.
func isStaticConfigured(iface string) bool {
	networkdPath := filepath.Join("/etc/systemd/network", fmt.Sprintf("10-%s.network", iface))
	if content, err := os.ReadFile(networkdPath); err == nil {
		if strings.Contains(string(content), "Address=") && !strings.Contains(string(content), "DHCP=yes") {
			return true
		}
	}

	nmPath := filepath.Join("/etc/NetworkManager/system-connections", fmt.Sprintf("%s.nmconnection", iface))
	if content, err := os.ReadFile(nmPath); err == nil {
		if strings.Contains(string(content), "method=manual") {
			return true
		}
	}

	return false
}
