package apperr

import (
	"errors"
	"fmt"
)

// Kind represents the high-level semantic category of an application or domain error.
type Kind uint8

const (
	KindUnspecified Kind = iota
	KindInternal
	KindInvalid
	KindUnauthenticated
	KindPermissionDenied
	KindNotFound
	KindConflict
	KindPrecondition
	KindRateLimited
)

// String returns a human-readable representation of Kind.
func (k Kind) String() string {
	switch k {
	case KindInternal:
		return "internal"
	case KindInvalid:
		return "invalid"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindPermissionDenied:
		return "permission_denied"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindPrecondition:
		return "precondition_failed"
	case KindRateLimited:
		return "rate_limited"
	default:
		return "unspecified"
	}
}

// Error represents a domain-level error carrying semantic classification, a stable machine-readable code,
// and optional internal cause details for server-side logging.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Err     error
}

// Error returns a formatted error string.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		if e.Err != nil {
			return fmt.Sprintf("%s: %v", e.Message, e.Err)
		}
		return e.Message
	}
	if e.Code != "" {
		if e.Err != nil {
			return fmt.Sprintf("%s: %v", e.Code, e.Err)
		}
		return e.Code
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Kind.String(), e.Err)
	}
	return e.Kind.String()
}

// Unwrap exposes the internal cause for errors.Is and errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is reports whether this error matches target.
func (e *Error) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	var t *Error
	if errors.As(target, &t) {
		if t.Kind != KindUnspecified && t.Kind != e.Kind {
			return false
		}
		if t.Code != "" && t.Code != e.Code {
			return false
		}
		return true
	}
	return false
}

// New constructs an Error with the specified Kind, Code, fallback Message, and underlying cause.
func New(kind Kind, code, message string, err error) *Error {
	return &Error{
		Kind:    kind,
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// Invalid creates a validation or malformed-input domain error.
func Invalid(code, message string, err error) *Error {
	return New(KindInvalid, code, message, err)
}

// Unauthenticated creates an authentication-required or invalid-credentials error.
func Unauthenticated(code, message string, err error) *Error {
	return New(KindUnauthenticated, code, message, err)
}

// PermissionDenied creates an authorization-denied or access-forbidden error.
func PermissionDenied(code, message string, err error) *Error {
	return New(KindPermissionDenied, code, message, err)
}

// NotFound creates a resource-not-found error.
func NotFound(code, message string, err error) *Error {
	return New(KindNotFound, code, message, err)
}

// Conflict creates a resource-conflict or state-collision error.
func Conflict(code, message string, err error) *Error {
	return New(KindConflict, code, message, err)
}

// Precondition creates an unsatisfied precondition error.
func Precondition(code, message string, err error) *Error {
	return New(KindPrecondition, code, message, err)
}

// RateLimited creates a rate-limiting or too-many-attempts error.
func RateLimited(code, message string, err error) *Error {
	return New(KindRateLimited, code, message, err)
}

// Internal creates a server-side unexpected error.
func Internal(code, message string, err error) *Error {
	return New(KindInternal, code, message, err)
}
