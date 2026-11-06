//go:build linux

package systeminfo

import "testing"

func TestIsVirtualNetworkInterface(t *testing.T) {
	tests := []struct {
		name    string
		iface   string
		virtual bool
		reason  string
	}{
		// 容器 / 内核 plumbing —— 必须隐藏
		{name: "loopback", iface: "lo", virtual: true, reason: "回环没有硬件 backing"},
		{name: "docker default bridge", iface: "docker0", virtual: true, reason: "Docker 默认 bridge"},
		{name: "docker user bridge", iface: "br-fc44a59a40ac", virtual: true,
			reason: "用户自定义 bridge 是 br-<12 hex>"},
		{name: "veth pair", iface: "veth1a2b3c4", virtual: true, reason: "容器侧虚拟以太网对"},
		{name: "libvirt bridge", iface: "virbr0", virtual: true, reason: "libvirt NAT bridge"},
		{name: "calico endpoint", iface: "cali9f2c1d3e4f5", virtual: true, reason: "Calico 端点"},
		{name: "cilium host", iface: "cilium_host", virtual: true, reason: "Cilium 主机设备"},
		{name: "dummy", iface: "dummy0", virtual: true, reason: "纯占位设备"},
		{name: "vxlan", iface: "vxlan.calico", virtual: true, reason: "overlay 隧道端点"},

		// 真实业务链路 —— 必须保留
		{name: "ethernet", iface: "eth0", virtual: false, reason: "物理网口"},
		{name: "ethernet alt", iface: "enp3s0", virtual: false, reason: "PCI 物理网口"},
		{name: "wireless", iface: "wlan0", virtual: false, reason: "无线网卡"},
		{name: "wireless alt", iface: "wlp2s0", virtual: false, reason: "PCI 无线网卡"},
		{name: "bond", iface: "bond0", virtual: false, reason: "物理口聚合是业务链路"},
		{name: "manual bridge", iface: "br0", virtual: false,
			reason: "手工业务桥接，与 br-<hash> 不同"},
		{name: "vpn tun", iface: "tun0", virtual: false, reason: "VPN 隧道，运维需要看见"},
		{name: "wireguard", iface: "wg0", virtual: false, reason: "WireGuard 隧道，运维需要看见"},
		{name: "cellular", iface: "wwan0", virtual: false, reason: "4G/5G 模组"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVirtualNetworkInterface(tt.iface); got != tt.virtual {
				t.Fatalf("isVirtualNetworkInterface(%q) = %v, want %v (%s)",
					tt.iface, got, tt.virtual, tt.reason)
			}
		})
	}
}
