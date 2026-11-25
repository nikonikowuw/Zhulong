package alertpush

import "github.com/nikonikowuw/Zhulong/internal/httputil"

const (
	CodeAlertDestinationNotFound         = "ALERT_DESTINATION_NOT_FOUND"
	CodeAlertDestinationNameConflict     = "ALERT_DESTINATION_NAME_CONFLICT"
	CodeAlertDestinationClientIDConflict = "ALERT_DESTINATION_CLIENT_ID_CONFLICT"
	CodeAlertDestinationConfigInvalid    = "ALERT_DESTINATION_CONFIG_INVALID"
	CodeAlertMaxDestinationsReached      = "ALERT_DESTINATION_LIMIT_REACHED"
	CodeAlertImageModeUnsupported        = "ALERT_IMAGE_MODE_UNSUPPORTED"
	CodeAlertPublicBaseURLMissing        = "ALERT_PUBLIC_BASE_URL_MISSING"
	CodeAlertEventTypeInvalid            = "ALERT_DESTINATION_EVENT_TYPE_INVALID"
	CodeAlertTestRateLimited             = "ALERT_TEST_RATE_LIMITED"
	CodeMediaURLInvalid                  = "MEDIA_URL_INVALID"
	CodeMediaURLExpired                  = "MEDIA_URL_EXPIRED"
	CodeMediaURLRateLimited              = "MEDIA_URL_RATE_LIMITED"
	CodeMediaSignerUnavailable           = "MEDIA_SIGNER_UNAVAILABLE"

	// Field-level validation codes used inside VALIDATION_FAILED details[].
	CodeAlertCapabilityRequired    = "ALERT_CAPABILITY_REQUIRED"
	CodeAlertChannelTypeImmutable  = "ALERT_CHANNEL_TYPE_IMMUTABLE"
	CodeAlertURLBlocked            = "ALERT_URL_BLOCKED"
	CodeAlertURLUserinfoForbidden  = "ALERT_URL_USERINFO_FORBIDDEN"
	CodeAlertHeartbeatCrossOrigin  = "ALERT_HEARTBEAT_CROSS_ORIGIN"
	CodeAlertMQTTSchemeUnsupported = "ALERT_MQTT_SCHEME_UNSUPPORTED"
	CodeAlertMQTTWildcardForbidden = "ALERT_MQTT_WILDCARD_FORBIDDEN"
	CodeAlertHeaderReserved        = "ALERT_HEADER_RESERVED"
	CodeAlertHeaderInvalid         = "ALERT_HEADER_INVALID"
	CodeAlertHeaderDuplicate       = "ALERT_HEADER_DUPLICATE"
	CodeAlertHeaderLimitExceeded   = "ALERT_HEADER_LIMIT_EXCEEDED"
)

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		CodeAlertDestinationNotFound: {
			"en":      "Alert destination not found",
			"zh-Hans": "指定的告警推送目标不存在",
			"zh-Hant": "指定的告警推送目標不存在",
		},
		CodeAlertDestinationNameConflict: {
			"en":      "Alert destination name already exists",
			"zh-Hans": "告警推送目标名称已存在",
			"zh-Hant": "告警推送目標名稱已存在",
		},
		CodeAlertDestinationClientIDConflict: {
			"en":      "MQTT Client ID already exists",
			"zh-Hans": "MQTT ClientID 已被其他推送目标使用",
			"zh-Hant": "MQTT ClientID 已被其他推送目標使用",
		},
		CodeAlertDestinationConfigInvalid: {
			"en":      "Alert destination configuration is invalid",
			"zh-Hans": "告警推送目标配置无效",
			"zh-Hant": "告警推送目標設定無效",
		},
		CodeAlertMaxDestinationsReached: {
			"en":      "Maximum number of alert destinations reached",
			"zh-Hans": "告警推送目标数量已达上限",
			"zh-Hant": "告警推送目標數量已達上限",
		},
		CodeAlertImageModeUnsupported: {
			"en":      "Image mode is unsupported for the chosen channel type",
			"zh-Hans": "当前推送协议不支持所选的图片模式",
			"zh-Hant": "目前推送協定不支援所選的圖片模式",
		},
		CodeAlertPublicBaseURLMissing: {
			"en":      "Public base URL is not configured on the host",
			"zh-Hans": "宿主未配置对外基地址（public_base_url），无法使用 URL 图片模式",
			"zh-Hant": "宿主未設定對外基位址（public_base_url），無法使用 URL 圖片模式",
		},
		CodeAlertEventTypeInvalid: {
			"en":      "Filter event types must be alarm, face or vehicle",
			"zh-Hans": "过滤事件类型只能是 alarm、face 或 vehicle",
			"zh-Hant": "過濾事件類型只能是 alarm、face 或 vehicle",
		},
		CodeAlertTestRateLimited: {
			"en":      "Connectivity test rate limit exceeded, please retry later",
			"zh-Hans": "连通性测试过于频繁，请稍后重试",
			"zh-Hant": "連通性測試過於頻繁，請稍後重試",
		},
		CodeMediaURLInvalid: {
			"en":      "Signed media URL signature is invalid or tampered",
			"zh-Hans": "签名媒体 URL 签名无效或已被篡改",
			"zh-Hant": "簽名媒體 URL 簽名無效或已被篡改",
		},
		CodeMediaURLExpired: {
			"en":      "Signed media URL has expired",
			"zh-Hans": "签名媒体 URL 已过期",
			"zh-Hant": "簽名媒體 URL 已過期",
		},
		CodeMediaURLRateLimited: {
			"en":      "Signed media request rate limit exceeded",
			"zh-Hans": "签名媒体访问过于频繁，请稍后重试",
			"zh-Hant": "簽名媒體存取過於頻繁，請稍後重試",
		},
		CodeMediaSignerUnavailable: {
			"en":      "Signed media service is temporarily unavailable",
			"zh-Hans": "签名媒体服务暂不可用",
			"zh-Hant": "簽名媒體服務暫不可用",
		},
		CodeAlertCapabilityRequired: {
			"en":      "Enable at least one of event push or heartbeat reporting",
			"zh-Hans": "请至少开启感知事件推送或设备存活上报中的一项能力",
			"zh-Hant": "請至少開啟感知事件推送或裝置存活上報中的一項能力",
		},
		CodeAlertChannelTypeImmutable: {
			"en":      "Channel type cannot be changed after creation",
			"zh-Hans": "创建后不允许修改推送协议类型",
			"zh-Hant": "建立後不允許修改推送協定類型",
		},
		CodeAlertURLBlocked: {
			"en":      "Target host or IP address is blocked by outbound security policy",
			"zh-Hans": "目标主机或 IP 地址被出站安全策略拦截",
			"zh-Hant": "目標主機或 IP 位址被出站安全策略攔截",
		},
		CodeAlertURLUserinfoForbidden: {
			"en":      "URL must not contain embedded username or password credentials",
			"zh-Hans": "地址中不允许包含账号凭据（如 user:pass@）",
			"zh-Hant": "位址中不允許包含帳號憑證（如 user:pass@）",
		},
		CodeAlertHeartbeatCrossOrigin: {
			"en":      "Heartbeat URL must share the same origin as the primary URL when credentials or custom headers are configured",
			"zh-Hans": "配置了鉴权凭据、签名密钥或自定义标头时，心跳 URL 必须与主推送 URL 同源",
			"zh-Hant": "設定了驗證憑證、簽名密鑰或自訂標頭時，心跳 URL 必須與主推送 URL 同源",
		},
		CodeAlertMQTTSchemeUnsupported: {
			"en":      "MQTT broker scheme must be one of tcp, ssl, tls, tcps, ws, wss, mqtt, or mqtts",
			"zh-Hans": "MQTT Broker 协议仅支持 tcp、ssl、tls、tcps、ws、wss、mqtt 或 mqtts",
			"zh-Hant": "MQTT Broker 協定僅支援 tcp、ssl、tls、tcps、ws、wss、mqtt 或 mqtts",
		},
		CodeAlertMQTTWildcardForbidden: {
			"en":      "MQTT publish topic must not contain wildcards (+ or #)",
			"zh-Hans": "MQTT 发布主题不能包含通配符（+ 或 #）",
			"zh-Hant": "MQTT 發布主題不能包含萬用字元（+ 或 #）",
		},
		CodeAlertHeaderReserved: {
			"en":      "Custom headers must not override reserved system headers",
			"zh-Hans": "自定义标头不允许覆盖系统保留标头",
			"zh-Hant": "自訂標頭不允許覆蓋系統保留標頭",
		},
		CodeAlertHeaderInvalid: {
			"en":      "Custom header name or value contains invalid characters",
			"zh-Hans": "自定义标头名称或值格式无效",
			"zh-Hant": "自訂標頭名稱或值格式無效",
		},
		CodeAlertHeaderDuplicate: {
			"en":      "Duplicate custom header names are not allowed",
			"zh-Hans": "自定义标头名称存在重复（不区分大小写）",
			"zh-Hant": "自訂標頭名稱存在重複（不區分大小寫）",
		},
		CodeAlertHeaderLimitExceeded: {
			"en":      "Too many custom headers (maximum 32)",
			"zh-Hans": "自定义标头数量超过上限（最多 32 个）",
			"zh-Hant": "自訂標頭數量超過上限（最多 32 個）",
		},
	})
}
