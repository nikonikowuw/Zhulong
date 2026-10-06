package httputil

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// FieldDetail contains a localized, input-safe validation message.
type FieldDetail struct {
	Field   string `json:"field" binding:"required"`
	Code    string `json:"code" binding:"required"`
	Message string `json:"message" binding:"required"`
}

// Response is the common JSON envelope used by every API endpoint.
type Response struct {
	Code    string        `json:"code" binding:"required"`
	Message string        `json:"message" binding:"required"`
	Data    any           `json:"data" binding:"required"`
	Details []FieldDetail `json:"details,omitempty"`
}

// Success writes a successful response using the request's preferred language.
func Success(c *gin.Context, data any) {
	SuccessWithStatus(c, http.StatusOK, data)
}

// SuccessWithStatus writes a successful response with a specified HTTP status code.
func SuccessWithStatus(c *gin.Context, statusCode int, data any) {
	locale := requestLocale(c.GetHeader("Accept-Language"))
	setLanguageHeaders(c, locale)
	c.JSON(statusCode, Response{
		Code:    "OK",
		Message: localizedMessage("OK", locale),
		Data:    data,
	})
}
