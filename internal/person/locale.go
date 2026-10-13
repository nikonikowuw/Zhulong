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
			"en":      "imagePath must be a clean absolute path without traversal segments",
			"zh-Hans": "登记照路径必须是不含路径穿越片段的绝对路径",
			"zh-Hant": "登記照路徑必須是不含路徑穿越片段的絕對路徑",
		},
	})
}
