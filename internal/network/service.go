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

const (
	// applyDelay lets the HTTP response flush before the interface is reconfigured.
	applyDelay = 300 * time.Millisecond

	// applyTimeout bounds the delayed reconfiguration handed to the provider.
	applyTimeout = 20 * time.Second

	// maintenanceInterface is preferred as primary during emergency recovery when present.
	maintenanceInterface = "eth0"
)

// NetworkService orchestrates host network configuration, watchdog transactions, and safety rules.
type NetworkService struct {
	provider           NetworkProvider
	watchdog           *WatchdogManager
	logger             *zap.Logger
	publicPort         string
	mu                 sync.Mutex
	cancelPendingApply context.CancelFunc
}

// NewNetworkService creates a new NetworkService.
//
// publicPort is the console port the host listens on; it is only used to build the confirmation
// URL that a client must open after the interface has been reconfigured.
func NewNetworkService(
	provider NetworkProvider,
	watchdog *WatchdogManager,
	publicPort string,
	logger *zap.Logger,
) *NetworkService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &NetworkService{
		provider:   provider,
		watchdog:   watchdog,
		logger:     logger,
		publicPort: publicPort,
	}
}

func errNetworkPermissionDenied() error {
	return apperr.New(
		apperr.KindPermissionDenied,
		"SYSTEM_NETWORK_PERMISSION_DENIED",
		"Insufficient permissions: CAP_NET_ADMIN or root required",
		nil,
	)
}

// ListInterfaces lists physical interfaces and marks the current one only when the match is unambiguous.
func (s *NetworkService) ListInterfaces(ctx context.Context, origin RequestOrigin) ([]InterfaceInfo, error) {
	ifaces, err := s.provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list physical interfaces: %w", err)
	}

	for i := range ifaces {
		ifaces[i].IsCurrent = false
	}

	candidates := []struct {
		address  string
		bySubnet bool
	}{
		{address: origin.LocalAddr},
		{address: origin.Host},
		{address: origin.ClientIP, bySubnet: true},
	}

	for _, candidate := range candidates {
		ip := parseCleanIP(candidate.address)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}

		if index := findUniqueInterfaceIndex(ifaces, ip, candidate.bySubnet); index >= 0 {
			ifaces[index].IsCurrent = true
			break
		}
	}

	// Guarantee non-nil slices so JSON serialization encodes [] instead of null.
	if ifaces == nil {
		ifaces = make([]InterfaceInfo, 0)
	}
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

func findUniqueInterfaceIndex(ifaces []InterfaceInfo, address net.IP, bySubnet bool) int {
	matchedIndex := -1
	for index := range ifaces {
		for _, cidr := range ifaces[index].IPAddresses {
			ip, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				continue
			}

			matches := ip.Equal(address)
			if bySubnet {
				matches = ipNet.Contains(address)
			}
			if !matches {
				continue
			}

			if matchedIndex >= 0 && matchedIndex != index {
				return -1
			}
			matchedIndex = index
			break
		}
	}
	return matchedIndex
}

// ApplyConfig validates parameters, creates a watchdog transaction, and starts delayed reconfiguration.
func (s *NetworkService) ApplyConfig(
	ctx context.Context,
	iface string,
	cfg InterfaceConfig,
	currentHost string,
) (*ApplyResponse, error) {
	if !s.provider.HasPermission() {
		return nil, errNetworkPermissionDenied()
	}

	if !isValidMode(cfg.Mode) {
		return nil, apperr.New(apperr.KindInvalid, "INVALID_MODE", "Mode must be 'dhcp' or 'static'", nil)
	}

	if cfg.Mode == modeStatic {
		if err := validateStaticConfig(cfg); err != nil {
			return nil, err
		}
	}

	// Fetch existing interfaces for constraints and rollback backup.
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
		return nil, apperr.New(
			apperr.KindNotFound,
			"INTERFACE_NOT_FOUND",
			fmt.Sprintf("Interface %q not found", iface),
			nil,
		)
	}

	if cfg.SetDefault {
		for _, other := range existing {
			if other.Name == iface || !other.IsDefaultGW {
				continue
			}
			return nil, apperr.New(
				apperr.KindConflict,
				"DEFAULT_GATEWAY_CONFLICT",
				fmt.Sprintf("Interface %q is already configured as the default gateway", other.Name),
				nil,
			)
		}
	}

	confirmToken, err := GenerateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate confirmation token: %w", err)
	}
	txSuffix, err := GenerateSecureToken(4)
	if err != nil {
		return nil, fmt.Errorf("generate transaction id: %w", err)
	}

	txID := fmt.Sprintf("tx_%d_%s", time.Now().Unix(), txSuffix)
	targetURL := computeTargetURL(cfg, currentHost, s.publicPort, confirmToken)

	txState := TransactionState{
		TransactionID: txID,
		InterfaceName: iface,
		ConfirmToken:  confirmToken,
		TargetURL:     targetURL,
		TimeoutSec:    DefaultTimeoutSec,
		RollbackCfg:   buildRollbackConfig(*currentTarget),
	}

	if err := s.watchdog.BeginTransaction(txState); err != nil {
		return nil, fmt.Errorf("begin watchdog transaction: %w", err)
	}

	s.scheduleApply(iface, cfg)

	return &ApplyResponse{
		TransactionID: txID,
		TimeoutSec:    DefaultTimeoutSec,
		TargetURL:     targetURL,
		ConfirmToken:  confirmToken,
	}, nil
}

