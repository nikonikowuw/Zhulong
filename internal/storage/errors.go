package storage

import (
	"errors"
	"regexp"
	"strings"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// Stable machine codes for the storage domain. They are the wire contract with
// the SPA and the keys of the translations registered in locale.go, so changing
// one is an API break.
const (
	codeInvalidStorageConfig = "INVALID_STORAGE_CONFIG"
	codeCleanupInProgress    = "STORAGE_CLEANUP_IN_PROGRESS"
	codeCleanupFailed        = "STORAGE_CLEANUP_FAILED"
	codeTelemetryUnavailable = "STORAGE_TELEMETRY_UNAVAILABLE"
	codeMigrationInProgress  = "STORAGE_MIGRATION_IN_PROGRESS"
	codeMigrationRequiresS3  = "STORAGE_MIGRATION_REQUIRES_S3"
	codeMigrationUnavailable = "STORAGE_MIGRATION_UNAVAILABLE"
	codeMigrationFailed      = "STORAGE_MIGRATION_FAILED"
	codeDatabaseError        = "DATABASE_ERROR"
)

var sensitiveQueryParamPattern = regexp.MustCompile(
	`(?i)(X-Amz-Signature|X-Amz-Credential|X-Amz-Security-Token|AWSAccessKeyId|Signature)=[^&\s]+`,
)

// sanitizeErrorText removes supplied S3 secrets and presigned query tokens before
// surfacing an error reason or recording diagnostic logs.
func sanitizeErrorText(msg string, secret string) string {
	out := sensitiveQueryParamPattern.ReplaceAllString(msg, "$1=[REDACTED]")
	if trimmed := strings.TrimSpace(secret); trimmed != "" {
		out = strings.ReplaceAll(out, trimmed, "[REDACTED]")
	}
	return out
}

func redactSecretErr(err error, secret string) error {
	if err == nil {
		return nil
	}
	sanitized := sanitizeErrorText(err.Error(), secret)
	if sanitized == err.Error() {
		return err
	}
	return errors.New(sanitized)
}

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

// migrationInProgress rejects concurrent migration job submissions.
func migrationInProgress(err error) *apperr.Error {
	return apperr.Conflict(codeMigrationInProgress, "Storage migration is already in progress", err)
}

// migrationRequiresS3 rejects migration triggers when S3 is not active.
func migrationRequiresS3(err error) *apperr.Error {
	return apperr.Invalid(codeMigrationRequiresS3, "Remote S3 backend must be active to migrate local assets", err)
}

// migrationUnavailable reports missing asyncjob or migration source wiring.
func migrationUnavailable(err error) *apperr.Error {
	return apperr.Unavailable(codeMigrationUnavailable, "Storage migration service is unavailable", err)
}

// migrationFailed wraps a migration initialization or execution failure.
func migrationFailed(err error) *apperr.Error {
	return apperr.Internal(codeMigrationFailed, "Storage migration failed", err)
}

// databaseFailure wraps a store failure. DATABASE_ERROR is the machine code every
// domain store failure reports; its translation lives in httputil/locale.go.
func databaseFailure(err error) *apperr.Error {
	return apperr.Internal(codeDatabaseError, "Database operation failed", err)
}
