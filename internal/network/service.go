package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// NetworkService orchestrates host network configuration, watchdog transactions, and safety rules.
type NetworkService struct {
	provider           NetworkProvider
	watchdog           *WatchdogManager
	logger             *zap.Logger
	mu                 sync.Mutex
	cancelPendingApply context.CancelFunc
}

// NewNetworkService creates a new NetworkService.
func NewNetworkService(provider NetworkProvider, watchdog *WatchdogManager, logger *zap.Logger) *NetworkService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &NetworkService{
		provider: provider,
		watchdog: watchdog,
		logger:   logger,
	}
}

// ListInterfaces queries physical network interfaces and tags the one matching clientIP or local connection as current.
func (s *NetworkService) ListInterfaces(ctx context.Context, localAddr, host, clientIP string) ([]InterfaceInfo, error) {
	ifaces, err := s.provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list physical interfaces: %w", err)
	}

	taggedCurrent := false

	// 1. Try matching incoming socket Local Address directly to interface IP
	if cleanLocal := parseCleanIP(localAddr); cleanLocal != nil && !cleanLocal.IsLoopback() && !cleanLocal.IsUnspecified() {
		for i := range ifaces {
			for _, cidr := range ifaces[i].IPAddresses {
				ip, _, err := net.ParseCIDR(cidr)
				if err == nil && ip.Equal(cleanLocal) {
					ifaces[i].IsCurrent = true
					taggedCurrent = true
					break
				}
			}
			if taggedCurrent {
				break
			}
		}
	}

	// 2. Try matching incoming Host header IP directly to interface IP
	if !taggedCurrent {
		if cleanHost := parseCleanIP(host); cleanHost != nil && !cleanHost.IsLoopback() && !cleanHost.IsUnspecified() {
			for i := range ifaces {
				for _, cidr := range ifaces[i].IPAddresses {
					ip, _, err := net.ParseCIDR(cidr)
					if err == nil && ip.Equal(cleanHost) {
						ifaces[i].IsCurrent = true
						taggedCurrent = true
						break
					}
				}
				if taggedCurrent {
					break
				}
			}
		}
	}

	// 3. Try matching clientIP by subnet
	if !taggedCurrent {
		if cleanClient := parseCleanIP(clientIP); cleanClient != nil && !cleanClient.IsLoopback() {
			for i := range ifaces {
				for _, cidr := range ifaces[i].IPAddresses {
					_, ipnet, err := net.ParseCIDR(cidr)
					if err == nil && ipnet.Contains(cleanClient) {
						ifaces[i].IsCurrent = true
						taggedCurrent = true
						break
					}
				}
				if taggedCurrent {
					break
				}
			}
		}
	}

	// 4. Fallback to default gateway interface or first interface
	if !taggedCurrent && len(ifaces) > 0 {
		foundDefault := false
		for i := range ifaces {
			if ifaces[i].IsDefaultGW {
				ifaces[i].IsCurrent = true
				foundDefault = true
				break
			}
		}
		if !foundDefault {
			ifaces[0].IsCurrent = true
		}
	}

	// Guarantee non-nil slices so JSON serialization encodes [] instead of null
	for i := range ifaces {
		if ifaces[i].IPAddresses == nil {
			ifaces[i].IPAddresses = make([]string, 0)
		}
		if ifaces[i].DNS == nil {
			ifaces[i].DNS = make([]string, 0)
		}
	}

	return ifaces, nil
}

