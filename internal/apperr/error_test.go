package apperr

import (
	"errors"
	"io"
	"testing"
)

func TestKind_String(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{name: "Unspecified", kind: KindUnspecified, want: "unspecified"},
		{name: "Internal", kind: KindInternal, want: "internal"},
		{name: "Invalid", kind: KindInvalid, want: "invalid"},
		{name: "Unauthenticated", kind: KindUnauthenticated, want: "unauthenticated"},
		{name: "PermissionDenied", kind: KindPermissionDenied, want: "permission_denied"},
		{name: "NotFound", kind: KindNotFound, want: "not_found"},
		{name: "Conflict", kind: KindConflict, want: "conflict"},
		{name: "Precondition", kind: KindPrecondition, want: "precondition_failed"},
		{name: "RateLimited", kind: KindRateLimited, want: "rate_limited"},
		{name: "Unavailable", kind: KindUnavailable, want: "unavailable"},
		{name: "BadRequest", kind: KindBadRequest, want: "bad_request"},
		{name: "PayloadTooLarge", kind: KindPayloadTooLarge, want: "payload_too_large"},
		{name: "Unknown", kind: Kind(255), want: "unspecified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("Kind.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppError_Error(t *testing.T) {
	cause := errors.New("underlying cause")

	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "nil receiver",
			err:  nil,
			want: "",
		},
		{
			name: "message and cause",
			err: &Error{
				Kind:    KindInvalid,
				Code:    "INVALID_INPUT",
				Message: "invalid input",
				Err:     cause,
			},
			want: "invalid input: underlying cause",
		},
		{
			name: "message only",
			err: &Error{
				Kind:    KindInvalid,
				Code:    "INVALID_INPUT",
				Message: "invalid input",
			},
			want: "invalid input",
		},
		{
			name: "code and cause",
			err: &Error{
				Kind: KindConflict,
				Code: "CONFLICT_DETECTED",
				Err:  cause,
			},
			want: "CONFLICT_DETECTED: underlying cause",
		},
		{
			name: "code only",
			err: &Error{
				Kind: KindConflict,
				Code: "CONFLICT_DETECTED",
			},
			want: "CONFLICT_DETECTED",
		},
		{
			name: "kind and cause",
			err: &Error{
				Kind: KindInternal,
				Err:  cause,
			},
			want: "internal: underlying cause",
		},
		{
			name: "kind only",
			err: &Error{
				Kind: KindInternal,
			},
			want: "internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppError_Unwrap(t *testing.T) {
	cause := errors.New("underlying cause")

	var nilErr *Error
	if got := nilErr.Unwrap(); got != nil {
		t.Errorf("expected (*Error)(nil).Unwrap() to be nil, got %v", got)
	}

	withCause := Internal("ERR", "msg", cause)
	if got := withCause.Unwrap(); got != cause {
		t.Errorf("expected Unwrap() to return cause, got %v", got)
	}

	withoutCause := Internal("ERR", "msg", nil)
	if got := withoutCause.Unwrap(); got != nil {
		t.Errorf("expected Unwrap() to be nil, got %v", got)
	}
}

func TestAppError_LifecycleAndUnwrap(t *testing.T) {
	cause := errors.New("underlying db error")
	err := Conflict("SYSTEM_ALREADY_INITIALIZED", "system already initialized", cause)

	if err.Kind != KindConflict {
		t.Fatalf("expected KindConflict, got %v", err.Kind)
	}
	if err.Code != "SYSTEM_ALREADY_INITIALIZED" {
		t.Fatalf("expected code SYSTEM_ALREADY_INITIALIZED, got %s", err.Code)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected errors.Is to match underlying cause")
	}

	var appErr *Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected errors.As to match *Error")
	}
	if appErr.Code != "SYSTEM_ALREADY_INITIALIZED" {
		t.Fatalf("unexpected appErr code: %s", appErr.Code)
	}
}

func TestAppError_Factories(t *testing.T) {
	tests := []struct {
		name     string
		err      *Error
		wantKind Kind
		wantCode string
	}{
		{
			name:     "Invalid",
			err:      Invalid("BAD_INPUT", "bad input", nil),
			wantKind: KindInvalid,
			wantCode: "BAD_INPUT",
		},
		{
			name:     "Unauthenticated",
			err:      Unauthenticated("NO_TOKEN", "no token", nil),
			wantKind: KindUnauthenticated,
			wantCode: "NO_TOKEN",
		},
		{
			name:     "PermissionDenied",
			err:      PermissionDenied("FORBIDDEN", "forbidden", nil),
			wantKind: KindPermissionDenied,
			wantCode: "FORBIDDEN",
		},
		{
			name:     "NotFound",
			err:      NotFound("NOT_FOUND", "not found", nil),
			wantKind: KindNotFound,
			wantCode: "NOT_FOUND",
		},
		{
			name:     "Conflict",
			err:      Conflict("CONFLICT", "conflict", nil),
			wantKind: KindConflict,
			wantCode: "CONFLICT",
		},
		{
			name:     "Precondition",
			err:      Precondition("PRECONDITION_FAILED", "precondition failed", nil),
			wantKind: KindPrecondition,
			wantCode: "PRECONDITION_FAILED",
		},
		{
			name:     "RateLimited",
			err:      RateLimited("TOO_MANY_ATTEMPTS", "too many attempts", nil),
			wantKind: KindRateLimited,
			wantCode: "TOO_MANY_ATTEMPTS",
		},
		{
			name:     "Unavailable",
			err:      Unavailable("UNAVAILABLE", "service unavailable", nil),
			wantKind: KindUnavailable,
			wantCode: "UNAVAILABLE",
		},
		{
			name:     "Internal",
			err:      Internal("INTERNAL_ERROR", "internal error", nil),
			wantKind: KindInternal,
			wantCode: "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Kind != tt.wantKind {
				t.Errorf("Kind = %v, want %v", tt.err.Kind, tt.wantKind)
			}
			if tt.err.Code != tt.wantCode {
				t.Errorf("Code = %v, want %v", tt.err.Code, tt.wantCode)
			}
		})
	}
}

