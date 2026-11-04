//go:build !linux

package storage

// probeCapacity falls back to the synthetic inspector used by non-Linux
// development hosts. The fallback inspector never touches /proc/mounts, so it
// is already cheap enough for the batch stop check.
func probeCapacity(path string) (*FSStats, error) {
	return NewDefaultPathInspector().InspectFS(path)
}
