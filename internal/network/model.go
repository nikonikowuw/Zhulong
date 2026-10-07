package network

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// InterfaceInfo describes the operational and configuration state of a physical network interface.
type InterfaceInfo struct {
	Name        string   `json:"name"`        // Interface identifier, e.g. "eth0"
	MAC         string   `json:"mac"`         // Physical MAC address, e.g. "52:54:00:12:34:56"
	LinkUp      bool     `json:"linkUp"`      // Carrier status (cable plugged in)
	Mode        string   `json:"mode"`        // "dhcp" or "static"
	IPAddresses []string `json:"ipAddresses"` // Assigned IPv4 addresses with CIDR prefix, e.g. ["192.168.1.100/24"]
	Gateway     string   `json:"gateway"`     // Configured gateway address, or empty
	DNS         []string `json:"dns"`         // Configured DNS servers
	IsDefaultGW bool     `json:"isDefaultGw"` // True if interface hosts default route (0.0.0.0/0)
	IsCurrent   bool     `json:"isCurrent"`   // True if current HTTP connection was routed through this interface
}

// InterfaceConfig contains parameters to reconfigure a network interface.
type InterfaceConfig struct {
	Mode       string   `json:"mode" binding:"required,oneof=dhcp static"`
	IPAddress  string   `json:"ipAddress"`  // Required when mode is "static", e.g. "192.168.1.100"
	SubnetMask string   `json:"subnetMask"` // Required when mode is "static", e.g. "255.255.255.0"
	Gateway    string   `json:"gateway"`    // Optional gateway address, e.g. "192.168.1.1"
	DNS        []string `json:"dns"`        // DNS servers list
	SetDefault bool     `json:"setDefault"` // Designate this interface as default gateway
}

// ApplyResponse is returned when an interface configuration has been accepted for two-phase trial.
type ApplyResponse struct {
	TransactionID string `json:"transactionId"`
	TimeoutSec    int    `json:"timeoutSec"`
	TargetURL     string `json:"targetUrl"`
	ConfirmToken  string `json:"confirmToken"`
}

// ConfirmRequest specifies confirmation parameters.
type ConfirmRequest struct {
	Token string `json:"token"`
}

// RollbackRequest specifies manual rollback parameters.
type RollbackRequest struct {
	Token string `json:"token"`
}

// PingRequest specifies a target host/IP for ping connectivity probe.
type PingRequest struct {
	Target string `json:"target" binding:"required"`
}

// PingResponse contains the connectivity probe result.
type PingResponse struct {
	Reachable bool    `json:"reachable"`
	RTTMs     float64 `json:"rttMs"`
}

// TransactionState represents the persisted watchdog transaction state machine.
type TransactionState struct {
	TransactionID string          `json:"transactionId"`
	Status        string          `json:"status"` // "idle", "pending_confirm", "rolling_back"
	InterfaceName string          `json:"interfaceName"`
	ConfirmToken  string          `json:"confirmToken"`
	TargetURL     string          `json:"targetUrl"`
	TimeoutSec    int             `json:"timeoutSec"`
	ExpiresAt     time.Time       `json:"expiresAt"`
	RollbackCfg   InterfaceConfig `json:"rollbackConfig"`
}

// CIDRFromIPAndMask converts an IPv4 address and subnet mask string into CIDR notation (e.g. "192.168.1.100/24").
func CIDRFromIPAndMask(ipStr, maskStr string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("invalid IPv4 address: %q", ipStr)
	}

	maskIP := net.ParseIP(strings.TrimSpace(maskStr))
	if maskIP == nil || maskIP.To4() == nil {
		return "", fmt.Errorf("invalid IPv4 subnet mask: %q", maskStr)
	}

	maskBytes := maskIP.To4()
	mask := net.IPv4Mask(maskBytes[0], maskBytes[1], maskBytes[2], maskBytes[3])
	ones, bits := mask.Size()
	if bits != 32 || ones == 0 {
		return "", fmt.Errorf("invalid IPv4 subnet mask bits: %q", maskStr)
	}

	return fmt.Sprintf("%s/%d", ip.To4().String(), ones), nil
}

// ParseCIDRToIPAndMask splits a CIDR string (e.g. "192.168.1.100/24") into IP address and subnet mask string.
func ParseCIDRToIPAndMask(cidr string) (string, string, error) {
	ip, ipNet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return "", "", fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	if ip.To4() == nil {
		return "", "", fmt.Errorf("only IPv4 is supported: %q", cidr)
	}

	mask := ipNet.Mask
	if len(mask) == 4 {
		maskStr := fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
		return ip.String(), maskStr, nil
	}
	if len(mask) == 16 {
		maskStr := fmt.Sprintf("%d.%d.%d.%d", mask[12], mask[13], mask[14], mask[15])
		return ip.String(), maskStr, nil
	}
	return ip.String(), "255.255.255.0", nil
}
