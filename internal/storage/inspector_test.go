package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspector_ProbeWritable(t *testing.T) {
	tempDir := t.TempDir()
	inspector := NewDefaultPathInspector()

	// Should succeed on a valid directory
	if err := inspector.ProbeWritable(tempDir); err != nil {
		t.Fatalf("ProbeWritable failed on tempDir: %v", err)
	}

	// Should auto-create non-existent subfolder and succeed
	subDir := filepath.Join(tempDir, "auto", "create", "media")
	if err := inspector.ProbeWritable(subDir); err != nil {
		t.Fatalf("ProbeWritable failed to auto-create subDir: %v", err)
	}

	// Should fail if path is a regular file, not a directory
	filePath := filepath.Join(tempDir, "regular_file.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := inspector.ProbeWritable(filePath); err == nil {
		t.Errorf("expected ProbeWritable to fail on regular file, got nil")
	}
}

func TestInspector_InspectFS(t *testing.T) {
	tempDir := t.TempDir()
	inspector := NewDefaultPathInspector()

	stats, err := inspector.InspectFS(tempDir)
	if err != nil {
		t.Fatalf("InspectFS failed: %v", err)
	}

	if stats.TotalBytes == 0 {
		t.Errorf("expected non-zero TotalBytes, got %d", stats.TotalBytes)
	}
	if stats.FreeBytes == 0 {
		t.Errorf("expected non-zero FreeBytes, got %d", stats.FreeBytes)
	}
	if stats.MountPoint == "" {
		t.Errorf("expected non-empty MountPoint")
	}
	if stats.FSType == "" {
		t.Errorf("expected non-empty FSType")
	}

	rootDev, err := inspector.GetRootDeviceID()
	if err != nil {
		t.Fatalf("GetRootDeviceID failed: %v", err)
	}
	if rootDev == 0 {
		t.Errorf("expected non-zero RootDeviceID, got %d", rootDev)
	}
}

func TestInspector_ScanBreakdown(t *testing.T) {
	tempDir := t.TempDir()
	inspector := NewDefaultPathInspector()
	ctx := context.Background()

	// Initially empty
	bd, err := inspector.ScanBreakdown(ctx, tempDir)
	if err != nil {
		t.Fatalf("ScanBreakdown failed: %v", err)
	}
	if bd.RecordingsBytes != 0 || bd.SnapshotsBytes != 0 || bd.ExportsBytes != 0 {
		t.Errorf("expected all 0 bytes for empty dir, got %+v", bd)
	}

	// Populate dummy files
	recFile := filepath.Join(tempDir, "recordings", "cam_1", "2026-10-07", "01.mp4")
	snapFile := filepath.Join(tempDir, "snapshots", "cam_1", "2026-10-07", "snap.jpg")
	expFile := filepath.Join(tempDir, "exports", "clip.mp4")

	_ = os.MkdirAll(filepath.Dir(recFile), 0o750)
	_ = os.MkdirAll(filepath.Dir(snapFile), 0o750)
	_ = os.MkdirAll(filepath.Dir(expFile), 0o750)

	_ = os.WriteFile(recFile, make([]byte, 1024), 0o600)
	_ = os.WriteFile(snapFile, make([]byte, 2048), 0o600)
	_ = os.WriteFile(expFile, make([]byte, 4096), 0o600)

	bd, err = inspector.ScanBreakdown(ctx, tempDir)
	if err != nil {
		t.Fatalf("ScanBreakdown failed: %v", err)
	}
	if bd.RecordingsBytes != 1024 {
		t.Errorf("expected 1024 recordings bytes, got %d", bd.RecordingsBytes)
	}
	if bd.SnapshotsBytes != 2048 {
		t.Errorf("expected 2048 snapshots bytes, got %d", bd.SnapshotsBytes)
	}
	if bd.ExportsBytes != 4096 {
		t.Errorf("expected 4096 exports bytes, got %d", bd.ExportsBytes)
	}
}
