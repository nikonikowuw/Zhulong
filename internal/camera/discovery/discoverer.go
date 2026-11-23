package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	// DefaultDiscoverTimeout 默认设备发现超时时间。
	DefaultDiscoverTimeout = 3 * time.Second

	// MaxDiscoverTimeout 设备发现最大超时时间。
	MaxDiscoverTimeout = 10 * time.Second

	// DefaultCacheTTL 探测结果在内存中的短缓存时效。
	DefaultCacheTTL = 5 * time.Second

	// udpReadBufferSize 单个 WS-Discovery UDP 报文最大缓冲区。
	udpReadBufferSize = 16 * 1024
)

// Discoverer 定义局域网设备发现服务接口。
type Discoverer interface {
	Discover(ctx context.Context, opts DiscoverOptions) ([]DiscoveredDevice, error)
}

// UDPDiscoverer 基于 UDP 组播与单播收集的 WS-Discovery 实现。
type UDPDiscoverer struct {
	logger        *zap.Logger
	targetAddr    string
	sf            singleflight.Group
	cacheMu       sync.RWMutex
	cacheTime     time.Time
	cachedDevices []DiscoveredDevice
}

// NewUDPDiscoverer 创建新的 WS-Discovery 探测服务实例。
func NewUDPDiscoverer(logger *zap.Logger) *UDPDiscoverer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &UDPDiscoverer{
		logger:     logger.Named("discovery"),
		targetAddr: WSDiscoveryMulticastAddress,
	}
}

// Discover 执行一次局域网设备探测。
func (d *UDPDiscoverer) Discover(ctx context.Context, opts DiscoverOptions) ([]DiscoveredDevice, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultDiscoverTimeout
	}
	if timeout > MaxDiscoverTimeout {
		timeout = MaxDiscoverTimeout
	}

	// 1. 检查短期内存缓存
	d.cacheMu.RLock()
	if !d.cacheTime.IsZero() && time.Since(d.cacheTime) < DefaultCacheTTL && len(d.cachedDevices) > 0 {
		cached := make([]DiscoveredDevice, len(d.cachedDevices))
		copy(cached, d.cachedDevices)
		d.cacheMu.RUnlock()
		d.logger.Debug("return cached discovered devices", zap.Int("count", len(cached)))
		return cached, nil
	}
	d.cacheMu.RUnlock()

	// 2. 利用 singleflight 避免密集并发触发网络广播风暴
	res, err, _ := d.sf.Do("discover", func() (any, error) {
		devices, probeErr := d.executeProbe(ctx, timeout, opts.Interfaces)
		if probeErr != nil {
			return nil, probeErr
		}

		d.cacheMu.Lock()
		d.cacheTime = time.Now()
		d.cachedDevices = devices
		d.cacheMu.Unlock()

		return devices, nil
	})

	if err != nil {
		return nil, err
	}

	devices, ok := res.([]DiscoveredDevice)
	if !ok {
		return []DiscoveredDevice{}, nil
	}

	return devices, nil
}

func (d *UDPDiscoverer) executeProbe(
	ctx context.Context,
	timeout time.Duration,
	targetIfaces []string,
) ([]DiscoveredDevice, error) {
	target := d.targetAddr
	if target == "" {
		target = WSDiscoveryMulticastAddress
	}

	dstAddr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return nil, fmt.Errorf("resolve target addr %q: %w", target, err)
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	probeData := BuildProbeMessage("")
	localAddrs := d.resolveOutboundAddresses(targetIfaces)

	// 按每个可用本地 IPv4 接口并发发起“同 socket 发包 + 收包”，确保 IPC 单播回包能命中正在监听的源端口。
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		collected = make(map[string]DiscoveredDevice)
	)

	mergeDevices := func(devs []DiscoveredDevice) {
		if len(devs) == 0 {
			return
		}
		mu.Lock()
		for _, dev := range devs {
			key := dev.ID
			if key == "" {
				key = dev.IP
			}
			if key == "" {
				continue
			}
			collected[key] = dev
		}
		mu.Unlock()
	}

	if len(localAddrs) == 0 {
		wg.Go(func() {
			conn, listenErr := net.ListenPacket("udp4", ":0")
			if listenErr != nil {
				d.logger.Warn("listen default udp4 socket failed", zap.Error(listenErr))
				return
			}
			defer conn.Close()
			mergeDevices(d.probeOnConn(probeCtx, conn, dstAddr, probeData, timeout))
		})
	} else {
		for _, localIP := range localAddrs {
			wg.Go(func() {
				srcAddr := &net.UDPAddr{IP: localIP, Port: 0}
				conn, listenErr := net.ListenUDP("udp4", srcAddr)
				if listenErr != nil {
					d.logger.Debug("bind local interface for multicast failed",
						zap.String("localIP", localIP.String()),
						zap.Error(listenErr),
					)
					return
				}
				defer conn.Close()
				mergeDevices(d.probeOnConn(probeCtx, conn, dstAddr, probeData, timeout))
			})
		}
	}

	wg.Wait()

	result := make([]DiscoveredDevice, 0, len(collected))
	for _, dev := range collected {
		result = append(result, dev)
	}

	d.logger.Info("ws-discovery probe completed",
		zap.Int("deviceCount", len(result)),
		zap.Duration("duration", timeout),
	)

	return result, nil
}

// probeOnConn 使用同一 UDP socket 发送 Probe 报文并接收单播 ProbeMatches 响应。
func (d *UDPDiscoverer) probeOnConn(
	ctx context.Context,
	conn net.PacketConn,
	dstAddr *net.UDPAddr,
	payload []byte,
	timeout time.Duration,
) []DiscoveredDevice {
	// 先设置初始 Deadline，再启动取消监听协程，避免已取消的 ctx 被后续 SetDeadline 覆盖。
	_ = conn.SetDeadline(time.Now().Add(timeout))

	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stopWatch:
		}
	}()

	if _, err := conn.WriteTo(payload, dstAddr); err != nil {
		d.logger.Debug("send ws-discovery probe failed",
			zap.String("localAddr", conn.LocalAddr().String()),
			zap.Error(err),
		)
		return nil
	}

	var devices []DiscoveredDevice
	buf := make([]byte, udpReadBufferSize)

	for {
		n, from, readErr := conn.ReadFrom(buf)
		if readErr != nil {
			var netErr net.Error
			if errors.As(readErr, &netErr) && netErr.Timeout() {
				break
			}
			if strings.Contains(readErr.Error(), "use of closed network connection") {
				break
			}
			d.logger.Debug("read ws-discovery response failed", zap.Error(readErr))
			break
		}

		matched, parseErr := ParseProbeMatches(buf[:n])
		if parseErr != nil {
			d.logger.Debug("parse probe matches failed",
				zap.String("from", from.String()),
				zap.Error(parseErr),
			)
			continue
		}
		devices = append(devices, matched...)
	}

	return devices
}

func (d *UDPDiscoverer) resolveOutboundAddresses(targetIfaces []string) []net.IP {
	var ips []net.IP

	filterMap := make(map[string]bool)
	for _, name := range targetIfaces {
		filterMap[strings.TrimSpace(name)] = true
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		d.logger.Warn("list network interfaces failed", zap.Error(err))
		return ips
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(filterMap) > 0 && !filterMap[iface.Name] {
			continue
		}

		addrs, addrErr := iface.Addrs()
		if addrErr != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() {
				continue
			}
			ips = append(ips, ip4)
		}
	}

	return ips
}
