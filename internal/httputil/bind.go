package httputil

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

var (
	validationMappersMu sync.RWMutex
	validationMappers   []func(validator.FieldError) string
)

// init installs the JSON field-name resolver on gin's default validator, so
// FieldDetail.Field always carries the name the client actually sent.
//
// It must run before any validation: RegisterTagNameFunc is documented as
// "not thread-safe, intended to be registered prior to any validation", and
// validation happens inside c.ShouldBindJSON, which is earlier than any
// HandleBindError call. Registering lazily from HandleBindError would miss the
// first failure and silently fall back to Go field names. gin builds its
// validator lazily, so Engine() is called here to force it into existence.
func init() {
	engine, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	engine.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return toLowerCamel(field.Name)
		}
		return name
	})
}

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
func HandleBindError(c *gin.Context, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		WritePayloadTooLarge(c)
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		details := make([]FieldDetail, 0, len(validationErrors))
		for _, fieldError := range validationErrors {
			// Field() is resolved by the tag name function installed in init.
			details = append(details, FieldDetail{
				Field: fieldError.Field(),
				Code:  validationCode(fieldError),
			})
		}
		WriteError(c, NewValidationError("Validation failed", details))
		return
	}

	WriteError(c, NewError(http.StatusBadRequest, "INVALID_FORMAT", "Invalid request format", err))
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
