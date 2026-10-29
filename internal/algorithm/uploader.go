package algorithm

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
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

// archiveType indicates the detected format of an uploaded archive.
type archiveType int

const (
	archiveTypeUnknown archiveType = iota
	archiveTypeTarGz
	archiveTypeZip
)

// detectArchiveType inspects magic bytes to detect archive type.
// .tar.gz starts with 0x1F, 0x8B
// .zip starts with 0x50, 0x4B, 0x03, 0x04 ("PK\x03\x04")
func detectArchiveType(header []byte) archiveType {
	if hasGzipMagic(header) {
		return archiveTypeTarGz
	}
	if hasZipMagic(header) {
		return archiveTypeZip
	}
	return archiveTypeUnknown
}

// countingReader tracks the cumulative bytes read and enforces a hard limit.
// 包内私有：它没有构造函数，字段也不导出，导出只会得到一个外部无法使用的类型。
type countingReader struct {
	r         io.Reader
	readBytes int64
	limit     int64
}

// hasGzipMagic and hasZipMagic sniff archive signatures from the first bytes of
// an upload. Short reads simply fail the check instead of panicking.
func hasGzipMagic(header []byte) bool {
	return len(header) >= 2 && header[0] == 0x1F && header[1] == 0x8B
}

func hasZipMagic(header []byte) bool {
	return len(header) >= 4 && bytes.Equal(header[:4], []byte{0x50, 0x4B, 0x03, 0x04})
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.readBytes += int64(n)
	if cr.readBytes > cr.limit {
		return n, ErrDecompressionBombSize
	}
	return n, err
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

	detected := detectArchiveType(header[:n])
	if detected == archiveTypeUnknown {
		return ErrUnsupportedArchive
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek archive start: %w", err)
	}

	cleanDestDir, err := filepath.Abs(filepath.Clean(destDir))
	if err != nil {
		return fmt.Errorf("resolve destination directory: %w", err)
	}

	if err := os.MkdirAll(cleanDestDir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	switch detected {
	case archiveTypeTarGz:
		return extractTarGz(file, cleanDestDir)
	case archiveTypeZip:
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

		targetPath, err := resolveExtractTarget(cleanDestDir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			written, copyErr := writeArchiveFile(targetPath, tr, MaxDecompressedSize-totalBytes)
			totalBytes += written
			if copyErr != nil {
				return copyErr
			}
		}

		// 入口权限由 verifier 在确认是包内普通文件后统一规范（单一来源）。
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

		targetPath, err := resolveExtractTarget(cleanDestDir, f.Name)
		if err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip file entry: %w", err)
		}

		written, copyErr := writeArchiveFile(targetPath, rc, MaxDecompressedSize-totalBytes)
		closeErr := rc.Close()
		totalBytes += written
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return fmt.Errorf("close zip file entry: %w", closeErr)
		}

		// 入口权限由 verifier 在确认是包内普通文件后统一规范（单一来源）。
	}

	return nil
}

// resolveExtractTarget 把归档条目名解析成落盘路径，并拒绝任何会逃出 destDir 的条目。
//
// tar 与 zip 共用这唯一一份判定：路径穿越是安全边界，两份实现迟早会分叉。
func resolveExtractTarget(cleanDestDir, entryName string) (string, error) {
	relPath := filepath.Clean(entryName)
	if isZipSlipPath(relPath) {
		return "", ErrZipSlip
	}

	targetPath := filepath.Join(cleanDestDir, relPath)
	if targetPath != cleanDestDir &&
		!strings.HasPrefix(targetPath, cleanDestDir+string(filepath.Separator)) {
		return "", ErrZipSlip
	}
	return targetPath, nil
}

// writeArchiveFile 把条目内容写入 targetPath，超出 remaining 的部分由
// countingReader 熔断。返回实际写入字节数，供调用方累计解压总量。
func writeArchiveFile(targetPath string, r io.Reader, remaining int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir parent: %w", err)
	}

	outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("create extracted file: %w", err)
	}

	written, copyErr := io.Copy(outFile, &countingReader{r: r, limit: remaining})
	closeErr := outFile.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if closeErr != nil {
		return written, fmt.Errorf("close extracted file: %w", closeErr)
	}
	return written, nil
}

// ExtractMultipartFile extracts a specific form file part from an incoming multipart HTTP request.
// It returns the part ReadCloser and a cleanup closure to close the part.
// isZipSlipPath reports whether a filepath.Clean-ed archive entry name would
// land outside the extraction directory. Archive entry names use forward
// slashes regardless of host OS, so the separator check is a literal "/".
func isZipSlipPath(relPath string) bool {
	if filepath.IsAbs(relPath) {
		return true
	}
	return strings.HasPrefix(relPath, "..") || strings.HasPrefix(relPath, "/")
}

func ExtractMultipartFile(r *http.Request, fieldName string) (io.ReadCloser, func(), error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, nil, httputil.NewError(http.StatusBadRequest, "INVALID_FILE", "Missing upload file", err)
	}

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				return nil, nil, httputil.NewError(
					http.StatusRequestEntityTooLarge,
					"FILE_TOO_LARGE",
					"Uploaded file exceeds size limit",
					err,
				)
			}
			return nil, nil, httputil.NewError(http.StatusBadRequest, "INVALID_FILE", "Failed to read multipart stream", err)
		}

		if part.FormName() != fieldName {
			_ = part.Close()
			continue
		}

		return part, func() { _ = part.Close() }, nil
	}

	return nil, nil, httputil.NewError(http.StatusBadRequest, "INVALID_FILE", "Missing upload file", nil)
}
