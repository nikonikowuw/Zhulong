//go:build linux

package systeminfo

import "strings"

// virtualNetworkPrefixes lists interface name prefixes that are kernel or
// container plumbing rather than physical links.
//
// 遥测页展示的是「主机的物理链路吞吐」，而 docker0 / br-<id> / vethXXX 的字节数
// 是主机与容器之间的内部搬运 —— 把它们算进汇总吞吐会重复计数，也会让运维在
// 「网络接口」表里看到一串自己没配过的设备名。
//
// 刻意**不**列入本表的（属于运维真正需要看见的链路，隐藏它们会掩盖故障）：
//   - tun / tap / wg / tailscale：VPN 与隧道通道
//   - bond*：物理口聚合，业务就是走它
//   - br0 / bridge0：手工搭的业务桥接（注意与 Docker 的 br-<hash> 区分）
//
// 语义与 internal/network/reader_linux.go 的 virtualInterfacePrefixes 同源，但**刻意不同**：
// 配置路径只让人改物理网卡的 IP，所以那里连 tun/tap/tailscale 一并隐藏；
// 遥测路径要反映「谁在真正跑流量」，因此只剔除容器/内核 plumbing。
// 两个包分属不同的依赖方向（systeminfo 是底层采集，不能反向依赖 network 的业务包），
// 所以各自持有清单 —— 调整其中一份前请先想清楚是否属于同一场景。
var virtualNetworkPrefixes = []string{
	"docker",  // docker0：默认 bridge
	"br-",     // br-<12 hex>：用户自定义 docker bridge
	"veth",    // 容器侧的虚拟以太网对
	"virbr",   // libvirt 默认 NAT bridge
	"vmnet",   // VMware 虚拟交换机
	"cilium_", // Cilium 端点与主机设备
	"flannel", // Flannel overlay
	"cali",    // Calico 端点
	"cni",     // CNI 通用前缀
	"kube-",   // kube-ipvs0 等
	"dummy",   // 纯占位设备
	"ifb",     // 流量整形中间设备
	"vxlan",   // overlay 隧道端点
	"genev",   // Geneve overlay 隧道端点
}

// isVirtualNetworkInterface reports whether name is container/kernel plumbing
// rather than a physical link.
func isVirtualNetworkInterface(name string) bool {
	if name == "lo" {
		return true
	}

	for _, prefix := range virtualNetworkPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}
