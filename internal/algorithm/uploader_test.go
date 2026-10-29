package algorithm_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
)

func createTestTarGz(t *testing.T, files map[string][]byte) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test_archive_*.tar.gz")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmpFile.Close()

	gw := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write tar content: %v", err)
		}
	}

	tw.Close()
	gw.Close()
	return tmpFile.Name()
}

func createTestZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test_archive_*.zip")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmpFile.Close()

	zw := zip.NewWriter(tmpFile)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip content: %v", err)
		}
	}
	zw.Close()
	return tmpFile.Name()
}

func TestSaveUploadStreamEnforcesSizeLimit(t *testing.T) {
	// A small stream should succeed
	smallData := bytes.NewReader([]byte("small payload"))
	path, err := algorithm.SaveUploadStreamWithLimit(smallData, "", algorithm.MaxUploadSize)
	if err != nil {
		t.Fatalf("SaveUploadStreamWithLimit failed: %v", err)
	}
	defer os.Remove(path)

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}
	if fi.Size() != int64(len("small payload")) {
		t.Errorf("unexpected file size: %d", fi.Size())
	}
}

func TestSaveUploadStreamWithCustomLimit(t *testing.T) {
	const customLimit int64 = 64

	// 1. Within custom limit
	dataWithin := bytes.NewReader(bytes.Repeat([]byte("a"), int(customLimit)))
	path, err := algorithm.SaveUploadStreamWithLimit(dataWithin, "", customLimit)
	if err != nil {
		t.Fatalf("expected success within custom limit, got: %v", err)
	}
	defer os.Remove(path)

	// 2. Exceeding custom limit
	dataExceeding := bytes.NewReader(bytes.Repeat([]byte("a"), int(customLimit)+1))
	_, err = algorithm.SaveUploadStreamWithLimit(dataExceeding, "", customLimit)
	if !errors.Is(err, algorithm.ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got: %v", err)
	}
}

func TestExtractArchiveZipSlipProtection(t *testing.T) {
	destDir := t.TempDir()

	// 1. Tar.gz with path traversal
	maliciousTar := createTestTarGz(t, map[string][]byte{
		"../../evil.txt": []byte("pwned"),
	})
	defer os.Remove(maliciousTar)

	err := algorithm.ExtractArchive(maliciousTar, destDir)
	if !errors.Is(err, algorithm.ErrZipSlip) {
		t.Errorf("expected ErrZipSlip for tar.gz, got: %v", err)
	}

	// 2. Zip with path traversal
	maliciousZip := createTestZip(t, map[string][]byte{
		"../evil.txt": []byte("pwned"),
	})
	defer os.Remove(maliciousZip)

	err = algorithm.ExtractArchive(maliciousZip, destDir)
	if !errors.Is(err, algorithm.ErrZipSlip) {
		t.Errorf("expected ErrZipSlip for zip, got: %v", err)
	}
}

// TestExtractArchiveDoesNotGuessEntryPermissions pins the single-source rule for
// permission bits: extraction never decides what is executable. It cannot know
// which member is the package entry (the entry is named after manifest.id), and a
// chmod-by-basename policy here would be a second, divergent opinion about what a
// runnable package is. The verifier normalises the entry after confirming it is a
// plain regular file inside the package.
func TestExtractArchiveDoesNotGuessEntryPermissions(t *testing.T) {
	destDir := t.TempDir()

	// A zip created on Windows carries no POSIX exec bits at all.
	zipPath := createTestZip(t, map[string][]byte{
		"some_pkg":   []byte("fake elf binary"),
		"sample.jpg": []byte("fake image"),
	})
	defer os.Remove(zipPath)

	if err := algorithm.ExtractArchive(zipPath, destDir); err != nil {
		t.Fatalf("ExtractArchive failed: %v", err)
	}

	fi, err := os.Stat(filepath.Join(destDir, "some_pkg"))
	if err != nil {
		t.Fatalf("stat extracted entry: %v", err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("extraction must not invent exec bits, got %o", fi.Mode().Perm())
	}
}
func TestExtractArchiveUnsupportedFormat(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "bad_archive_*.bin")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, _ = tmpFile.WriteString("random data that is not tar or zip")
	tmpFile.Close()

	destDir := t.TempDir()
	err = algorithm.ExtractArchive(tmpFile.Name(), destDir)
	if !errors.Is(err, algorithm.ErrUnsupportedArchive) {
		t.Errorf("expected ErrUnsupportedArchive, got: %v", err)
	}
}

func TestExtractArchiveDecompressionBombFileCount(t *testing.T) {
	// Create an archive with 1001 files
	files := make(map[string][]byte)
	for i := 0; i <= algorithm.MaxDecompressedFiles; i++ {
		files[fmt.Sprintf("data/file_%d.txt", i)] = []byte("1")
	}

	zipPath := createTestZip(t, files)
	defer os.Remove(zipPath)

	destDir := t.TempDir()
	err := algorithm.ExtractArchive(zipPath, destDir)
	if !errors.Is(err, algorithm.ErrDecompressionBombFile) {
		t.Errorf("expected ErrDecompressionBombFile, got: %v", err)
	}
}
