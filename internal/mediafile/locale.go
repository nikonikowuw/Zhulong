package mediafile

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"MEDIA_ROOT_UNAVAILABLE": {
			"en":      "Media root is unavailable",
			"zh-Hans": "媒体根目录不可用",
			"zh-Hant": "媒體根目錄無法使用",
		},
		"MEDIA_KIND_INVALID": {
			"en":      "Media path must start with a known media directory",
			"zh-Hans": "媒体路径必须以已知的媒体目录开头",
			"zh-Hant": "媒體路徑必須以已知的媒體目錄開頭",
		},
		"MEDIA_PATH_INVALID": {
			"en":      "Media path is invalid",
			"zh-Hans": "媒体路径无效",
			"zh-Hant": "媒體路徑無效",
		},
		"MEDIA_PATH_ESCAPE": {
			"en":      "Media path escapes the media root",
			"zh-Hans": "媒体路径超出媒体根目录",
			"zh-Hant": "媒體路徑超出媒體根目錄",
		},
		"MEDIA_ENTRY_NOT_FOUND": {
			"en":      "Media entry not found",
			"zh-Hans": "未找到媒体文件",
			"zh-Hant": "找不到媒體檔案",
		},
		"MEDIA_ENTRY_UNREADABLE": {
			"en":      "Media entry could not be read",
			"zh-Hans": "媒体文件无法读取",
			"zh-Hant": "媒體檔案無法讀取",
		},
		"MEDIA_NOT_A_FILE": {
			"en":      "Media path is not a file",
			"zh-Hans": "媒体路径不是文件",
			"zh-Hant": "媒體路徑不是檔案",
		},
	})
}
