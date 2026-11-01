package storage

import "github.com/nikonikowuw/Zhulong/internal/apperr"

// Stable machine codes for the storage domain. They are the wire contract with
// the SPA and the keys of the translations registered in locale.go, so changing
// one is an API break.
const (
	codeInvalidStorageConfig = "INVALID_STORAGE_CONFIG"
	codeCleanupInProgress    = "STORAGE_CLEANUP_IN_PROGRESS"
	codeCleanupFailed        = "STORAGE_CLEANUP_FAILED"
	codeTelemetryUnavailable = "STORAGE_TELEMETRY_UNAVAILABLE"
	codeDatabaseError        = "DATABASE_ERROR"
)

// invalidStorageConfig rejects a configuration the domain refuses to persist.
func invalidStorageConfig(err error) *apperr.Error {
	return apperr.Invalid(codeInvalidStorageConfig, "Invalid storage configuration", err)
}

// cleanupInProgress reports that a prune cycle is already running.
func cleanupInProgress(err error) *apperr.Error {
	return apperr.Conflict(codeCleanupInProgress, "Cleanup cycle is already in progress", err)
}

// cleanupFailed wraps a prune cycle that failed for a server-side reason.
func cleanupFailed(err error) *apperr.Error {
	return apperr.Internal(codeCleanupFailed, "Failed to execute storage cleanup", err)
}

// telemetryUnavailable wraps a probe that could not read volume geometry.
func telemetryUnavailable(err error) *apperr.Error {
	return apperr.Internal(codeTelemetryUnavailable, "Storage telemetry is unavailable", err)
}

// databaseFailure wraps a store failure. DATABASE_ERROR is the machine code every
// domain store failure reports; its translation lives in httputil/locale.go.
func databaseFailure(err error) *apperr.Error {
	return apperr.Internal(codeDatabaseError, "Database operation failed", err)
}