// scheduleApply runs the reconfiguration after the HTTP response has been written,
// cancelling any previously scheduled apply.
func (s *NetworkService) scheduleApply(iface string, cfg InterfaceConfig) {
	s.mu.Lock()
	if s.cancelPendingApply != nil {
		s.cancelPendingApply()
	}
	applyCtx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	s.cancelPendingApply = cancel
	s.mu.Unlock()

	go func() {
		defer cancel()

		select {
		case <-time.After(applyDelay):
		case <-applyCtx.Done():
			s.logger.Info("Delayed network reconfiguration cancelled before execution",
				zap.String("interface", iface))
			return
		}

		s.logger.Info("Applying delayed network reconfiguration...",
			zap.String("interface", iface),
			zap.String("mode", cfg.Mode))

		if err := s.provider.ApplyInterfaceConfig(applyCtx, iface, cfg); err != nil {
			s.logger.Error("Delayed network apply failed, rolling back immediately",
				zap.String("interface", iface),
				zap.Error(err))

			if rbErr := s.watchdog.RollbackTransaction(context.Background()); rbErr != nil {
				s.logger.Error("Immediate rollback after apply failure also failed; host may be unreachable",
					zap.String("interface", iface),
					zap.Error(rbErr))
			}
		}
	}()
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
		return errNetworkPermissionDenied()
	}
	return s.watchdog.ConfirmTransaction(token)
}

// Rollback immediately restores the prior configuration.
func (s *NetworkService) Rollback(ctx context.Context, token string) error {
	if !s.provider.HasPermission() {
		return errNetworkPermissionDenied()
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
		return errNetworkPermissionDenied()
	}

	ifaces, err := s.provider.ListPhysicalInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no physical network interfaces found")
	}

	primary := pickPrimaryInterface(ifaces)
	s.logger.Warn("Executing emergency network reset", zap.String("primary_interface", primary))
	if err := s.provider.ResetInterfaceToMaintenance(ctx, primary); err != nil {
		return fmt.Errorf("reset primary interface %s: %w", primary, err)
	}

	// Secondary interfaces are restored to DHCP. A failure here must not abort the recovery,
	// because the primary interface is already on the maintenance address.
	for _, ifc := range ifaces {
		if ifc.Name == primary {
			continue
		}

		s.logger.Info("Restoring secondary interface to DHCP", zap.String("interface", ifc.Name))
		if err := s.provider.ApplyInterfaceConfig(ctx, ifc.Name, InterfaceConfig{Mode: modeDHCP}); err != nil {
			s.logger.Error("Failed to restore secondary interface to DHCP",
				zap.String("interface", ifc.Name),
				zap.Error(err))
		}
	}

	return nil
}

func pickPrimaryInterface(ifaces []InterfaceInfo) string {
	for _, ifc := range ifaces {
		if ifc.Name == maintenanceInterface {
			return maintenanceInterface
		}
	}
	return ifaces[0].Name
}

// OnBootCheck evaluates watchdog transactions on startup.
func (s *NetworkService) OnBootCheck(ctx context.Context) error {
	return s.watchdog.CheckPendingBootTransaction(ctx)
}

// GetTransactionStatus returns active transaction state if any.
func (s *NetworkService) GetTransactionStatus() *TransactionState {
	return s.watchdog.GetActiveTransaction()
}

