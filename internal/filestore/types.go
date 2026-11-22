// Package filestore provides a unified, pluggable object-semantics storage layer
// across local filesystems and S3-compatible object stores (MinIO / cloud OSS).
package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// Backend identifiers for active file storage drivers.
const (
	BackendLocal  = "local"
	BackendS3     = "s3"
	BackendMemory = "memory"
)

// Logical object namespaces managed by filestore.
const (
	NamespaceRecordings = "recordings"
	NamespaceSnapshots  = "snapshots"
	NamespaceExports    = "exports"
	NamespaceFaces      = "faces"
	NamespaceMaps       = "maps"
)

// Sentinel errors returned (often wrapped inside *apperr.Error) by Store drivers.
var (
	ErrNotFound           = errors.New("filestore object not found")
	ErrNotAFile           = errors.New("filestore key refers to a directory")
	ErrInvalidKey         = errors.New("filestore key is invalid")
	ErrInvalidNamespace   = errors.New("filestore key namespace is not allowed")
	ErrPathEscape         = errors.New("filestore key escapes storage root")
	ErrStoreUnavailable   = errors.New("filestore backend is unavailable")
	ErrPresignUnsupported = errors.New("filestore backend does not support presigned URLs")
)

// ReadSeekCloser combines sequential reading, random-access seeking (required by
// http.ServeContent for HTTP 206 Range streaming), and deterministic resource close.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// ObjectInfo describes metadata for a stored object or file.
type ObjectInfo struct {
	Key         string
	Name        string
	SizeBytes   int64
	ModifiedAt  time.Time
	ContentType string
	ETag        string
}

// PutOptions configures object creation semantics.
type PutOptions struct {
	Size        int64
	ContentType string
	Mode        os.FileMode
	// Protected marks high-value alarm evidence so local directory-walk cleaners
	// write the sidecar ".lock" marker atomically before creating the object.
	Protected bool
}

// HealthStatus summarizes backend availability and optional local mount telemetry.
type HealthStatus struct {
	Backend      string
	Available    bool
	Message      string
	DeviceID     uint64
	RootDeviceID uint64
	MountPoint   string
	FSType       string
	IsExternal   bool
	IsReadOnly   bool
}

// MountTelemetry captures kernel mount identity for local fallthrough checks.
type MountTelemetry struct {
	DeviceID   uint64
	MountPoint string
	FSType     string
	IsReadOnly bool
}

// MountInspector queries filesystem device identity for local fallthrough detection.
// Kept free of storage-package types so filestore stays at the dependency bottom.
type MountInspector interface {
	InspectMount(path string) (MountTelemetry, error)
	RootDeviceID() (uint64, error)
}

// Store defines the minimal object-semantics storage contract shared by LocalFS,
// S3/MinIO, and in-memory test drivers.
type Store interface {
	Backend() string
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error
	Open(ctx context.Context, key string) (ReadSeekCloser, ObjectInfo, error)
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Health(ctx context.Context) (HealthStatus, error)
}

// Presigner is an optional capability for stores that can issue time-limited
// direct download URLs (e.g. S3/MinIO presigned GET URLs).
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// PresignCapable reports whether a store (for example Manager across dynamic
// local/S3 slots) currently supports presigned URL generation.
type PresignCapable interface {
	SupportsPresign() bool
}

// ProtectionMarkerStore is an optional capability for local filesystem stores that
// coordinate concurrent directory-walk cleaners via ".lock" sidecar files.
type ProtectionMarkerStore interface {
	EnsureProtectionMarker(ctx context.Context, key string) error
	RemoveProtectionMarker(ctx context.Context, key string) error
	HasProtectionMarker(ctx context.Context, key string) (bool, error)
}

// LocalRootInspector is an optional capability exposed by local-filesystem stores
// for root path reporting and subdirectory existence probes.
type LocalRootInspector interface {
	LocalRoot() string
	HasNamespaceDir(ctx context.Context, namespace string) bool
}

// LocalFileImporter is an optional capability allowing a store to adopt an already
// materialized local file (e.g. atomic rename on the same local filesystem).
type LocalFileImporter interface {
	PutFromLocalFile(ctx context.Context, key string, srcPath string, opts PutOptions) error
}

// LocalFilePutInput parameters for PutLocalFile.
type LocalFilePutInput struct {
	Store      Store
	Key        string
	SourcePath string
	Options    PutOptions
}

// PutLocalFile uploads or adopts a local file into input.Store. When it implements
// LocalFileImporter (e.g. LocalDriver), it uses the fast local adoption path;
// otherwise it streams the file via Put and removes input.SourcePath on success.
func PutLocalFile(ctx context.Context, input LocalFilePutInput) error {
	store := input.Store
	if store == nil {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media store is unavailable", ErrStoreUnavailable)
	}
	if importer, ok := store.(LocalFileImporter); ok {
		return importer.PutFromLocalFile(
			ctx,
			input.Key,
			input.SourcePath,
			input.Options,
		)
	}

	file, err := os.Open(input.SourcePath)
	if err != nil {
		return fmt.Errorf("open local source %q: %w", input.SourcePath, err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		closeReaderBestEffort(file)
		return fmt.Errorf("stat local source %q: %w", input.SourcePath, statErr)
	}
	if info.IsDir() {
		closeReaderBestEffort(file)
		return apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", ErrNotAFile)
	}

	opts := input.Options
	opts.Size = info.Size()
	if opts.ContentType == "" {
		opts.ContentType = ContentTypeForKey(input.Key)
	}
	putErr := store.Put(
		ctx,
		input.Key,
		file,
		opts,
	)
	if putErr != nil {
		closeReaderBestEffort(file)
		return putErr
	}
	closeReaderBestEffort(file)

	if rmErr := os.Remove(input.SourcePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("remove local source %q after upload: %w", input.SourcePath, rmErr)
	}
	return nil
}

// IsNotFound reports whether err represents a missing object or file.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return true
	}
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr != nil && appErr.Kind == apperr.KindNotFound
}

// IsUnavailable reports whether err indicates the storage backend or root is unavailable.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrStoreUnavailable) {
		return true
	}
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr != nil && appErr.Code == "MEDIA_ROOT_UNAVAILABLE"
}

// IsPresignUnsupported reports whether err indicates presigned URLs are unsupported
// by the active storage backend.
func IsPresignUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPresignUnsupported) {
		return true
	}
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr != nil && appErr.Code == "PRESIGN_UNSUPPORTED"
}

func closeReaderBestEffort(c io.Closer) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		// Read-only source file already consumed or failed with primary error.
	}
}
