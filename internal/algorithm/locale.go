package algorithm

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"ALGO_PLATFORM_MISMATCH": {
			"en":      "Algorithm platform does not match host platform",
			"zh-Hans": "算法运行架构与当前宿主系统不兼容",
			"zh-Hant": "演算法執行架構與當前宿主系統不相容",
		},
		"FILE_TOO_LARGE": {
			"en":      "Uploaded algorithm archive exceeds the size limit",
			"zh-Hans": "上传的算法包超过大小限制",
			"zh-Hant": "上傳的演算法包超過大小限制",
		},
		"INVALID_FILE": {
			"en":      "Uploaded file is missing or unreadable",
			"zh-Hans": "上传文件缺失或无法读取",
			"zh-Hant": "上傳檔案缺失或無法讀取",
		},
		"ALGORITHM_VERSION_EXISTS": {
			"en":      "This algorithm version is already installed",
			"zh-Hans": "该算法版本已安装，不能覆盖",
			"zh-Hant": "此演算法版本已安裝，不能覆蓋",
		},
		"ALGORITHM_NOT_IN_CRASH_LOOP": {
			"en":      "Algorithm is not in CRASH_LOOP state",
			"zh-Hans": "算法当前不处于 CRASH_LOOP 状态",
			"zh-Hant": "演算法目前不處於 CRASH_LOOP 狀態",
		},
		"ALGORITHM_IN_CRASH_LOOP": {
			"en":      "Algorithm is in CRASH_LOOP, please reset it before enabling",
			"zh-Hans": "算法当前处于熔断状态 (CRASH_LOOP)，请先重置后再启用",
			"zh-Hant": "演算法目前處於熔斷狀態 (CRASH_LOOP)，請先重置後再啟用",
		},
		"UNSUPPORTED_ARCHIVE_FORMAT": {
			"en":      "Unsupported archive format, must be .tar.gz or .zip",
			"zh-Hans": "不支持的归档格式，必须为 .tar.gz 或 .zip",
			"zh-Hant": "不支援的封存格式，必須為 .tar.gz 或 .zip",
		},
		"ZIP_SLIP_ATTACK_DETECTED": {
			"en":      "Path traversal attack detected in uploaded archive",
			"zh-Hans": "检测到非法的路径穿越文件条目",
			"zh-Hant": "檢測到非法的路徑穿越檔案條目",
		},
		"DECOMPRESSION_BOMB_DETECTED": {
			"en":      "Archive decompressed size or file count exceeds safe limits",
			"zh-Hans": "解压后体积或文件数超过安全上限",
			"zh-Hant": "解壓縮後體積或檔案數超過安全上限",
		},
		"ALGO_VERIFICATION_FAILED": {
			"en":      "Algorithm black-box verification failed",
			"zh-Hans": "算法黑盒动态自测失败",
			"zh-Hant": "演算法黑盒動態自測失敗",
		},
		"ALGO_INSTALL_FAILED": {
			"en":      "Algorithm installation failed",
			"zh-Hans": "算法包安装失败",
			"zh-Hant": "演算法套件安裝失敗",
		},
		"INVALID_MANIFEST_TOKEN": {
			"en":      "Manifest identity field contains illegal characters",
			"zh-Hans": "算法清单标识字段包含非法字符",
			"zh-Hant": "演算法清單標識欄位包含非法字元",
		},
		"ALGORITHM_NOT_FOUND": {
			"en":      "Algorithm not found",
			"zh-Hans": "算法不存在",
			"zh-Hant": "演算法不存在",
		},
		"COMPUTE_BUDGET_EXCEEDED": {
			"en":      "Total compute budget (1,000 points) exceeded",
			"zh-Hans": "摄像头算法算力总配额超过 1000 点基准上限",
			"zh-Hant": "攝影機演算法算力總配額超過 1000 點基準上限",
		},
		"ALGORITHM_IS_ACTIVE": {
			"en":      "Algorithm is currently active, please disable it first",
			"zh-Hans": "算法当前正在运行中，请先停用后再操作",
			"zh-Hant": "演算法當前正在執行中，請先停用後再操作",
		},
		"TASK_NOT_FOUND": {
			"en":      "Analysis task not found",
			"zh-Hans": "视频分析任务不存在",
			"zh-Hant": "視訊分析任務不存在",
		},
		"TASK_CREATE_FAILED": {
			"en":      "Failed to create analysis task",
			"zh-Hans": "创建分析任务失败",
			"zh-Hant": "建立分析任務失敗",
		},
		"INVALID_REQUEST": {
			"en":      "Invalid analysis task request payload",
			"zh-Hans": "分析任务请求内容无效",
			"zh-Hant": "分析任務請求內容無效",
		},
	})
}
