package storage

import (
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
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(cleanPath, 0o750); mkErr != nil {
				return fmt.Errorf("create directory %s: %w", cleanPath, mkErr)
			}
		} else {
			return fmt.Errorf("stat directory %s: %w", cleanPath, err)
		}
	} else if !info.IsDir() {
		return fmt.Errorf("path %s is not a directory", cleanPath)
	}

	// Generate random probe file name
	var randomBytes [8]byte
	_, _ = rand.Read(randomBytes[:])
	probeFileName := filepath.Join(cleanPath, fmt.Sprintf(".zhulong_storage_probe_%s", hex.EncodeToString(randomBytes[:])))

	payload := []byte("zhulong_storage_probe_check")
	if err := os.WriteFile(probeFileName, payload, 0o600); err != nil {
		return fmt.Errorf("write test file in %s: %w", cleanPath, err)
	}
	defer func() {
		_ = os.Remove(probeFileName)
	}()

	readBack, err := os.ReadFile(probeFileName)
	if err != nil {
		return fmt.Errorf("read back test file in %s: %w", cleanPath, err)
	}
	if string(readBack) != string(payload) {
		return fmt.Errorf("corrupted test file content in %s", cleanPath)
	}

	return nil
}

// ScanBreakdown calculates disk consumption for recordings, snapshots, and exports directories.
func (p *DefaultPathInspector) ScanBreakdown(ctx context.Context, mediaDir string) (StorageUsageBreakdown, error) {
	var breakdown StorageUsageBreakdown
	cleanRoot := filepath.Clean(mediaDir)

	recordingsDir := filepath.Join(cleanRoot, "recordings")
	snapshotsDir := filepath.Join(cleanRoot, "snapshots")
	exportsDir := filepath.Join(cleanRoot, "exports")

	var err error
	breakdown.RecordingsBytes, err = dirSize(ctx, recordingsDir)
	if err != nil && !os.IsNotExist(err) {
		return breakdown, fmt.Errorf("scan recordings dir: %w", err)
	}

	breakdown.SnapshotsBytes, err = dirSize(ctx, snapshotsDir)
	if err != nil && !os.IsNotExist(err) {
		return breakdown, fmt.Errorf("scan snapshots dir: %w", err)
	}

	breakdown.ExportsBytes, err = dirSize(ctx, exportsDir)
	if err != nil && !os.IsNotExist(err) {
		return breakdown, fmt.Errorf("scan exports dir: %w", err)
	}

	return breakdown, nil
}

func dirSize(ctx context.Context, root string) (int64, error) {
	var total int64
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.IsDir() {
			finfo, err := d.Info()
			if err == nil {
				total += finfo.Size()
			}
		}
		return nil
	})

	return total, err
}