func TestAppError_IsMatching(t *testing.T) {
	internalErr := Internal("DATABASE_DOWN", "database unreachable", nil)
	sameKind := &Error{Kind: KindInternal}
	differentKind := &Error{Kind: KindNotFound}
	matchingCode := &Error{Code: "DATABASE_DOWN"}
	differentCode := &Error{Code: "OTHER_ERROR"}
	bothMatching := &Error{Kind: KindInternal, Code: "DATABASE_DOWN"}
	kindMismatchCodeMatch := &Error{Kind: KindNotFound, Code: "DATABASE_DOWN"}
	wildcard := &Error{}

	if !errors.Is(internalErr, sameKind) {
		t.Errorf("expected internalErr to match target with KindInternal")
	}
	if errors.Is(internalErr, differentKind) {
		t.Errorf("did not expect internalErr to match KindNotFound")
	}
	if !errors.Is(internalErr, matchingCode) {
		t.Errorf("expected internalErr to match target with same Code")
	}
	if errors.Is(internalErr, differentCode) {
		t.Errorf("did not expect internalErr to match target with different Code")
	}
	if !errors.Is(internalErr, bothMatching) {
		t.Errorf("expected internalErr to match target with same Kind and Code")
	}
	if errors.Is(internalErr, kindMismatchCodeMatch) {
		t.Errorf("did not expect internalErr to match when Kind mismatches")
	}
	if !errors.Is(internalErr, wildcard) {
		t.Errorf("expected internalErr to match wildcard target (&Error{})")
	}

	// Boundary cases: typed nil, nil target, other error types, and nil receiver
	var typedNilTarget *Error
	if errors.Is(internalErr, typedNilTarget) {
		t.Errorf("expected internalErr not to match typed nil target")
	}
	if errors.Is(internalErr, nil) {
		t.Errorf("expected internalErr not to match nil error")
	}
	if errors.Is(internalErr, io.EOF) {
		t.Errorf("expected internalErr not to match non-AppError error")
	}

	var nilReceiver *Error
	if !nilReceiver.Is(nil) {
		t.Errorf("expected (*Error)(nil).Is(nil) to be true")
	}
	if nilReceiver.Is(internalErr) {
		t.Errorf("expected (*Error)(nil).Is(non-nil) to be false")
	}
}
