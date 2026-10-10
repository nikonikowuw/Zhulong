//go:build !linux

package storage

import (
	"os"
	"path/filepath"
)

// GetRootDeviceID returns a stub root device ID on non-Linux platforms.
func (p *DefaultPathInspector) GetRootDeviceID() (uint64, error) {
	return 1, nil
}

// InspectFS provides safe fallback filesystem telemetry for non-Linux platforms.
func (p *DefaultPathInspector) InspectFS(path string) (*FSStats, error) {
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(cleanPath, 0o750); err != nil && !os.IsExist(err) {
		// ignore
	}

	// 100 GB total, 50 GB free fallback
	total := uint64(100 * 1024 * 1024 * 1024)
	free := uint64(50 * 1024 * 1024 * 1024)
	used := total - free

	return &FSStats{
		TotalBytes: total,
		FreeBytes:  free,
		UsedBytes:  used,
		DeviceID:   1,
		MountPoint: "/",
		FSType:     "simulated",
		IsReadOnly: false,
		IsExternal: false,
	}, nil
}
