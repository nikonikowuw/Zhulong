package systemtime

import (
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// Time domain error catalog. Registering here keeps the shared platform
// dictionary free of business codes and makes every message the handler can
// return translatable; an unregistered code falls back to the handler's English
// message, which used to be the raw driver or SQLite error text.
func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"INVALID_TIME_CONFIG": {
			"en":      "Invalid time configuration",
			"zh-Hans": "时间配置无效",
			"zh-Hant": "時間設定無效",
		},
		"INVALID_TIMESTAMP": {
			"en":      "Target timestamp must be a valid ISO-8601 or RFC3339 timestamp",
			"zh-Hans": "目标时间必须是合法的 ISO-8601 或 RFC3339 时间格式",
			"zh-Hant": "目標時間必須是合法的 ISO-8601 或 RFC3339 時間格式",
		},
		"SYNC_IN_PROGRESS": {
			"en":      "NTP synchronization is already in progress",
			"zh-Hans": "已有一次 NTP 对时正在进行",
			"zh-Hant": "已有一次 NTP 對時正在進行",
		},
		"MANUAL_MODE_ACTIVE": {
			"en":      "Cannot perform NTP sync while in manual mode",
			"zh-Hans": "手动模式下无法执行 NTP 对时",
			"zh-Hant": "手動模式下無法執行 NTP 對時",
		},
		"NTP_SYNC_FAILED": {
			"en":      "NTP synchronization failed",
			"zh-Hans": "NTP 对时失败",
			"zh-Hant": "NTP 對時失敗",
		},
		"SET_TIME_FAILED": {
			"en":      "Failed to set system time",
			"zh-Hans": "设置系统时间失败",
			"zh-Hant": "設定系統時間失敗",
		},
	})
}
