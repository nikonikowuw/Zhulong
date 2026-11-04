//go:build linux

package storage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// probeCapacity refreshes only volume geometry via statfs. It deliberately does
// not resolve the mount point: the batch stop check runs per unit on weak edge
// CPUs, and re-reading /proc/mounts there would turn a cleanup into a syscall
// storm.
func probeCapacity(path string) (*FSStats, error) {
	var statfs unix.Statfs_t
	if err := unix.Statfs(path, &statfs); err != nil {
		return nil, fmt.Errorf("statfs %q: %w", path, err)
	}

	bsize := uint64(statfs.Bsize)
	totalBytes := statfs.Blocks * bsize
	freeBytes := statfs.Bavail * bsize

	var usedBytes uint64
	switch {
	case statfs.Blocks >= statfs.Bfree:
		usedBytes = (statfs.Blocks - statfs.Bfree) * bsize
	case totalBytes >= freeBytes:
		usedBytes = totalBytes - freeBytes
	default:
		usedBytes = 0
	}

	return &FSStats{
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  usedBytes,
	}, nil
}
