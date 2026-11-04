package openapi

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	// Only codes that originate in this package belong here. The person-domain codes
	// this API happens to surface are registered by internal/person: RegisterMessages
	// is global and the last registration wins, so a second copy would silently
	// override the owner's wording.
	httputil.RegisterMessages(map[string]map[string]string{
		"TOO_MANY_REQUESTS": {
			"en":      "Face upload concurrency limit reached, please retry later",
			"zh-Hans": "人脸特征提取并发已达上限，请稍后重试",
			"zh-Hant": "人臉特徵提取並發已達上限，請稍後重試",
		},
	})
}
