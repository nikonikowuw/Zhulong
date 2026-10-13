package algorithm

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxUploadSize is the maximum allowed archive upload size (500 MB).
	MaxUploadSize int64 = 500 * 1024 * 1024
	// MaxUploadRequestBodySize allows bounded multipart framing around the archive.
	MaxUploadRequestBodySize int64 = MaxUploadSize + 1<<20

	// MaxDecompressedSize is the maximum allowed total uncompressed size (1 GB).
	MaxDecompressedSize int64 = 1024 * 1024 * 1024

	// MaxDecompressedFiles is the maximum allowed file count in an archive.
	MaxDecompressedFiles = 1000
)

var (
	ErrFileTooLarge          = errors.New("file exceeds maximum allowed size (500MB)")
	ErrUnsupportedArchive    = errors.New("unsupported archive format, must be .tar.gz or .zip")
	ErrZipSlip               = errors.New("potential zip-slip path traversal attack detected")
	ErrDecompressionBombSize = errors.New("decompression bomb detected: unpacked size exceeds limit (1GB)")
	ErrDecompressionBombFile = errors.New("decompression bomb detected: file count exceeds limit (1000)")
)

// ArchiveType indicates the detected format of an uploaded archive.
type ArchiveType int

const (
	ArchiveTypeUnknown ArchiveType = iota
	ArchiveTypeTarGz
	ArchiveTypeZip
)

// DetectArchiveType inspects magic bytes to detect archive type.
// .tar.gz starts with 0x1F, 0x8B
// .zip starts with 0x50, 0x4B, 0x03, 0x04 ("PK\x03\x04")
func DetectArchiveType(header []byte) ArchiveType {
	if len(header) >= 2 && header[0] == 0x1F && header[1] == 0x8B {
		return ArchiveTypeTarGz
	}
	if len(header) >= 4 && bytes.Equal(header[:4], []byte{0x50, 0x4B, 0x03, 0x04}) {
		return ArchiveTypeZip
	}
	return ArchiveTypeUnknown
}

// CountingReader tracks the cumulative bytes read and enforces a hard limit.
type CountingReader struct {
	r         io.Reader
	readBytes int64
	limit     int64
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.readBytes += int64(n)
	if cr.readBytes > cr.limit {
		return n, ErrDecompressionBombSize
	}
	return n, err
}

// SaveUploadStream streams an incoming upload to a temporary file, enforcing MaxUploadSize.
func SaveUploadStream(r io.Reader, tempDir string) (string, error) {
	return SaveUploadStreamWithLimit(r, tempDir, MaxUploadSize)
}

// SaveUploadStreamWithLimit streams an incoming upload to a temporary file, enforcing custom size limit.
func SaveUploadStreamWithLimit(r io.Reader, tempDir string, limit int64) (string, error) {
	if limit <= 0 {
		limit = MaxUploadSize
	}
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	tmpFile, err := os.CreateTemp(tempDir, "zhulong_upload_*.bin")
	if err != nil {
		return "", fmt.Errorf("create temp upload file: %w", err)
	}
	defer tmpFile.Close()

	limited := io.LimitReader(r, limit+1)
	written, err := io.Copy(tmpFile, limited)
	if err != nil {
		if rmErr := os.Remove(tmpFile.Name()); rmErr != nil && !os.IsNotExist(rmErr) {
			// clean up attempt on copy error
		}
		return "", fmt.Errorf("copy upload stream: %w", err)
	}

	if written > limit {
		if rmErr := os.Remove(tmpFile.Name()); rmErr != nil && !os.IsNotExist(rmErr) {
			// clean up attempt on overflow
		}
		return "", ErrFileTooLarge
	}

	return tmpFile.Name(), nil
}

// ExtractArchive extracts a .tar.gz or .zip file into destDir with Zip-Slip, bomb, and permission protection.
func ExtractArchive(archivePath, destDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive file: %w", err)
	}
	defer file.Close()

	header := make([]byte, 4)
	n, err := file.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read archive magic bytes: %w", err)
	}
	if n < 2 {
		return ErrUnsupportedArchive
	}

	archiveType := DetectArchiveType(header[:n])
	if archiveType == ArchiveTypeUnknown {
		return ErrUnsupportedArchive
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek archive start: %w", err)
	}

	cleanDestDir, err := filepath.Abs(filepath.Clean(destDir))
	if err != nil {
		return fmt.Errorf("resolve destination directory: %w", err)
	}

	if err := os.MkdirAll(cleanDestDir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	switch archiveType {
	case ArchiveTypeTarGz:
		return extractTarGz(file, cleanDestDir)
	case ArchiveTypeZip:
		fi, err := file.Stat()
		if err != nil {
			return fmt.Errorf("stat archive file: %w", err)
		}
		return extractZip(file, fi.Size(), cleanDestDir)
	default:
		return ErrUnsupportedArchive
	}
}

func extractTarGz(r io.Reader, cleanDestDir string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var totalBytes int64
	var fileCount int

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		fileCount++
		if fileCount > MaxDecompressedFiles {
			return ErrDecompressionBombFile
		}

		relPath := filepath.Clean(header.Name)
		if strings.HasPrefix(relPath, "..") || strings.HasPrefix(relPath, "/") || filepath.IsAbs(relPath) {
			return ErrZipSlip
		}

		targetPath := filepath.Join(cleanDestDir, relPath)
		if !strings.HasPrefix(targetPath, cleanDestDir+string(filepath.Separator)) && targetPath != cleanDestDir {
			return ErrZipSlip
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("mkdir parent: %w", err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("create extracted file: %w", err)
			}

			cr := &CountingReader{r: tr, limit: MaxDecompressedSize - totalBytes}
			written, copyErr := io.Copy(outFile, cr)
			outFile.Close()
			totalBytes += written
			if copyErr != nil {
				return copyErr
			}

			// Automatically compensate POSIX executable permission for plugin.so
			if filepath.Base(targetPath) == "plugin.so" {
				if chmodErr := os.Chmod(targetPath, 0755); chmodErr != nil {
					return fmt.Errorf("chmod plugin.so: %w", chmodErr)
				}
			}
		}
	}

	return nil
}

func extractZip(r io.ReaderAt, size int64, cleanDestDir string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("open zip reader: %w", err)
	}

	var totalBytes int64
	var fileCount int

	for _, f := range zr.File {
		fileCount++
		if fileCount > MaxDecompressedFiles {
			return ErrDecompressionBombFile
		}

		relPath := filepath.Clean(f.Name)
		if strings.HasPrefix(relPath, "..") || strings.HasPrefix(relPath, "/") || filepath.IsAbs(relPath) {
			return ErrZipSlip
		}

		targetPath := filepath.Join(cleanDestDir, relPath)
		if !strings.HasPrefix(targetPath, cleanDestDir+string(filepath.Separator)) && targetPath != cleanDestDir {
			return ErrZipSlip
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip file entry: %w", err)
		}

		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			rc.Close()
			return fmt.Errorf("create extracted file: %w", err)
		}

		cr := &CountingReader{r: rc, limit: MaxDecompressedSize - totalBytes}
		written, copyErr := io.Copy(outFile, cr)
		outFile.Close()
		rc.Close()
		totalBytes += written
		if copyErr != nil {
			return copyErr
		}

		// Automatically compensate POSIX executable permission for plugin.so
		// Especially crucial when archive was created on Windows without POSIX permissions
		if filepath.Base(targetPath) == "plugin.so" {
			if chmodErr := os.Chmod(targetPath, 0755); chmodErr != nil {
				return fmt.Errorf("chmod plugin.so: %w", chmodErr)
			}
		}
	}

	return nil
}
