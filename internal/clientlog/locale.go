package clientlog

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"TOO_MANY_REQUESTS": {
			"en":      "Too many requests; retry later",
			"zh-Hans": "请求过于频繁，请稍后重试",
			"zh-Hant": "請求過於頻繁，請稍後重試",
		},
		"CLIENT_LOG_BUDGET_EXHAUSTED": {
			"en":      "Client log budget exhausted; retry later",
			"zh-Hans": "客户端日志配额已用尽，请稍后重试",
			"zh-Hant": "客戶端日誌配額已用盡，請稍後重試",
		},
		"CLIENT_LOG_CONCURRENCY_LIMIT": {
			"en":      "Too many concurrent client log requests",
			"zh-Hans": "客户端日志请求并发过高",
			"zh-Hant": "客戶端日誌請求併發過高",
		},
		"CLIENT_LOG_BYTE_BUDGET_EXHAUSTED": {
			"en":      "Client log byte budget exhausted; retry later",
			"zh-Hans": "客户端日志流量配额已用尽，请稍后重试",
			"zh-Hant": "客戶端日誌流量配額已用盡，請稍後重試",
		},
		"INVALID_EVENT_ID": {
			"en":      "Event identifier must be a stable lowercase token",
			"zh-Hans": "事件标识必须是稳定的小写标识符",
			"zh-Hant": "事件標識必須是穩定的小寫識別碼",
		},
		"INVALID_ROUTE_CATEGORY": {
			"en":      "Route must be an allowlisted category, not a URL",
			"zh-Hans": "路由必须是允许的分类标识，而非 URL",
			"zh-Hant": "路由必須是允許的分類標識，而非 URL",
		},
		"UNKNOWN_FIELD": {
			"en":      "Field is not allowlisted",
			"zh-Hans": "字段不在允许列表内",
			"zh-Hant": "欄位不在允許清單內",
		},
		"MAX_ITEMS": {
			"en":      "Too many items",
			"zh-Hans": "条目数量超出上限",
			"zh-Hant": "條目數量超出上限",
		},
		"MAX_LENGTH": {
			"en":      "Value is too long",
			"zh-Hans": "字段长度超出上限",
			"zh-Hant": "欄位長度超出上限",
		},
	})
}
