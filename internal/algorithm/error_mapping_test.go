package algorithm

import (
	"errors"
	"net/http"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// TestNormalizeInstallErrorRetiredTarget pins the stable machine code for the
// retired "cpu" target. The code is part of the public API surface (the SPA and
// operators act on it), so it must not drift with the sentinel's wording.
func TestNormalizeInstallErrorRetiredTarget(t *testing.T) {
	cause := errors.Join(ErrPlatformTargetRetired, errors.New("package demo 1.0.0 was built for platform.target=\"cpu\""))

	err := normalizeInstallError(cause)
	if err == nil {
		t.Fatal("expected an API error, got nil")
	}

	var apiErr *httputil.AppError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *httputil.AppError, got %T", err)
	}
	if apiErr.Code != "PLATFORM_TARGET_CPU_RETIRED" {
		t.Errorf("expected code PLATFORM_TARGET_CPU_RETIRED, got %q", apiErr.Code)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", apiErr.Status)
	}
}

// TestNormalizeInstallErrorPlatformMismatchStaysDistinct guards the two codes
// against collapsing: an unrelated arch/target mismatch must keep its own code.
func TestNormalizeInstallErrorPlatformMismatchStaysDistinct(t *testing.T) {
	err := normalizeInstallError(ErrPlatformMismatch)

	var apiErr *httputil.AppError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *httputil.AppError, got %T", err)
	}
	if apiErr.Code != "ALGO_PLATFORM_MISMATCH" {
		t.Errorf("expected code ALGO_PLATFORM_MISMATCH, got %q", apiErr.Code)
	}
}
