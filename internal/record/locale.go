package record

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"RECOGNITION_NOT_FOUND": {
			"en":      "Recognition record not found",
			"zh-Hans": "识别记录不存在",
			"zh-Hant": "識別記錄不存在",
		},
		"ALARM_NOT_FOUND": {
			"en":      "Alarm record not found",
			"zh-Hans": "告警记录不存在",
			"zh-Hant": "告警記錄不存在",
		},
		"CAPTURE_NOT_FOUND": {
			"en":      "Capture record not found",
			"zh-Hans": "抓拍记录不存在",
			"zh-Hant": "抓拍記錄不存在",
		},
		"INVALID_TIME_FORMAT": {
			"en":      "Invalid time format, must be RFC3339",
			"zh-Hans": "时间格式无效，必须为 RFC3339 格式",
			"zh-Hant": "時間格式無效，必須為 RFC3339 格式",
		},
	})
}
