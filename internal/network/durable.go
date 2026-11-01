package network

import (
	"errors"
	"fmt"
	"os"

	"go.uber.org/zap"
)

// writeFileSynced writes data to path with the given permission and fsyncs it before closing,
// so that the content is on non-volatile media before the caller relies on it.
func writeFileSynced(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, errors.Join(err, file.Close()))
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("fsync %s: %w", path, errors.Join(err, file.Close()))
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	return nil
}

// discardTempFile removes a temporary file whose content could not be committed.
// The caller is already returning a more specific error, so removal failures are only logged.
func discardTempFile(logger *zap.Logger, path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("Failed to remove temporary file",
			zap.String("path", path),
			zap.Error(err))
	}
}
