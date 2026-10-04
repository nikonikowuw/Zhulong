package httputil

import (
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
)

func setLanguageHeaders(c *gin.Context, locale string) {
	c.Header("Content-Language", locale)
	c.Writer.Header().Add("Vary", "Accept-Language")
}

var (
	preferredLanguages = []language.Tag{
		language.English,
		language.SimplifiedChinese,
		language.TraditionalChinese,
	}
	languageMatcher = language.NewMatcher(preferredLanguages)

	messagesMu sync.RWMutex
	messages   = map[string]map[string]string{
		"OK": {
			"en":      "Success",
			"zh-Hans": "成功",
			"zh-Hant": "成功",
		},
		"INTERNAL_ERROR": {
			"en":      "Internal server error",
			"zh-Hans": "服务器内部错误",
			"zh-Hant": "伺服器內部錯誤",
		},
		"REQUEST_FAILED": {
			"en":      "The request could not be completed",
			"zh-Hans": "无法完成请求",
			"zh-Hant": "無法完成此請求",
		},
		"ROUTE_NOT_FOUND": {
			"en":      "Route not found",
			"zh-Hans": "未找到请求路由",
			"zh-Hant": "找不到請求路由",
		},
		"SERVICE_UNAVAILABLE": {
			"en":      "Service is not ready",
			"zh-Hans": "服务尚未就绪",
			"zh-Hant": "服務尚未就緒",
		},
		"VALIDATION_FAILED": {
			"en":      "Request validation failed",
			"zh-Hans": "请求参数校验失败",
			"zh-Hant": "請求參數驗證失敗",
		},
		"REQUIRED": {
			"en":      "This field is required",
			"zh-Hans": "此字段为必填项",
			"zh-Hant": "此欄位為必填項",
		},
		"MIN_VALUE": {
			"en":      "Value is below the allowed minimum",
			"zh-Hans": "数值低于允许的最小值",
			"zh-Hant": "數值低於允許的最小值",
		},
		"MAX_VALUE": {
			"en":      "Value exceeds the allowed maximum",
			"zh-Hans": "数值超过允许的最大值",
			"zh-Hant": "數值超過允許的最大值",
		},
		"MIN_LENGTH": {
			"en":      "Value is shorter than the minimum length",
			"zh-Hans": "长度小于允许的最小值",
			"zh-Hant": "長度小於允許的最小值",
		},
		"MAX_LENGTH": {
			"en":      "Value exceeds the maximum length",
			"zh-Hans": "长度超过允许的最大值",
			"zh-Hant": "長度超過允許的最大值",
		},
		"INVALID_FORMAT": {
			"en":      "Value has an invalid format",
			"zh-Hans": "格式无效",
			"zh-Hant": "格式無效",
		},
		"PAYLOAD_TOO_LARGE": {
			"en":      "Request body is too large",
			"zh-Hans": "请求内容过大",
			"zh-Hant": "請求內容過大",
		},
		"INVALID_URL": {
			"en":      "Enter a valid URL",
			"zh-Hans": "请输入有效的 URL",
			"zh-Hant": "請輸入有效的 URL",
		},
		"INVALID_VALUE": {
			"en":      "The value is invalid",
			"zh-Hans": "输入值无效",
			"zh-Hant": "輸入值無效",
		},
	}
)

// RegisterMessages registers domain-specific or custom error code translations in a thread-safe manner.
func RegisterMessages(custom map[string]map[string]string) {
	messagesMu.Lock()
	defer messagesMu.Unlock()
	for code, translations := range custom {
		if _, ok := messages[code]; !ok {
			messages[code] = make(map[string]string, len(translations))
		}
		for lang, text := range translations {
			messages[code][lang] = text
		}
	}
}

func requestLocale(acceptLanguage string) string {
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return "en"
	}

	_, index, _ := languageMatcher.Match(tags...)
	switch index {
	case 1:
		return "zh-Hans"
	case 2:
		return "zh-Hant"
	default:
		return "en"
	}
}

func localizedMessage(code, locale string) string {
	messagesMu.RLock()
	defer messagesMu.RUnlock()
	return localizedCatalogMessage(code, locale)
}

func lookupMessage(code, locale string) (string, bool) {
	messagesMu.RLock()
	defer messagesMu.RUnlock()
	if translations, ok := messages[code]; ok {
		if message, ok := translations[locale]; ok {
			return message, true
		}
	}
	return "", false
}

func localizedCatalogMessage(code, locale string) string {
	if translations, ok := messages[code]; ok {
		if message, ok := translations[locale]; ok {
			return message
		}
	}
	return messages["REQUEST_FAILED"][locale]
}

func localizedValidationMessage(code, locale string) string {
	messagesMu.RLock()
	defer messagesMu.RUnlock()
	if _, ok := messages[code]; !ok {
		code = "INVALID_VALUE"
	}
	return localizedCatalogMessage(code, locale)
}
