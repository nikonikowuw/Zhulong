package httputil

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

var (
	validationMappersMu sync.RWMutex
	validationMappers   []func(validator.FieldError) string
)

// RegisterValidationMapper registers a domain-specific validation error code mapper.
// If a mapper returns a non-empty string, that code is used for the field validation detail.
func RegisterValidationMapper(mapper func(validator.FieldError) string) {
	if mapper == nil {
		return
	}
	validationMappersMu.Lock()
	defer validationMappersMu.Unlock()
	validationMappers = append(validationMappers, mapper)
}

// HandleBindError formats binding and validation errors according to the API contract.
func HandleBindError(c *gin.Context, err error, reqType reflect.Type) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		WritePayloadTooLarge(c)
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		details := make([]FieldDetail, 0, len(validationErrors))
		for _, fieldError := range validationErrors {
			fieldName := fieldError.StructField()
			if reqType != nil {
				fieldName = findJSONFieldName(reqType, fieldName)
			} else {
				fieldName = toLowerCamel(fieldName)
			}
			details = append(details, FieldDetail{
				Field: fieldName,
				Code:  validationCode(fieldError),
			})
		}
		WriteError(c, NewValidationError("Validation failed", details))
		return
	}

	WriteError(c, NewError(http.StatusBadRequest, "INVALID_FORMAT", "Invalid request format", err))
}

func findJSONFieldName(t reflect.Type, structFieldName string) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return toLowerCamel(structFieldName)
	}

	if field, ok := t.FieldByName(structFieldName); ok {
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" && jsonTag != "-" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				return parts[0]
			}
		}
	}
	return toLowerCamel(structFieldName)
}

func toLowerCamel(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func validationCode(fieldError validator.FieldError) string {
	validationMappersMu.RLock()
	for _, mapper := range validationMappers {
		if code := mapper(fieldError); code != "" {
			validationMappersMu.RUnlock()
			return code
		}
	}
	validationMappersMu.RUnlock()

	switch fieldError.Tag() {
	case "required":
		return "REQUIRED"
	case "min":
		if fieldError.Kind() == reflect.String {
			return "MIN_LENGTH"
		}
		return "MIN_VALUE"
	case "max":
		if fieldError.Kind() == reflect.String {
			return "MAX_LENGTH"
		}
		return "MAX_VALUE"
	default:
		return "INVALID_VALUE"
	}
}
