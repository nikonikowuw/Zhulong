package apperr

import (
	"errors"
	"testing"
)

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
}
