package auth

import "github.com/nikonikowuw/Zhulong/internal/httputil"

func init() {
	httputil.RegisterMessages(map[string]map[string]string{
		"UNAUTHORIZED": {
			"en":      "Authentication required",
			"zh-Hans": "需要身份验证",
			"zh-Hant": "需要身分驗證",
		},
		"INVALID_CREDENTIALS": {
			"en":      "Invalid username or password",
			"zh-Hans": "用户名或密码错误",
			"zh-Hant": "使用者名稱或密碼錯誤",
		},
		"SYSTEM_ALREADY_INITIALIZED": {
			"en":      "System is already initialized",
			"zh-Hans": "系统已经初始化，禁止重复设置",
			"zh-Hant": "系統已經初始化，禁止重複設定",
		},
		"SYSTEM_NOT_INITIALIZED": {
			"en":      "System is not initialized",
			"zh-Hans": "系统尚未初始化管理员",
			"zh-Hant": "系統尚未初始化管理員",
		},
		"TOO_MANY_ATTEMPTS": {
			"en":      "Too many failed login attempts, please try again later",
			"zh-Hans": "登录尝试次数过多，请稍后再试",
			"zh-Hant": "登入嘗試次數過多，請稍後再試",
		},
		"PASSWORD_TOO_SHORT": {
			"en":      "Password must be at least 8 characters",
			"zh-Hans": "密码长度不能少于 8 位",
			"zh-Hant": "密碼長度不能少於 8 位",
		},
		"PASSWORD_MISMATCH": {
			"en":      "Passwords do not match",
			"zh-Hans": "两次输入的密码不一致",
			"zh-Hant": "兩次輸入的密碼不一致",
		},
	})
}