// ApplyConfig validates parameters, creates a watchdog transaction, and starts delayed reconfiguration.
func (s *NetworkService) ApplyConfig(
	ctx context.Context,
	iface string,
	cfg InterfaceConfig,
	currentHost string,
	clientIP string,
) (*ApplyResponse, error) {
	// 0. Permission check
	if !s.provider.HasPermission() {
		return nil, apperr.New(apperr.KindPermissionDenied, "SYSTEM_NETWORK_PERMISSION_DENIED", "Insufficient permissions: CAP_NET_ADMIN or root required", nil)
	}

	// 1. Basic validation
	if cfg.Mode != "dhcp" && cfg.Mode != "static" {
		return nil, apperr.New(apperr.KindInvalid, "INVALID_MODE", "Mode must be 'dhcp' or 'static'", nil)
	}

	if cfg.Mode == "static" {
		if err := validateStaticConfig(cfg); err != nil {
			return nil, err
		}
	}

	// 2. Fetch existing interfaces for constraints & rollback backup
	existing, err := s.provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("read existing interfaces: %w", err)
	}

	var currentTarget *InterfaceInfo
	for i := range existing {
		if existing[i].Name == iface {
			currentTarget = &existing[i]
			break
		}
	}
	if currentTarget == nil {
		return nil, apperr.New(apperr.KindNotFound, "INTERFACE_NOT_FOUND", fmt.Sprintf("Interface %q not found", iface), nil)
	}

	// 3. Single Default Gateway rule enforcement
	if cfg.SetDefault {
		for _, other := range existing {
			if other.Name != iface && other.IsDefaultGW {
				return nil, apperr.New(apperr.KindConflict, "DEFAULT_GATEWAY_CONFLICT",
					fmt.Sprintf("Interface %q is already configured as the default gateway", other.Name), nil)
			}
		}
	}

	// 4. Capture current state for rollback
	rollbackDNS := currentTarget.DNS
	if rollbackDNS == nil {
		rollbackDNS = make([]string, 0)
	}
	rollbackCfg := InterfaceConfig{
		Mode:       currentTarget.Mode,
		Gateway:    currentTarget.Gateway,
		DNS:        rollbackDNS,
		SetDefault: currentTarget.IsDefaultGW,
	}
	if len(currentTarget.IPAddresses) > 0 {
		ip, mask, err := ParseCIDRToIPAndMask(currentTarget.IPAddresses[0])
		if err == nil {
			rollbackCfg.IPAddress = ip
			rollbackCfg.SubnetMask = mask
		}
	}

	// 5. Generate secure confirm token and transaction ID
	confirmToken := GenerateSecureToken(32)
	txID := fmt.Sprintf("tx_%d_%s", time.Now().Unix(), GenerateSecureToken(4))

	// 6. Compute Target URL
	targetURL := computeTargetURL(cfg, currentHost, confirmToken)

	// 7. Persist to Watchdog
	txState := TransactionState{
		TransactionID: txID,
		InterfaceName: iface,
		ConfirmToken:  confirmToken,
		TargetURL:     targetURL,
		TimeoutSec:    DefaultTimeoutSec,
		RollbackCfg:   rollbackCfg,
	}

	if err := s.watchdog.BeginTransaction(txState); err != nil {
		return nil, fmt.Errorf("begin watchdog transaction: %w", err)
	}

	// 8. Delayed apply coordination (sleep 300ms so HTTP response completes cleanly)
	s.mu.Lock()
	if s.cancelPendingApply != nil {
		s.cancelPendingApply()
	}
	applyCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	s.cancelPendingApply = cancel
	s.mu.Unlock()

	go func() {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-applyCtx.Done():
			s.logger.Info("Delayed network reconfiguration cancelled before execution", zap.String("interface", iface))
			return
		}

		s.logger.Info("Applying delayed network reconfiguration...",
			zap.String("interface", iface),
			zap.String("mode", cfg.Mode))

		if err := s.provider.ApplyInterfaceConfig(applyCtx, iface, cfg); err != nil {
			s.logger.Error("Delayed network apply failed, rolling back immediately",
				zap.String("interface", iface),
				zap.Error(err))
			_ = s.watchdog.RollbackTransaction(context.Background())
		}
	}()

	return &ApplyResponse{
		TransactionID: txID,
		TimeoutSec:    DefaultTimeoutSec,
		TargetURL:     targetURL,
		ConfirmToken:  confirmToken,
	}, nil
}

// Stop cancels any pending asynchronous operations and disarms the watchdog timer.
func (s *NetworkService) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.cancelPendingApply != nil {
		s.cancelPendingApply()
		s.cancelPendingApply = nil
	}
	s.mu.Unlock()

	s.watchdog.Stop()
	return nil
}

// HasPermission checks whether the host runtime grants network administration privileges.
func (s *NetworkService) HasPermission() bool {
	return s.provider.HasPermission()
}

// Confirm solidifies the trial network configuration.
func (s *NetworkService) Confirm(ctx context.Context, token string) error {
	if !s.provider.HasPermission() {
		return apperr.New(apperr.KindPermissionDenied, "SYSTEM_NETWORK_PERMISSION_DENIED", "Insufficient permissions: CAP_NET_ADMIN or root required", nil)
	}
	return s.watchdog.ConfirmTransaction(token)
}

// Rollback immediately restores the prior configuration.
func (s *NetworkService) Rollback(ctx context.Context, token string) error {
	if !s.provider.HasPermission() {
		return apperr.New(apperr.KindPermissionDenied, "SYSTEM_NETWORK_PERMISSION_DENIED", "Insufficient permissions: CAP_NET_ADMIN or root required", nil)
	}
	return s.watchdog.RollbackTransaction(ctx)
}

// Ping checks reachability of a target host or IP.
func (s *NetworkService) Ping(ctx context.Context, target string) (*PingResponse, error) {
	return PingProbe(ctx, target)
}

