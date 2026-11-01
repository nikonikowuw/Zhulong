package gb28181

import "github.com/nikonikowuw/Zhulong/internal/httputil"

// Translations for the stable machine codes this package returns. The backend
// never ships localized text inline: httputil selects the language from
// Accept-Language, and this catalog is the single place the wording lives.
func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		CodeConfigInvalid: {
			"en":      "GB28181 configuration is invalid",
			"zh-Hans": "GB28181 配置无效",
			"zh-Hant": "GB28181 設定無效",
		},
		CodePortRangeInvalid: {
			"en":      "RTP port range is invalid; it must hold at least one RTP/RTCP pair",
			"zh-Hans": "RTP 端口范围无效，至少需要容纳一对 RTP/RTCP 端口",
			"zh-Hant": "RTP 連接埠範圍無效，至少需要容納一對 RTP/RTCP 連接埠",
		},
		CodeAddressUnresolvable: {
			"en":      "Media advertised address cannot be determined; set it explicitly",
			"zh-Hans": "无法自动确定媒体宣告地址，请显式填写",
			"zh-Hant": "無法自動判斷媒體宣告位址，請明確填寫",
		},
		CodeAddressProbeFailed: {
			"en":      "Host network addresses cannot be enumerated",
			"zh-Hans": "无法枚举主机网络地址",
			"zh-Hant": "無法列舉主機網路位址",
		},
		CodeAddressInvalid: {
			"en":      "Media address must be a valid IPv4 address",
			"zh-Hans": "媒体地址必须是有效的 IPv4 地址",
			"zh-Hant": "媒體位址必須是有效的 IPv4 位址",
		},
		CodeDeviceNotFound: {
			"en":      "GB28181 device not found",
			"zh-Hans": "指定的 GB28181 设备不存在",
			"zh-Hant": "指定的 GB28181 裝置不存在",
		},
		CodeDeviceIDInvalid: {
			"en":      "Device identity must be exactly 20 ASCII digits",
			"zh-Hans": "设备编码必须是 20 位 ASCII 数字",
			"zh-Hant": "裝置編碼必須是 20 位 ASCII 數字",
		},
		CodeRegisteredDeviceRequired: {
			"en":      "Only pre-registered devices may register",
			"zh-Hans": "仅允许已预置设备注册",
			"zh-Hant": "僅允許已預置裝置註冊",
		},
		CodePasswordRequired: {
			"en":      "A Digest password is required for a new device",
			"zh-Hans": "新设备必须设置 Digest 密码",
			"zh-Hant": "新裝置必須設定 Digest 密碼",
		},
		CodeTransportInvalid: {
			"en":      "Device transport must be udp or tcp",
			"zh-Hans": "设备传输方式只能是 udp 或 tcp",
			"zh-Hant": "裝置傳輸方式只能是 udp 或 tcp",
		},
		CodeProfileUnsupported: {
			"en":      "Only the standard security profile is supported",
			"zh-Hans": "仅支持 standard 安全档位",
			"zh-Hant": "僅支援 standard 安全等級",
		},
		CodeRuntimeNotReady: {
			"en":      "GB28181 runtime is not ready",
			"zh-Hans": "GB28181 运行时尚未就绪",
			"zh-Hant": "GB28181 執行階段尚未就緒",
		},
		CodeDeviceOffline: {
			"en":      "GB28181 device is offline",
			"zh-Hans": "GB28181 设备当前离线",
			"zh-Hant": "GB28181 裝置目前離線",
		},
		CodeDeviceFlowClosed: {
			"en":      "The device signalling connection has been closed; wait for it to register again",
			"zh-Hans": "设备信令连接已断开，请等待其重新注册",
			"zh-Hant": "裝置信令連線已中斷，請等待其重新註冊",
		},
		CodeCatalogSyncFailed: {
			"en":      "Failed to synchronize device catalog",
			"zh-Hans": "同步设备目录失败",
			"zh-Hant": "同步裝置目錄失敗",
		},
		CodeCatalogTimeout: {
			"en":      "Catalog synchronization timed out",
			"zh-Hans": "目录同步超时",
			"zh-Hant": "目錄同步逾時",
		},
		CodeOutboundRateLimited: {
			"en":      "Too many platform requests to this device; retry later",
			"zh-Hans": "对该设备的下发过于频繁，请稍后重试",
			"zh-Hant": "對該裝置的下發過於頻繁，請稍後重試",
		},
		CodeDeviceChallenged: {
			"en":      "The device requires credentials for platform requests; this build does not authenticate outbound requests",
			"zh-Hans": "设备要求平台下发时携带凭据，当前版本不支持出站鉴权",
			"zh-Hant": "裝置要求平台下發時攜帶憑據，目前版本不支援出站鑑別",
		},
		CodeChannelNotFound: {
			"en":      "GB28181 channel not found",
			"zh-Hans": "指定的 GB28181 通道不存在",
			"zh-Hant": "指定的 GB28181 通道不存在",
		},
		CodeChannelIDInvalid: {
			"en":      "GB28181 channel ID must be 20 digits",
			"zh-Hans": "GB28181 通道编号必须是 20 位数字",
			"zh-Hant": "GB28181 通道編號必須是 20 位數字",
		},
		CodeChannelAlreadyBound: {
			"en":      "Channel has already been added as a camera",
			"zh-Hans": "该通道已添加为摄像机",
			"zh-Hant": "該通道已新增為攝影機",
		},
		CodeCameraBound: {
			"en":      "Device or channel has bound cameras; unbind or delete cameras first",
			"zh-Hans": "设备或通道仍有关联摄像机，请先解绑或删除摄像机",
			"zh-Hant": "裝置或通道仍有關聯攝影機，請先解除綁定或刪除攝影機",
		},
	})
}
