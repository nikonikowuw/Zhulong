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
		"STORAGE_MIGRATION_IN_PROGRESS": {
			"en":      "A storage migration job is already running",
			"zh-Hans": "已有历史媒体迁移任务正在执行",
			"zh-Hant": "已有歷史媒體遷移任務正在執行",
		},
		"STORAGE_MIGRATION_REQUIRES_S3": {
			"en":      "Switch to S3 object storage before migrating local media assets",
			"zh-Hans": "请先切换并启用 S3 对象存储后再执行历史资产迁移",
			"zh-Hant": "請先切換並啟用 S3 物件儲存後再執行歷史資產遷移",
		},
		"STORAGE_MIGRATION_UNAVAILABLE": {
			"en":      "Storage migration service is currently unavailable",
			"zh-Hans": "存储迁移后台服务暂不可用",
			"zh-Hant": "儲存遷移後台服務暫不可用",
		},
		"STORAGE_MIGRATION_FAILED": {
			"en":      "Storage migration failed",
			"zh-Hans": "历史媒体迁移任务执行失败",
			"zh-Hant": "歷史媒體遷移任務執行失敗",
		},
	})
}
