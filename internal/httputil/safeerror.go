package httputil

import "github.com/gin-gonic/gin"

// Context keys for the sanitized error metadata written by WriteError.
const (
	errorCodeKey    = "zhulong.http.error_code"
	errorMessageKey = "zhulong.http.error_message"
)

// SafeError is the already-sanitized error information exposed to other HTTP
// layers, such as the audit adapter. Both fields are safe to persist: Code is a
// stable machine-readable identifier and Message is the outward-facing
// localized text. The internal cause is deliberately excluded.
type SafeError struct {
	Code    string
	Message string
}

// setSafeError records the sanitized error metadata on the request context.
// WriteError is the only caller, so every error response produced through the
// standard path is observable without reaching into raw error values.
func setSafeError(c *gin.Context, code, message string) {
	c.Set(errorCodeKey, code)
	c.Set(errorMessageKey, message)
}

// SafeErrorFromContext returns the sanitized error metadata written by
// WriteError, if any. The boolean is false when the request did not produce a
// standard error response.
func SafeErrorFromContext(c *gin.Context) (SafeError, bool) {
	if c == nil {
		return SafeError{}, false
	}
	codeValue, exists := c.Get(errorCodeKey)
	if !exists {
		return SafeError{}, false
	}
	code, _ := codeValue.(string)
	if code == "" {
		return SafeError{}, false
	}
	messageValue, _ := c.Get(errorMessageKey)
	message, _ := messageValue.(string)
	return SafeError{Code: code, Message: message}, true
}
