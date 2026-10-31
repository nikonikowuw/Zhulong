package person

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"PERSON_NOT_FOUND": {
			"en":      "Person not found",
			"zh-Hans": "人员档案不存在",
			"zh-Hant": "人員檔案不存在",
		},
		"FACE_NOT_FOUND": {
			"en":      "Face template not found",
			"zh-Hans": "人脸特征模板不存在",
			"zh-Hant": "人臉特徵模板不存在",
		},
		"PERSON_CREATE_FAILED": {
			"en":      "Failed to create person",
			"zh-Hans": "创建人员档案失败",
			"zh-Hant": "建立人員檔案失敗",
		},
		"PERSON_ALREADY_EXISTS": {
			"en":      "Person already exists",
			"zh-Hans": "人员档案已存在",
			"zh-Hant": "人員檔案已存在",
		},
		"PERSON_ID_ALREADY_EXISTS": {
			"en":      "A person with this personId already exists",
			"zh-Hans": "该人员编号已被占用",
			"zh-Hant": "該人員編號已被佔用",
		},
		"PERSON_ID_IMMUTABLE": {
			"en":      "personId cannot be changed after creation",
			"zh-Hans": "人员编号创建后不可修改",
			"zh-Hant": "人員編號建立後不可修改",
		},
		"NAME_REQUIRED": {
			"en":      "Name is required",
			"zh-Hans": "姓名不能为空",
			"zh-Hant": "姓名不能為空",
		},
		"FACE_ADD_FAILED": {
			"en":      "Failed to add face template",
			"zh-Hans": "添加人脸模板失败",
			"zh-Hant": "添加人臉模板失敗",
		},
		"INVALID_IMAGE_PATH": {
			"en":      "imagePath must resolve inside the face storage directory",
			"zh-Hans": "登记照路径必须位于人脸存储目录内",
			"zh-Hant": "登記照路徑必須位於人臉儲存目錄內",
		},
		"FILE_REQUIRED": {
			"en":      "Image file is required",
			"zh-Hans": "必须提供图片文件",
			"zh-Hant": "必須提供圖片檔案",
		},
		"INVALID_FILE_TYPE": {
			"en":      "Only jpg, jpeg, png and webp images are allowed",
			"zh-Hans": "只允许上传 jpg、jpeg、png 或 webp 格式图片",
			"zh-Hant": "只允許上傳 jpg、jpeg、png 或 webp 格式圖片",
		},
		"FILE_TOO_LARGE": {
			"en":      "File size exceeds the 10MB limit",
			"zh-Hans": "图片大小超过 10MB 限制",
			"zh-Hant": "圖片大小超過 10MB 限制",
		},
		"IMAGE_NOT_FOUND": {
			"en":      "Face image file not found on disk",
			"zh-Hans": "磁盘上未找到人脸图片文件",
			"zh-Hant": "磁碟上未找到人臉圖片檔案",
		},
		"FILE_READ_FAILED": {
			"en":      "Failed to read uploaded file",
			"zh-Hans": "读取上传文件失败",
			"zh-Hant": "讀取上傳檔案失敗",
		},
		"STORAGE_ERROR": {
			"en":      "Storage service failure",
			"zh-Hans": "底库存储服务异常",
			"zh-Hant": "底庫儲存服務異常",
		},
		"NAME_TOO_LONG": {
			"en":      "Name is too long",
			"zh-Hans": "姓名长度超出限制",
			"zh-Hant": "姓名長度超出限制",
		},
		"FACE_ID_REQUIRED": {
			"en":      "Face ID is required",
			"zh-Hans": "人脸标识不能为空",
			"zh-Hant": "人臉識別碼不能為空",
		},
		"INVALID_FACE_OWNER": {
			"en":      "Face does not belong to this person",
			"zh-Hans": "该人脸不属于此人员",
			"zh-Hant": "該人臉不屬於此人員",
		},
		"FEATURE_DIM_MISMATCH": {
			"en":      "Face feature dimension mismatch",
			"zh-Hans": "人脸特征维度异常",
			"zh-Hant": "人臉特徵維度異常",
		},
		"FEATURE_EXTRACTOR_UNAVAILABLE": {
			"en":      "Face feature extraction is not configured",
			"zh-Hans": "人脸特征提取未配置",
			"zh-Hant": "人臉特徵提取未設定",
		},
		"PERSON_ID_REQUIRED_FOR_ENROLLMENT": {
			"en":      "Assign a personId before enrolling face photos",
			"zh-Hans": "请先为该人员绑定人员编号，再登记人脸照片",
			"zh-Hant": "請先為該人員綁定人員編號，再登記人臉照片",
		},
		"IMPORT_FILE_EMPTY": {
			"en":      "The person import file is empty",
			"zh-Hans": "人员导入文件为空",
			"zh-Hant": "人員匯入檔案為空",
		},
		"IMPORT_HEADER_INVALID": {
			"en":      "The person import header must contain name and personId",
			"zh-Hans": "人员导入表头必须包含 name 和 personId",
			"zh-Hant": "人員匯入表頭必須包含 name 和 personId",
		},
		"IMPORT_CSV_INVALID": {
			"en":      "The person import CSV is invalid",
			"zh-Hans": "人员导入 CSV 格式无效",
			"zh-Hant": "人員匯入 CSV 格式無效",
		},
		"IMPORT_FILE_TOO_LARGE": {
			"en":      "The person import file is too large",
			"zh-Hans": "人员导入文件超过 2 MB 限制",
			"zh-Hant": "人員匯入檔案超過 2 MB 限制",
		},
		"IMPORT_TOO_MANY_ROWS": {
			"en":      "The person import contains too many rows",
			"zh-Hans": "人员导入记录超过 1000 行限制",
			"zh-Hant": "人員匯入記錄超過 1000 行限制",
		},
		"PERSON_ID_TOO_LONG": {
			"en":      "personId is too long",
			"zh-Hans": "人员编号长度超出限制",
			"zh-Hant": "人員編號長度超出限制",
		},
		"IMPORT_COLUMN_COUNT": {
			"en":      "The number of columns does not match the header",
			"zh-Hans": "列数与表头不一致",
			"zh-Hant": "欄數與表頭不一致",
		},
		"IMPORT_DUPLICATE_PERSON_ID": {
			"en":      "personId is duplicated in the import file",
			"zh-Hans": "人员编号在导入文件中重复",
			"zh-Hant": "人員編號在匯入檔案中重複",
		},
		"PERSON_IMPORT_FAILED": {
			"en":      "Failed to import persons",
			"zh-Hans": "批量导入人员失败",
			"zh-Hant": "批量匯入人員失敗",
		},
		"MULTIPLE_FACES_DETECTED": {
			"en":      "Multiple faces detected; group photos are rejected",
			"zh-Hans": "检测到多张人脸（合影照拒绝录入）",
			"zh-Hant": "檢測到多張人臉（合影照拒絕錄入）",
		},
		"CORRUPT_OR_OVERSIZED_IMAGE": {
			"en":      "Image is corrupted or exceeds 4096px dimension limit",
			"zh-Hans": "图片损坏或单边尺寸超出 4096 像素限制",
			"zh-Hant": "圖片損壞或單邊尺寸超出 4096 像素限制",
		},
		"FACE_TOO_SMALL": {
			"en":      "Detected face is too small (less than 80px)",
			"zh-Hans": "检出人脸尺寸过小（短边小于 80 像素）",
			"zh-Hant": "檢出人臉尺寸過小（短邊小於 80 像素）",
		},
		"FACE_POSE_EXCEED": {
			"en":      "Face pose angle exceeds threshold",
			"zh-Hans": "人脸偏角过大（侧脸角度超限）",
			"zh-Hant": "人臉偏角過大（側臉角度超限）",
		},
		"PERSON_NAME_MISMATCH": {
			"en":      "Person ID already exists but the name does not match",
			"zh-Hans": "该人员编号已存在但姓名不一致",
			"zh-Hant": "該人員編號已存在但姓名不一致",
		},
		"ZIP_SLIP_ATTEMPT": {
			"en":      "Illegal file path escaping archive destination",
			"zh-Hans": "压缩包内包含非法路径逃逸文件",
			"zh-Hant": "壓縮包內包含非法路徑逃逸檔案",
		},
		"ZIP_BOMB_DETECTED": {
			"en":      "Extracted contents exceed maximum allowed decompression limit",
			"zh-Hans": "解压内容超出安全容量上限（疑似解压炸弹）",
			"zh-Hant": "解壓內容超出安全容量上限（疑似解壓炸彈）",
		},
		"ZIP_TOO_MANY_FILES": {
			"en":      "Archive contains too many files",
			"zh-Hans": "压缩包内文件数量超出上限",
			"zh-Hant": "壓縮包內檔案數量超出上限",
		},
		"ZIP_READ_FAILED": {
			"en":      "Failed to read or unpack zip archive",
			"zh-Hans": "解压读取压缩包失败",
			"zh-Hant": "解壓讀取壓縮包失敗",
		},
		"FACE_IMPORT_JOB_NOT_FOUND": {
			"en":      "Face import job not found",
			"zh-Hans": "人脸批量导入任务不存在",
			"zh-Hant": "人臉批量匯入任務不存在",
		},
		"FACE_LIMIT_EXCEEDED": {
			"en":      "Face count limit exceeded for this person",
			"zh-Hans": "该人员底库人脸照片数量已达上限",
			"zh-Hant": "該人員底庫人臉照片數量已達上限",
		},
	})
}
