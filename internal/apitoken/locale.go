package apitoken

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"UNAUTHORIZED": {
			"en":      "Invalid or missing Bearer token",
			"zh-Hans": "无效或缺失的 Bearer 访问令牌",
			"zh-Hant": "無效或缺失的 Bearer 訪問令牌",
		},
		"API_TOKEN_NOT_FOUND": {
			"en":      "API token not found",
			"zh-Hans": "API 访问令牌不存在",
			"zh-Hant": "API 訪問令牌不存在",
		},
		"API_TOKEN_LIMIT_REACHED": {
			"en":      "Active API token limit reached (maximum 10)",
			"zh-Hans": "已达到生效 API 令牌上限（最多 10 个）",
			"zh-Hant": "已達到生效 API 令牌上限（最多 10 個）",
		},
		"API_TOKEN_NAME_ALREADY_EXISTS": {
			"en":      "API token name already exists (including revoked tokens)",
			"zh-Hans": "API 令牌名称已被占用（含已吊销令牌）",
			"zh-Hant": "API 令牌名稱已被佔用（含已吊銷令牌）",
		},
		"API_TOKEN_NAME_REQUIRED": {
			"en":      "Token name is required",
			"zh-Hans": "令牌名称不能为空",
			"zh-Hant": "令牌名稱不能為空",
		},
		"API_TOKEN_NAME_TOO_LONG": {
			"en":      "Token name cannot exceed 48 characters",
			"zh-Hans": "令牌名称不能超过 48 个字符",
			"zh-Hant": "令牌名稱不能超過 48 個字符",
		},
		"API_TOKEN_NAME_INVALID": {
			"en":      "Token name contains invalid characters",
			"zh-Hans": "令牌名称包含非法控制字符",
			"zh-Hant": "令牌名稱包含非法控制字符",
		},
	})
}