// ResetNetwork executes emergency recovery to maintenance IP 192.168.1.168/24 and restores secondary NICs to DHCP.
func (s *NetworkService) ResetNetwork(ctx context.Context) error {
	if !s.provider.HasPermission() {
		return apperr.New(apperr.KindPermissionDenied, "SYSTEM_NETWORK_PERMISSION_DENIED", "Insufficient permissions: CAP_NET_ADMIN or root required", nil)
	}

	ifaces, err := s.provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no physical network interfaces found")
	}

	primary := ifaces[0].Name
	for _, iface := range ifaces {
		if iface.Name == "eth0" {
			primary = "eth0"
			break
		}
	}

	s.logger.Warn("Executing emergency network reset", zap.String("primary_interface", primary))
	if err := s.provider.ResetInterfaceToMaintenance(ctx, primary); err != nil {
		return fmt.Errorf("reset primary interface %s: %w", primary, err)
	}

	// Secondary interfaces restored to DHCP
	for _, iface := range ifaces {
		if iface.Name != primary {
			s.logger.Info("Restoring secondary interface to DHCP", zap.String("interface", iface.Name))
			_ = s.provider.ApplyInterfaceConfig(ctx, iface.Name, InterfaceConfig{
				Mode: "dhcp",
			})
		}
	}
	return nil
}

// OnBootCheck evaluates watchdog transactions on startup.
func (s *NetworkService) OnBootCheck(ctx context.Context) error {
	return s.watchdog.CheckPendingBootTransaction(ctx)
}

// GetTransactionStatus returns active transaction state if any.
func (s *NetworkService) GetTransactionStatus() *TransactionState {
	st := s.watchdog.GetActiveTransaction()
	if st != nil && st.RollbackCfg.DNS == nil {
		st.RollbackCfg.DNS = make([]string, 0)
	}
	return st
}

func validateStaticConfig(cfg InterfaceConfig) error {
	ip := net.ParseIP(strings.TrimSpace(cfg.IPAddress))
	if ip == nil || ip.To4() == nil {
		return apperr.New(apperr.KindInvalid, "INVALID_IP_ADDRESS", "A valid IPv4 address is required", nil)
	}
	if ip.IsLoopback() || ip.IsMulticast() {
		return apperr.New(apperr.KindInvalid, "INVALID_IP_ADDRESS", "Loopback and multicast addresses are not allowed", nil)
	}

	maskIP := net.ParseIP(strings.TrimSpace(cfg.SubnetMask))
	if maskIP == nil || maskIP.To4() == nil {
		return apperr.New(apperr.KindInvalid, "INVALID_SUBNET_MASK", "A valid IPv4 subnet mask is required", nil)
	}

	maskBytes := maskIP.To4()
	mask := net.IPv4Mask(maskBytes[0], maskBytes[1], maskBytes[2], maskBytes[3])
	ones, bits := mask.Size()
	if bits != 32 || ones == 0 {
		return apperr.New(apperr.KindInvalid, "INVALID_SUBNET_MASK", "Invalid IPv4 subnet mask", nil)
	}

	// Validate gateway if set
	if strings.TrimSpace(cfg.Gateway) != "" {
		gwIP := net.ParseIP(strings.TrimSpace(cfg.Gateway))
		if gwIP == nil || gwIP.To4() == nil {
			return apperr.New(apperr.KindInvalid, "INVALID_GATEWAY", "Gateway must be a valid IPv4 address", nil)
		}
		// Gateway must belong to the same subnet
		ipNet := net.IPNet{IP: ip.To4().Mask(mask), Mask: mask}
		if !ipNet.Contains(gwIP.To4()) {
			return apperr.New(apperr.KindInvalid, "GATEWAY_OUT_OF_SUBNET",
				fmt.Sprintf("Gateway %s does not belong to subnet %s/%d", cfg.Gateway, ip.String(), ones), nil)
		}
	}

	// Validate DNS entries
	for _, dns := range cfg.DNS {
		trimmed := strings.TrimSpace(dns)
		if trimmed != "" && (net.ParseIP(trimmed) == nil || net.ParseIP(trimmed).To4() == nil) {
			return apperr.New(apperr.KindInvalid, "INVALID_DNS", fmt.Sprintf("Invalid DNS IPv4 address %q", dns), nil)
		}
	}

	return nil
}

func computeTargetURL(cfg InterfaceConfig, currentHost string, token string) string {
	cleanHost := strings.TrimSpace(currentHost)
	port := "8080"
	if cleanHost != "" {
		if h, p, err := net.SplitHostPort(cleanHost); err == nil {
			port = p
			_ = h
		}
	}

	if cfg.Mode == "static" && cfg.IPAddress != "" {
		return fmt.Sprintf("http://%s:%s/#settings?token=%s", cfg.IPAddress, port, token)
	}

	if cleanHost != "" {
		return fmt.Sprintf("http://%s/#settings?token=%s", cleanHost, token)
	}
	return fmt.Sprintf("http://127.0.0.1:8080/#settings?token=%s", token)
}

func parseCleanIP(clientIP string) net.IP {
	clean := strings.TrimSpace(clientIP)
	if clean == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(clean); err == nil {
		clean = host
	}
	return net.ParseIP(clean)
}
