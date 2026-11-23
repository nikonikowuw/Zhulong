// Package discovery 提供基于 ONVIF WS-Discovery 标准的局域网摄像机设备发现功能。
package discovery

import (
	"fmt"
	"strings"
	"time"
)

// DiscoveredDevice 代表通过 WS-Discovery 探测到的局域网摄像机设备。
type DiscoveredDevice struct {
	ID               string   `json:"id"`
	IP               string   `json:"ip"`
	Port             int      `json:"port"`
	XAddr            string   `json:"xAddr"`
	Name             string   `json:"name"`
	Manufacturer     string   `json:"manufacturer"`
	Model            string   `json:"model"`
	Hardware         string   `json:"hardware"`
	Types            []string `json:"types"`
	Scopes           []string `json:"scopes,omitempty"`
	SuggestedRtspUrl string   `json:"suggestedRtspUrl"`
}

// DiscoverOptions 定义局域网探测的控制参数。
type DiscoverOptions struct {
	Timeout    time.Duration
	Interfaces []string
}

// DeriveSuggestedRtspUrl 根据厂商和 IP 给出推荐的 RTSP 模板路径。
func DeriveSuggestedRtspUrl(manufacturer, ip string) string {
	lower := strings.ToLower(manufacturer)
	switch {
	case strings.Contains(lower, "hikvision") || strings.Contains(lower, "hik"):
		return fmt.Sprintf("rtsp://admin:password@%s:554/Streaming/Channels/101", ip)
	case strings.Contains(lower, "dahua"):
		return fmt.Sprintf("rtsp://admin:password@%s:554/cam/realmonitor?channel=1&subtype=0", ip)
	case strings.Contains(lower, "uniview"):
		return fmt.Sprintf("rtsp://admin:password@%s:554/unicast/c1/s0/live", ip)
	case strings.Contains(lower, "tp-link") || strings.Contains(lower, "tplink"):
		return fmt.Sprintf("rtsp://admin:password@%s:554/stream1", ip)
	default:
		return fmt.Sprintf("rtsp://admin:password@%s:554/live/ch0", ip)
	}
}
