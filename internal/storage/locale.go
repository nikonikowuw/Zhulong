package storage

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"INVALID_STORAGE_CONFIG": {
			"en":      "Invalid storage configuration",
			"zh-Hans": "无效的存储策略配置",
			"zh-Hant": "無效的儲存策略配置",
		},
		"STORAGE_PATH_INVALID": {
			"en":      "Storage path is invalid or not writable",
			"zh-Hans": "存储路径无效或无法写入",
			"zh-Hant": "儲存路徑無效或無法寫入",
		},
		"STORAGE_CLEANUP_IN_PROGRESS": {
			"en":      "Storage cleanup cycle is already in progress",
			"zh-Hans": "存储清理正在执行中",
			"zh-Hant": "儲存清理正在執行中",
		},
	})
}
