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
//
// The numbers are synthetic: they exist so developers can run the host and its
// tests off-device, and must never be read as real volume state.
func (p *DefaultPathInspector) InspectFS(path string) (*FSStats, error) {
	cleanPath := filepath.Clean(path)
	// The probe directory is best effort here: a synthetic inspector cannot test
	// writability, so a creation failure must not fail the simulation either.
	_ = os.MkdirAll(cleanPath, 0o750)

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
