package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// FSStats holds filesystem geometry and mount device identity.
type FSStats struct {
	TotalBytes uint64
	FreeBytes  uint64
	UsedBytes  uint64
	DeviceID   uint64
	MountPoint string
	FSType     string
	IsReadOnly bool
	IsExternal bool
}

// PathInspector defines filesystem querying and telemetry probe operations.
//
// InspectFS and ProbeWritable deliberately take no context: they are built on
// stat/statfs and a tiny write-read roundtrip, none of which Go can interrupt.
// Callers bound them with their own timeout instead of passing a context the
// syscalls would ignore.
type PathInspector interface {
	InspectFS(path string) (*FSStats, error)
	ProbeWritable(path string) error
	GetRootDeviceID() (uint64, error)
	ScanBreakdown(ctx context.Context, mediaDir string) (StorageUsageBreakdown, error)
}

// DefaultPathInspector provides the standard production inspector implementation.
type DefaultPathInspector struct {
	rootDevMu  sync.RWMutex
	rootDevID  uint64
	hasRootDev bool
}

// NewDefaultPathInspector creates a new system inspector.
func NewDefaultPathInspector() *DefaultPathInspector {
	return &DefaultPathInspector{}
}

// ProbeWritable performs an actual write-read-delete roundtrip with a hidden probe file.
func (p *DefaultPathInspector) ProbeWritable(path string) error {
	cleanPath := filepath.Clean(path)

	info, err := os.Stat(cleanPath)
	switch {
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("stat directory %q: %w", cleanPath, err)
	case err != nil:
		// A missing candidate directory is created here: the operator is allowed
		// to point storage at a path that does not exist yet.
		if mkErr := os.MkdirAll(cleanPath, 0o750); mkErr != nil {
			return fmt.Errorf("create directory %q: %w", cleanPath, mkErr)
		}
	case !info.IsDir():
		return fmt.Errorf("path %q is not a directory", cleanPath)
	}

	// Generate random probe file name. crypto/rand.Read never returns an error on
	// any platform Go supports, so the result needs no check.
	var randomBytes [8]byte
	_, _ = rand.Read(randomBytes[:])
	probeFileName := filepath.Join(
		cleanPath,
		fmt.Sprintf(".zhulong_storage_probe_%s", hex.EncodeToString(randomBytes[:])),
	)

	payload := []byte("zhulong_storage_probe_check")
	if err := os.WriteFile(probeFileName, payload, 0o600); err != nil {
		return fmt.Errorf("write test file in %q: %w", cleanPath, err)
	}
	defer func() {
		// Best effort: the probe already delivered its verdict, and a leftover
		// file is harmless next to a failed cleanup.
		_ = os.Remove(probeFileName)
	}()

	readBack, err := os.ReadFile(probeFileName)
	if err != nil {
		return fmt.Errorf("read back test file in %q: %w", cleanPath, err)
	}
	if !bytes.Equal(readBack, payload) {
		return fmt.Errorf("corrupted test file content in %q", cleanPath)
	}

	return nil
}

// ScanBreakdown calculates disk consumption for recordings, snapshots, and exports directories.
func (p *DefaultPathInspector) ScanBreakdown(ctx context.Context, mediaDir string) (StorageUsageBreakdown, error) {
	var breakdown StorageUsageBreakdown
	cleanRoot := filepath.Clean(mediaDir)

	targets := []struct {
		dir string
		dst *int64
	}{
		{dir: "recordings", dst: &breakdown.RecordingsBytes},
		{dir: "snapshots", dst: &breakdown.SnapshotsBytes},
		{dir: "exports", dst: &breakdown.ExportsBytes},
	}

	for _, target := range targets {
		size, err := dirSize(ctx, filepath.Join(cleanRoot, target.dir))
		if err != nil && !os.IsNotExist(err) {
			return breakdown, fmt.Errorf("scan %q dir: %w", target.dir, err)
		}
		*target.dst = size
	}

	return breakdown, nil
}

// dirSize sums the size of every regular file under root.
func dirSize(ctx context.Context, root string) (int64, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	var total int64
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				// A directory vanishing mid-walk just stops contributing bytes.
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		finfo, err := d.Info()
		if err != nil {
			// The file was removed between the walk and the stat; it simply no
			// longer occupies space.
			return nil
		}
		total += finfo.Size()

		return nil
	})

	return total, err
}