// buildRollbackConfig captures the current interface state as the configuration to restore.
func buildRollbackConfig(current InterfaceInfo) InterfaceConfig {
	dns := current.DNS
	if dns == nil {
		dns = make([]string, 0)
	}

	cfg := InterfaceConfig{
		Mode:       current.Mode,
		Gateway:    current.Gateway,
		DNS:        dns,
		SetDefault: current.IsDefaultGW,
	}

	if len(current.IPAddresses) == 0 {
		return cfg
	}

	if ip, mask, err := ParseCIDRToIPAndMask(current.IPAddresses[0]); err == nil {
		cfg.IPAddress = ip
		cfg.SubnetMask = mask
	}
	return cfg
}

// isValidMode reports whether mode is one of the supported interface configuration modes.
func isValidMode(mode string) bool {
	return mode == modeDHCP || mode == modeStatic
}

func validateStaticConfig(cfg InterfaceConfig) error {
	ip := parseIPv4(cfg.IPAddress)
	if ip == nil {
		return apperr.New(apperr.KindInvalid, "INVALID_IP_ADDRESS", "A valid IPv4 address is required", nil)
	}
	if ip.IsLoopback() || ip.IsMulticast() {
		return apperr.New(
			apperr.KindInvalid,
			"INVALID_IP_ADDRESS",
			"Loopback and multicast addresses are not allowed",
			nil,
		)
	}

	maskIP := parseIPv4(cfg.SubnetMask)
	if maskIP == nil {
		return apperr.New(apperr.KindInvalid, "INVALID_SUBNET_MASK", "A valid IPv4 subnet mask is required", nil)
	}

	mask := net.IPv4Mask(maskIP[0], maskIP[1], maskIP[2], maskIP[3])
	ones, bits := mask.Size()
	if bits != 32 || ones == 0 {
		return apperr.New(apperr.KindInvalid, "INVALID_SUBNET_MASK", "Invalid IPv4 subnet mask", nil)
	}

	if strings.TrimSpace(cfg.Gateway) != "" {
		gwIP := parseIPv4(cfg.Gateway)
		if gwIP == nil {
			return apperr.New(apperr.KindInvalid, "INVALID_GATEWAY", "Gateway must be a valid IPv4 address", nil)
		}

		ipNet := net.IPNet{IP: ip.Mask(mask), Mask: mask}
		if !ipNet.Contains(gwIP) {
			return apperr.New(
				apperr.KindInvalid,
				"GATEWAY_OUT_OF_SUBNET",
				fmt.Sprintf("Gateway %s does not belong to subnet %s", cfg.Gateway, ipNet.String()),
				nil,
			)
		}
	}

	for _, dns := range cfg.DNS {
		if strings.TrimSpace(dns) == "" {
			continue
		}
		if parseIPv4(dns) == nil {
			return apperr.New(
				apperr.KindInvalid,
				"INVALID_DNS",
				fmt.Sprintf("Invalid DNS IPv4 address %q", dns),
				nil,
			)
		}
	}

	return nil
}

// computeTargetURL builds the URL the operator must open to confirm the trial configuration.
// The port of the incoming request wins; defaultPort covers requests that carry none.
func computeTargetURL(cfg InterfaceConfig, currentHost, defaultPort, token string) string {
	cleanHost := strings.TrimSpace(currentHost)

	port := defaultPort
	if _, requestPort, err := net.SplitHostPort(cleanHost); err == nil {
		port = requestPort
	}

	switch {
	case cfg.Mode == modeStatic && cfg.IPAddress != "":
		return settingsURL(hostWithPort(cfg.IPAddress, port), token)
	case cleanHost != "":
		// The request host already carries its own port when it is not the scheme default.
		return settingsURL(cleanHost, token)
	default:
		return settingsURL(hostWithPort("127.0.0.1", port), token)
	}
}

func settingsURL(host, token string) string {
	return fmt.Sprintf("http://%s/#settings?token=%s", host, token)
}

// portOf extracts the port from a host:port listen address, returning "" when absent.
func portOf(address string) string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return ""
	}
	return port
}

// hostWithPort joins host and port, omitting the separator when no port is known.
func hostWithPort(host, port string) string {
	if port == "" {
		return host
	}
	return host + ":" + port
}

// parseIPv4 returns the 4-byte form of value, or nil when value is not a valid IPv4 literal.
func parseIPv4(value string) net.IP {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return nil
	}
	return ip.To4()
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
