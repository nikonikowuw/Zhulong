package task

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"TASK_NOT_FOUND": {
			"en":      "Analysis task not found",
			"zh-Hans": "视频分析任务不存在",
			"zh-Hant": "視訊分析任務不存在",
		},
		"TASK_ALREADY_EXISTS": {
			"en":      "Analysis task for this camera and algorithm already exists",
			"zh-Hans": "该摄像头和算法的分析任务已存在",
			"zh-Hant": "該攝影機與演算法的分析任務已存在",
		},
		"TASK_CREATE_FAILED": {
			"en":      "Failed to create analysis task",
			"zh-Hans": "创建分析任务失败",
			"zh-Hant": "建立分析任務失敗",
		},
		"TASK_UPDATE_FAILED": {
			"en":      "Failed to update analysis task",
			"zh-Hans": "更新分析任务失败",
			"zh-Hant": "更新分析任務失敗",
		},
		"TASK_DELETE_FAILED": {
			"en":      "Failed to delete analysis task",
			"zh-Hans": "删除分析任务失败",
			"zh-Hant": "刪除分析任務失敗",
		},
		"INVALID_TASK_CONFIG": {
			"en":      "Analysis task config does not match algorithm schema",
			"zh-Hans": "分析任务配置不符合算法参数规范",
			"zh-Hant": "分析任務設定不符合演算法參數規範",
		},
		"INVALID_REGION_CONFIG": {
			"en":      "Invalid region configuration",
			"zh-Hans": "无效的区域策略配置",
			"zh-Hant": "無效的區域策略設定",
		},
		"COMPUTE_BUDGET_EXCEEDED": {
			"en":      "Total compute budget (1,000 points) exceeded",
			"zh-Hans": "摄像头算法算力总配额超过 1000 点基准上限",
			"zh-Hant": "攝影機演算法算力總配額超過 1000 點基準上限",
		},
		"ALGORITHM_NOT_FOUND": {
			"en":      "Referenced algorithm not found",
			"zh-Hans": "关联的算法不存在",
			"zh-Hant": "關聯的演算法不存在",
		},
	})
}
