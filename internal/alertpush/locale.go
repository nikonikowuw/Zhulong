package alertpush

import "github.com/nikonikowuw/Zhulong/internal/httputil"

const (
	CodeAlertDestinationNotFound      = "ALERT_DESTINATION_NOT_FOUND"
	CodeAlertDestinationNameConflict  = "ALERT_DESTINATION_NAME_CONFLICT"
	CodeAlertDestinationConfigInvalid = "ALERT_DESTINATION_CONFIG_INVALID"
	CodeAlertImageModeUnsupported     = "ALERT_IMAGE_MODE_UNSUPPORTED"
	CodeAlertPublicBaseURLMissing     = "ALERT_PUBLIC_BASE_URL_MISSING"
	CodeMediaURLInvalid               = "MEDIA_URL_INVALID"
	CodeMediaURLExpired               = "MEDIA_URL_EXPIRED"
)

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		CodeAlertDestinationNotFound: {
			"en":      "Alert destination not found",
			"zh-Hans": "指定的告警推送目标不存在",
			"zh-Hant": "指定的告警推送目標不存在",
		},
		CodeAlertDestinationNameConflict: {
			"en":      "Alert destination name or client ID already exists",
			"zh-Hans": "告警推送目标名称或 ClientID 已存在",
			"zh-Hant": "告警推送目標名稱或 ClientID 已存在",
		},
		CodeAlertDestinationConfigInvalid: {
			"en":      "Alert destination configuration is invalid",
			"zh-Hans": "告警推送目标配置无效",
			"zh-Hant": "告警推送目標設定無效",
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
	})
}
