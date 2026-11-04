package spatial

import "github.com/nikonikowuw/Zhulong/internal/httputil"

// Business error codes of the spatial domain (error-handling.md §4.2).
//
// Each module owns its own catalog so the shared httputil dictionary stays a set
// of platform-wide codes. Without this registration every spatial error falls
// back to the generic REQUEST_FAILED text, which reads as "请求失败" no matter
// what actually went wrong.
func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"INVALID_MAP_NAME": {
			"en":      "Map name is required and must not exceed 64 characters",
			"zh-Hans": "底图名称不能为空且不能超过 64 个字符",
			"zh-Hant": "底圖名稱不能為空且不能超過 64 個字元",
		},
		"UNSUPPORTED_MAP_FORMAT": {
			"en":      "Only png, jpg, webp and svg base maps are supported",
			"zh-Hans": "底图仅支持 png、jpg、webp 或 svg 格式",
			"zh-Hant": "底圖僅支援 png、jpg、webp 或 svg 格式",
		},
		"IMAGE_SIZE_UNREADABLE": {
			"en":      "The intrinsic size of the uploaded image could not be determined",
			"zh-Hans": "无法识别上传图片的固有尺寸，请确认文件未损坏",
			"zh-Hant": "無法辨識上傳圖片的固有尺寸，請確認檔案未損壞",
		},
		"MAP_FILE_REQUIRED": {
			"en":      "A base map image file is required",
			"zh-Hans": "必须提供底图图片文件",
			"zh-Hant": "必須提供底圖圖片檔案",
		},
		"MAP_NOT_FOUND": {
			"en":      "Canvas map not found",
			"zh-Hans": "底图不存在",
			"zh-Hant": "底圖不存在",
		},
		"MAP_FILE_NOT_FOUND": {
			"en":      "The base map image file is missing on disk",
			"zh-Hans": "磁盘上未找到该底图文件",
			"zh-Hant": "磁碟上未找到該底圖檔案",
		},
		"MAP_STORAGE_ERROR": {
			"en":      "Base map storage failure",
			"zh-Hans": "底图存储服务异常",
			"zh-Hant": "底圖儲存服務異常",
		},
		"POSITIONS_REQUIRED": {
			"en":      "The positions field is required; send an empty array to unbind every camera",
			"zh-Hans": "必须提供 positions 字段；如需解绑全部摄像头请传空数组",
			"zh-Hant": "必須提供 positions 欄位；如需解除綁定全部攝影機請傳空陣列",
		},
		"DUPLICATE_CAMERA_POSITION": {
			"en":      "The same camera appears more than once in the batch",
			"zh-Hans": "同一摄像头在本次提交中出现多次",
			"zh-Hant": "同一攝影機在本次提交中出現多次",
		},
		"CAMERA_NOT_FOUND": {
			"en":      "The camera does not exist",
			"zh-Hans": "摄像头不存在",
			"zh-Hant": "攝影機不存在",
		},
		"PLATE_REQUIRED": {
			"en":      "A vehicle plate is required for a vehicle trajectory",
			"zh-Hans": "查询车辆轨迹必须提供车牌号",
			"zh-Hant": "查詢車輛軌跡必須提供車牌號",
		},
		"SPATIAL_PERSON_ID_REQUIRED": {
			"en":      "A person ID is required for a person trajectory",
			"zh-Hans": "查询人员轨迹必须选择人员",
			"zh-Hant": "查詢人員軌跡必須選擇人員",
		},
		"INVALID_TARGET_TYPE": {
			"en":      "targetType must be vehicle or person",
			"zh-Hans": "目标类型必须是车辆或人员",
			"zh-Hant": "目標類型必須是車輛或人員",
		},
		"INVALID_TIME_RANGE": {
			"en":      "endTime must be later than startTime",
			"zh-Hans": "结束时间必须晚于起始时间",
			"zh-Hant": "結束時間必須晚於起始時間",
		},
	})
}
