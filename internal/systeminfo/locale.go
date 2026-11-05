package systeminfo

import "github.com/nikonikowuw/Zhulong/internal/httputil"

// Translations for the stable machine codes this package returns. The backend never
// ships localized text inline: httputil selects the language from Accept-Language and
// this catalog is the single place the wording lives.
func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		CodeIdentityInvalid: {
			"en":      "Device ID must be 1-64 characters of letters, digits, dot, underscore or hyphen",
			"zh-Hans": "设备 ID 只能包含字母、数字、点、下划线或连字符，长度为 1-64 个字符",
			"zh-Hant": "裝置 ID 只能包含字母、數字、點、底線或連字號，長度為 1-64 個字元",
		},
	})
}
