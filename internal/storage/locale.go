package storage

import "github.com/nikonikowuw/Zhulong/internal/httputil"

// Storage domain error catalog. Every code the handler can return must be
// registered here, otherwise the response falls back to the English message.
func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"INVALID_STORAGE_CONFIG": {
			"en":      "Invalid storage configuration",
			"zh-Hans": "无效的存储策略配置",
			"zh-Hant": "無效的儲存策略配置",
		},
		"STORAGE_CLEANUP_IN_PROGRESS": {
			"en":      "Storage cleanup cycle is already in progress",
			"zh-Hans": "存储清理正在执行中",
			"zh-Hant": "儲存清理正在執行中",
		},
		"STORAGE_CLEANUP_FAILED": {
			"en":      "Storage cleanup failed",
			"zh-Hans": "存储清理执行失败",
			"zh-Hant": "儲存清理執行失敗",
		},
		"STORAGE_TELEMETRY_UNAVAILABLE": {
			"en":      "Storage telemetry is unavailable",
			"zh-Hans": "无法读取存储介质的容量信息",
			"zh-Hant": "無法讀取儲存介質的容量資訊",
		},
	})
}
