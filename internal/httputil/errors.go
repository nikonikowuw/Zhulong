package httputil

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AppError is an internal error description. Err is only written to server logs.
type AppError struct {
	Status  int
	Code    string
	Message string // WriteError returns the localized catalog entry for Code instead.
	Details []FieldDetail
	Err     error
}

// Error returns the internal cause when present, otherwise the stable error code.
func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

// Unwrap exposes the internal cause to server-side error handling.
func (e *AppError) Unwrap() error {
	return e.Err
}

// NewError creates a non-validation HTTP error.
func NewError(status int, code, message string, err error) *AppError {
	return &AppError{Status: status, Code: code, Message: message, Err: err}
}

// NewValidationError creates an HTTP 422 error with localized field details.
func NewValidationError(message string, details []FieldDetail) *AppError {
	copiedDetails := append([]FieldDetail(nil), details...)
	return &AppError{
		Status:  http.StatusUnprocessableEntity,
		Code:    "VALIDATION_FAILED",
		Message: message,
		Details: copiedDetails,
	}
}

// WritePayloadTooLarge writes the standard response for requests exceeding the API body limit.
func WritePayloadTooLarge(c *gin.Context) {
	WriteError(c, NewError(http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Request body too large", nil))
}

// WriteError writes a sanitized API error and logs its internal cause server-side.
func WriteError(c *gin.Context, err error) {
	var appError *AppError
	if !errors.As(err, &appError) || appError == nil {
		appError = NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", err)
	}

	status := appError.Status
	if status < http.StatusBadRequest || status > 599 {
		status = http.StatusInternalServerError
		appError = NewError(status, "INTERNAL_ERROR", "Internal server error", appError.Err)
	}

	locale := requestLocale(c.GetHeader("Accept-Language"))
	setLanguageHeaders(c, locale)
	response := Response{
		Code:    appError.Code,
		Message: localizedMessage(appError.Code, locale),
		Data:    nil,
	}
	if status == http.StatusUnprocessableEntity && appError.Code == "VALIDATION_FAILED" {
		response.Details = make([]FieldDetail, len(appError.Details))
		for i, detail := range appError.Details {
			detail.Message = localizedValidationMessage(detail.Code, locale)
			response.Details[i] = detail
		}
	}
	if appError.Err != nil {
		zap.L().Error("HTTP request failed",
			zap.String("code", appError.Code),
			zap.Int("status", status),
			zap.Error(appError.Err),
		)
	}

	c.AbortWithStatusJSON(status, response)
}
