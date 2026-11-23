package discovery

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDiscovererTimeout(t *testing.T) {
	d := NewUDPDiscoverer(zap.NewNop())
	d.targetAddr = "127.0.0.1:19999"

	start := time.Now()
	devices, err := d.Discover(context.Background(), DiscoverOptions{
		Timeout:    50 * time.Millisecond,
		Interfaces: []string{"lo"},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error on timeout, got %v", err)
	}
	if elapsed < 40*time.Millisecond || elapsed > 1*time.Second {
		t.Errorf("expected elapsed time around 50ms, got %v", elapsed)
	}
	_ = devices
}

func TestDiscovererContextCancel(t *testing.T) {
	d := NewUDPDiscoverer(zap.NewNop())
	d.targetAddr = "127.0.0.1:19999"

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	_, _ = d.Discover(ctx, DiscoverOptions{
		Timeout:    2 * time.Second,
		Interfaces: []string{"lo"},
	})
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("expected prompt return on context cancel within 200ms, took %v", elapsed)
	}
}

func TestDiscovererPreCancelledContext(t *testing.T) {
	d := NewUDPDiscoverer(zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := d.Discover(ctx, DiscoverOptions{
		Timeout: 5 * time.Second,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error on pre-cancelled ctx, got %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("expected prompt return on pre-cancelled context within 100ms, took %v", elapsed)
	}
}

func TestDiscovererSingleflightAndCache(t *testing.T) {
	d := NewUDPDiscoverer(zap.NewNop())

	// 手动填充模拟短缓存
	d.cacheMu.Lock()
	d.cacheTime = time.Now()
	d.cachedDevices = []DiscoveredDevice{
		{
			ID:   "cached-uuid-001",
			IP:   "192.168.1.200",
			Name: "CachedCamera",
		},
	}
	d.cacheMu.Unlock()

	// 1. 立即再次调用，应直接命中缓存
	res, err := d.Discover(context.Background(), DiscoverOptions{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].ID != "cached-uuid-001" {
		t.Fatalf("expected cached device, got: %v", res)
	}

	// 2. 并发调用 singleflight 测试
	var wg sync.WaitGroup
	var err1, err2 error
	var res1, res2 []DiscoveredDevice

	wg.Add(2)
	go func() {
		defer wg.Done()
		res1, err1 = d.Discover(context.Background(), DiscoverOptions{Timeout: 50 * time.Millisecond})
	}()
	go func() {
		defer wg.Done()
		res2, err2 = d.Discover(context.Background(), DiscoverOptions{Timeout: 50 * time.Millisecond})
	}()
	wg.Wait()

	if err1 != nil || err2 != nil {
		t.Fatalf("concurrent discover failed: err1=%v, err2=%v", err1, err2)
	}
	if len(res1) != 1 || len(res2) != 1 {
		t.Errorf("expected both calls to receive cached result")
	}
}

func TestDiscovererMockProbeResponse(t *testing.T) {
	// 启动一个本地 UDP 模拟服务，监听请求并回复 ProbeMatches
	serverConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on udp4: %v", err)
	}
	defer serverConn.Close()

	serverAddr := serverConn.LocalAddr().String()

	stopServer := make(chan struct{})
	defer close(stopServer)

	go func() {
		buf := make([]byte, 2048)
		for {
			_ = serverConn.SetDeadline(time.Now().Add(500 * time.Millisecond))
			n, peer, readErr := serverConn.ReadFrom(buf)
			if readErr != nil {
				select {
				case <-stopServer:
					return
				default:
					continue
				}
			}

			payload := string(buf[:n])
			if strings.Contains(payload, "Probe") {
				resp := []byte(`<?xml version="1.0" encoding="utf-8"?>
<Envelope xmlns="http://www.w3.org/2003/05/soap-envelope"
          xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
          xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
          xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <Body>
    <d:ProbeMatches>
      <d:ProbeMatch>
        <wsa:EndpointReference>
          <wsa:Address>uuid:mock-camera-uuid-999</wsa:Address>
        </wsa:EndpointReference>
        <d:Types>dn:NetworkVideoTransmitter</d:Types>
        <d:Scopes>onvif://www.onvif.org/manufacturer/Uniview onvif://www.onvif.org/name/MockUniviewCamera</d:Scopes>
        <d:XAddrs>http://127.0.0.1:8888/onvif/device_service</d:XAddrs>
      </d:ProbeMatch>
    </d:ProbeMatches>
  </Body>
</Envelope>`)
				_, _ = serverConn.WriteTo(resp, peer)
			}
		}
	}()

	// 通过 UDPDiscoverer 真实执行 Discover 链路，验证发送 Probe 与接收单播回包同 socket 源端口一致
	d := NewUDPDiscoverer(zap.NewNop())
	d.targetAddr = serverAddr

	devices, err := d.Discover(context.Background(), DiscoverOptions{
		Timeout:    250 * time.Millisecond,
		Interfaces: []string{"lo"},
	})
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(devices) != 1 || devices[0].ID != "mock-camera-uuid-999" {
		t.Fatalf("expected parsed mock device via Discover, got: %+v", devices)
	}
	if devices[0].Manufacturer != "Uniview" {
		t.Errorf("expected Uniview manufacturer, got: %s", devices[0].Manufacturer)
	}
}
