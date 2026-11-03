package openapi

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"PERSON_ID_REQUIRED": {
			"en":      "Person ID is required",
			"zh-Hans": "人员编号不能为空",
			"zh-Hant": "人員編號不能為空",
		},
		"PERSON_ID_TOO_LONG": {
			"en":      "Person ID exceeds maximum length of 64 characters",
			"zh-Hans": "人员编号长度不能超过 64 个字符",
			"zh-Hant": "人員編號長度不能超過 64 個字元",
		},
		"PERSON_ID_INVALID_CHARS": {
			"en":      "Person ID contains invalid characters or delimiters",
			"zh-Hans": "人员编号包含非法字符或路径分隔符",
			"zh-Hant": "人員編號包含非法字元或路徑分隔符",
		},
		"FACE_LIMIT_EXCEEDED": {
			"en":      "Maximum enrolled faces per person reached",
			"zh-Hans": "该人员底库人脸照片数量已达上限",
			"zh-Hant": "該人員底庫人臉照片數量已達上限",
		},
		"TOO_MANY_REQUESTS": {
			"en":      "Face upload concurrency limit reached, please retry later",
			"zh-Hans": "人脸特征提取并发已达上限，请稍后重试",
			"zh-Hant": "人臉特徵提取並發已達上限，請稍後重試",
		},
		"FACE_REPLACE_FAILED": {
			"en":      "Failed to replace face templates",
			"zh-Hans": "替换人员底库人脸失败",
			"zh-Hant": "替換人員底庫人臉失敗",
		},
		"FACE_CLEAR_FAILED": {
			"en":      "Failed to clear enrolled faces",
			"zh-Hans": "清空人员底库人脸失败",
			"zh-Hant": "清空人員底庫人臉失敗",
		},
	})
}
