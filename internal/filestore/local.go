package filestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

const (
	escapeErrText     = "path escapes"
	defaultFileMode   = os.FileMode(0o640)
	defaultDirMode    = os.FileMode(0o750)
	lockMarkerSuffix  = ".lock"
	tempFilePrefix    = ".zhulong.tmp."
	tempRandomByteLen = 8
)

// LocalConfig configures a LocalDriver backed by os.Root handles.
type LocalConfig struct {
	// RootDir is the primary media root containing recordings/, snapshots/, and exports/.
	RootDir string
	// NamespaceRoots optionally redirects specific namespaces (such as faces or maps)
	// to dedicated directories where object sub-paths are stored directly beneath
	// the override root, preserving existing on-disk layouts.
	NamespaceRoots map[string]string
	// Inspector optionally supplies kernel mount telemetry for fallthrough checks.
	Inspector MountInspector
	Logger    *zap.Logger
}

type localRootHandle struct {
	path string
	root *os.Root
}

// LocalDriver implements Store, ProtectionMarkerStore, LocalRootInspector, and
// LocalFileImporter using Go os.Root directory file descriptors.
type LocalDriver struct {
	rootDir        string
	namespaceRoots map[string]string
	inspector      MountInspector
	logger         *zap.Logger

	mu      sync.RWMutex
	handles map[string]*localRootHandle
	closed  bool
}

// NewLocalDriver constructs a LocalDriver without touching the filesystem.
// Handles are opened lazily on first access so startup succeeds even if an
// external disk is not mounted yet.
func NewLocalDriver(cfg LocalConfig) *LocalDriver {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	nsRoots := make(map[string]string, len(cfg.NamespaceRoots))
	for ns, dir := range cfg.NamespaceRoots {
		trimmed := strings.TrimSpace(dir)
		if trimmed != "" {
			nsRoots[ns] = trimmed
		}
	}
	return &LocalDriver{
		rootDir:        strings.TrimSpace(cfg.RootDir),
		namespaceRoots: nsRoots,
		inspector:      cfg.Inspector,
		logger:         logger.Named("filestore.local"),
		handles:        map[string]*localRootHandle{},
	}
}

// Backend returns BackendLocal.
func (d *LocalDriver) Backend() string {
	return BackendLocal
}

// LocalRoot returns the configured primary root directory.
func (d *LocalDriver) LocalRoot() string {
	return d.rootDir
}

// HasNamespaceDir reports whether the namespace subdirectory exists on disk.
// It uses a single os.Stat call and never enumerates directories.
func (d *LocalDriver) HasNamespaceDir(_ context.Context, namespace string) bool {
	if !IsKnownNamespace(namespace) {
		return false
	}
	if override, ok := d.namespaceRoots[namespace]; ok && override != "" {
		info, err := os.Stat(override)
		return err == nil && info.IsDir()
	}
	if d.rootDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(d.rootDir, namespace))
	return err == nil && info.IsDir()
}

// Close releases all cached os.Root handles. Already-opened ReadSeekCloser file
// descriptors remain readable until closed by their callers.
func (d *LocalDriver) Close() error {
	d.mu.Lock()
	handles := d.handles
	d.handles = map[string]*localRootHandle{}
	d.closed = true
	d.mu.Unlock()

	var errs []error
	for _, h := range handles {
		if h != nil && h.root != nil {
			if err := h.root.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Put writes an object atomically inside the target root using os.Root.
func (d *LocalDriver) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	opts PutOptions,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return apperr.Invalid("MEDIA_PATH_INVALID", "Media content reader must not be nil", nil)
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}

	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return err
	}
	handle, err := d.acquireRoot(target.rootPath, true)
	if err != nil {
		return err
	}

	parent := path.Dir(target.relPath)
	if parent != "." && parent != "" {
		if err := handle.root.MkdirAll(parent, defaultDirMode); err != nil {
			return fmt.Errorf("create parent directory %q: %w", parent, err)
		}
	}

	mode := opts.Mode
	if mode == 0 {
		mode = defaultFileMode
	}

	if opts.Protected {
		if err := createProtectionMarker(handle.root, target.relPath, mode); err != nil {
			return err
		}
	}

	tmpRel, err := randomTempSibling(target.relPath)
	if err != nil {
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return err
	}

	tmpFile, err := handle.root.OpenFile(tmpRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return mapRootWriteError(err)
	}

	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			removeBestEffort(handle.root, tmpRel)
		}
	}()

	if _, err := io.Copy(tmpFile, r); err != nil {
		closeReaderBestEffort(tmpFile)
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return fmt.Errorf("write object %q: %w", cleanKey, err)
	}
	if err := tmpFile.Close(); err != nil {
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return fmt.Errorf("close object %q: %w", cleanKey, err)
	}

	if err := handle.root.Rename(tmpRel, target.relPath); err != nil {
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return fmt.Errorf("commit object %q: %w", cleanKey, err)
	}
	cleanupTemp = false
	return nil
}

// PutFromLocalFile adopts srcPath into the local store, using an atomic rename
// when possible and falling back to a safe root-contained copy on EXDEV.
func (d *LocalDriver) PutFromLocalFile(
	ctx context.Context,
	key string,
	srcPath string,
	opts PutOptions,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}

	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return err
	}
	handle, err := d.acquireRoot(target.rootPath, true)
	if err != nil {
		return err
	}

	parent := path.Dir(target.relPath)
	if parent != "." && parent != "" {
		if err := handle.root.MkdirAll(parent, defaultDirMode); err != nil {
			return fmt.Errorf("create parent directory %q: %w", parent, err)
		}
	}

	mode := opts.Mode
	if mode == 0 {
		mode = defaultFileMode
	}
	if opts.Protected {
		if err := createProtectionMarker(handle.root, target.relPath, mode); err != nil {
			return err
		}
	}

	adopted, renameErr := d.tryRenameIntoRoot(
		handle.root,
		target,
		parent,
		srcPath,
	)
	if adopted {
		return nil
	}
	if renameErr != nil && !errors.Is(renameErr, syscall.EXDEV) {
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return renameErr
	}

	file, err := os.Open(srcPath)
	if err != nil {
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return fmt.Errorf("open local source %q: %w", srcPath, err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		closeReaderBestEffort(file)
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return fmt.Errorf("stat local source %q: %w", srcPath, statErr)
	}
	if info.IsDir() {
		closeReaderBestEffort(file)
		if opts.Protected {
			removeBestEffort(handle.root, target.relPath+lockMarkerSuffix)
		}
		return apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", ErrNotAFile)
	}

	opts.Size = info.Size()
	putErr := d.Put(
		ctx,
		cleanKey,
		file,
		opts,
	)
	closeReaderBestEffort(file)
	if putErr != nil {
		return putErr
	}
	if rmErr := os.Remove(srcPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("remove local source %q: %w", srcPath, rmErr)
	}
	return nil
}

// Open resolves key inside its root and returns an open ReadSeekCloser plus metadata.
func (d *LocalDriver) Open(
	ctx context.Context,
	key string,
) (ReadSeekCloser, ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, err
	}
	cleanKey, err := ValidateKey(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}

	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	handle, err := d.acquireRoot(target.rootPath, false)
	if err != nil {
		if target.isOverride && errors.Is(err, os.ErrNotExist) {
			return nil, ObjectInfo{}, apperr.NotFound("MEDIA_ENTRY_NOT_FOUND", "Media entry not found", ErrNotFound)
		}
		return nil, ObjectInfo{}, err
	}

	file, err := handle.root.Open(target.relPath)
	if err != nil {
		return nil, ObjectInfo{}, mapRootReadError(err)
	}

	info, err := file.Stat()
	if err != nil {
		closeReaderBestEffort(file)
		return nil, ObjectInfo{}, apperr.Internal(
			"MEDIA_ENTRY_UNREADABLE",
			"Media entry could not be read",
			err,
		)
	}
	if info.IsDir() {
		closeReaderBestEffort(file)
		return nil, ObjectInfo{}, apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", ErrNotAFile)
	}

	return file, objectInfoFromFileInfo(cleanKey, info), nil
}

// Stat resolves key metadata without opening a transfer handle.
func (d *LocalDriver) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	cleanKey, err := ValidateKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}

	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return ObjectInfo{}, err
	}
	handle, err := d.acquireRoot(target.rootPath, false)
	if err != nil {
		if target.isOverride && errors.Is(err, os.ErrNotExist) {
			return ObjectInfo{}, apperr.NotFound("MEDIA_ENTRY_NOT_FOUND", "Media entry not found", ErrNotFound)
		}
		return ObjectInfo{}, err
	}

	info, err := handle.root.Stat(target.relPath)
	if err != nil {
		return ObjectInfo{}, mapRootReadError(err)
	}
	if info.IsDir() {
		return ObjectInfo{}, apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", ErrNotAFile)
	}

	return objectInfoFromFileInfo(cleanKey, info), nil
}

// Delete removes key from its root. Missing files succeed idempotently.
func (d *LocalDriver) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}

	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return err
	}
	handle, err := d.acquireRoot(target.rootPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	if err := handle.root.Remove(target.relPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapRootReadError(err)
	}
	return nil
}

// Exists reports whether key resolves to a regular file inside its root.
func (d *LocalDriver) Exists(ctx context.Context, key string) (bool, error) {
	_, err := d.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// Health inspects availability and optional mount identity of the primary root.
func (d *LocalDriver) Health(ctx context.Context) (HealthStatus, error) {
	status := HealthStatus{
		Backend: BackendLocal,
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if d.rootDir == "" {
		status.Message = "media root is not configured"
		return status, nil
	}

	if _, err := d.acquireRoot(d.rootDir, false); err != nil {
		status.Message = err.Error()
		return status, nil
	}
	status.Available = true

	if d.inspector != nil && deviceTelemetryIsReal {
		telemetry, err := d.inspector.InspectMount(d.rootDir)
		if err != nil {
			status.Available = false
			status.Message = err.Error()
			return status, nil
		}
		status.DeviceID = telemetry.DeviceID
		status.MountPoint = telemetry.MountPoint
		status.FSType = telemetry.FSType
		status.IsReadOnly = telemetry.IsReadOnly

		if rootDev, devErr := d.inspector.RootDeviceID(); devErr == nil {
			status.RootDeviceID = rootDev
			status.IsExternal = telemetry.DeviceID != rootDev
		}
	}

	return status, nil
}

// EnsureProtectionMarker idempotently creates "<key>.lock" inside its root.
func (d *LocalDriver) EnsureProtectionMarker(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}
	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return err
	}
	handle, err := d.acquireRoot(target.rootPath, true)
	if err != nil {
		return err
	}
	parent := path.Dir(target.relPath)
	if parent != "." && parent != "" {
		if err := handle.root.MkdirAll(parent, defaultDirMode); err != nil {
			return fmt.Errorf("create marker parent %q: %w", parent, err)
		}
	}
	return createProtectionMarker(handle.root, target.relPath, defaultFileMode)
}

// RemoveProtectionMarker idempotently deletes "<key>.lock" inside its root.
func (d *LocalDriver) RemoveProtectionMarker(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}
	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return err
	}
	handle, err := d.acquireRoot(target.rootPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	marker := target.relPath + lockMarkerSuffix
	if err := handle.root.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return mapRootReadError(err)
	}
	return nil
}

// HasProtectionMarker reports whether "<key>.lock" exists inside its root.
func (d *LocalDriver) HasProtectionMarker(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return false, err
	}
	target, err := d.resolveTarget(cleanKey)
	if err != nil {
		return false, err
	}
	handle, err := d.acquireRoot(target.rootPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	_, err = handle.root.Lstat(target.relPath + lockMarkerSuffix)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, mapRootReadError(err)
}

// NamespaceRootsCopy returns a copy of the configured namespace root overrides.
func (d *LocalDriver) NamespaceRootsCopy() map[string]string {
	out := make(map[string]string, len(d.namespaceRoots))
	maps.Copy(out, d.namespaceRoots)
	return out
}

type resolvedTarget struct {
	rootPath   string
	relPath    string
	isOverride bool
}

func (d *LocalDriver) resolveTarget(cleanKey string) (resolvedTarget, error) {
	ns, rest := SplitKey(cleanKey)
	if override, ok := d.namespaceRoots[ns]; ok && override != "" {
		if rest == "" {
			return resolvedTarget{}, apperr.Invalid(
				"MEDIA_NOT_A_FILE",
				"Media path is not a file",
				ErrNotAFile,
			)
		}
		return resolvedTarget{
			rootPath:   override,
			relPath:    rest,
			isOverride: true,
		}, nil
	}

	if d.rootDir == "" {
		return resolvedTarget{}, apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media root is not configured",
			ErrStoreUnavailable,
		)
	}
	return resolvedTarget{
		rootPath:   d.rootDir,
		relPath:    cleanKey,
		isOverride: false,
	}, nil
}

func (d *LocalDriver) acquireRoot(rootPath string, create bool) (*localRootHandle, error) {
	if rootPath == "" {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", ErrStoreUnavailable)
	}

	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is closed", ErrStoreUnavailable)
	}
	if cached, ok := d.handles[rootPath]; ok && cached != nil {
		d.mu.RUnlock()
		return cached, nil
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is closed", ErrStoreUnavailable)
	}
	if cached, ok := d.handles[rootPath]; ok && cached != nil {
		return cached, nil
	}

	if create {
		if err := os.MkdirAll(rootPath, defaultDirMode); err != nil {
			return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
		}
	}

	handle, err := openVerifiedRoot(rootPath, d.inspector)
	if err != nil {
		return nil, err
	}
	d.handles[rootPath] = handle
	return handle, nil
}

func (d *LocalDriver) tryRenameIntoRoot(
	root *os.Root,
	target resolvedTarget,
	parent string,
	srcPath string,
) (bool, error) {
	// Verify every parent segment via os.Root before issuing an OS-level rename
	// so no symlink planted inside the root can redirect the destination outside.
	if parent != "." && parent != "" {
		opened, err := root.OpenRoot(parent)
		if err != nil {
			return false, mapRootWriteError(err)
		}
		closeRootBestEffort(opened)
	}

	dstAbs := filepath.Join(target.rootPath, filepath.FromSlash(target.relPath))
	if err := os.Rename(srcPath, dstAbs); err != nil {
		return false, err
	}
	return true, nil
}

func openVerifiedRoot(rootPath string, inspector MountInspector) (*localRootHandle, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media root is unavailable",
			errors.Join(ErrStoreUnavailable, err),
		)
	}

	selfDev, err := deviceIDOf(root)
	if err != nil {
		closeRootBestEffort(root)
		return nil, apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media root is unavailable",
			errors.Join(ErrStoreUnavailable, err),
		)
	}

	if err := verifyNotFallenThrough(inspector, rootPath, selfDev); err != nil {
		closeRootBestEffort(root)
		return nil, err
	}

	return &localRootHandle{
		path: rootPath,
		root: root,
	}, nil
}

func deviceIDOf(root *os.Root) (uint64, error) {
	info, err := root.Stat(".")
	if err != nil {
		return 0, fmt.Errorf("stat media root: %w", err)
	}
	deviceID, ok := deviceIDFromFileInfo(info)
	if !ok {
		return 0, nil
	}
	return deviceID, nil
}

func verifyNotFallenThrough(
	inspector MountInspector,
	rootPath string,
	selfDev uint64,
) error {
	if inspector == nil || !deviceTelemetryIsReal {
		return nil
	}

	telemetry, err := inspector.InspectMount(rootPath)
	if err != nil {
		return apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media root is unavailable",
			errors.Join(ErrStoreUnavailable, err),
		)
	}

	rootDev, err := inspector.RootDeviceID()
	if err != nil {
		return apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media root is unavailable",
			errors.Join(ErrStoreUnavailable, err),
		)
	}

	if isFallenThrough(selfDev, telemetry.DeviceID, rootDev) {
		return apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media root device changed",
			ErrStoreUnavailable,
		)
	}
	return nil
}

// isFallenThrough reports whether an external mount point silently resolved back
// to the system root filesystem after its backing disk disconnected.
func isFallenThrough(selfDev, statsDev, rootDev uint64) bool {
	return selfDev != rootDev && statsDev == rootDev
}

func createProtectionMarker(root *os.Root, relPath string, mode os.FileMode) error {
	marker := relPath + lockMarkerSuffix
	_, err := root.Lstat(marker)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return mapRootWriteError(err)
	}
	file, err := root.OpenFile(marker, os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return mapRootWriteError(err)
	}
	return file.Close()
}

func randomTempSibling(relPath string) (string, error) {
	var raw [tempRandomByteLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate temp filename: %w", err)
	}
	dir := path.Dir(relPath)
	name := tempFilePrefix + hex.EncodeToString(raw[:])
	if dir == "." || dir == "" {
		return name, nil
	}
	return path.Join(dir, name), nil
}

func mapRootReadError(err error) error {
	if err == nil {
		return nil
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && strings.Contains(pathErr.Err.Error(), escapeErrText) {
		return apperr.Invalid(
			"MEDIA_PATH_ESCAPE",
			"Media path escapes the media root",
			errors.Join(ErrPathEscape, err),
		)
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return apperr.NotFound(
			"MEDIA_ENTRY_NOT_FOUND",
			"Media entry not found",
			errors.Join(ErrNotFound, err),
		)
	}
	return apperr.Internal("MEDIA_ENTRY_UNREADABLE", "Media entry could not be read", err)
}

func mapRootWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && strings.Contains(pathErr.Err.Error(), escapeErrText) {
		return apperr.Invalid(
			"MEDIA_PATH_ESCAPE",
			"Media path escapes the media root",
			errors.Join(ErrPathEscape, err),
		)
	}
	return apperr.Internal("MEDIA_ENTRY_UNREADABLE", "Media entry could not be written", err)
}

func objectInfoFromFileInfo(cleanKey string, info os.FileInfo) ObjectInfo {
	return ObjectInfo{
		Key:         cleanKey,
		Name:        info.Name(),
		SizeBytes:   info.Size(),
		ModifiedAt:  info.ModTime().UTC(),
		ContentType: ContentTypeForKey(cleanKey),
	}
}

func removeBestEffort(root *os.Root, relPath string) {
	if root == nil || relPath == "" {
		return
	}
	if err := root.Remove(relPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Best-effort rollback cleanup after write failure.
	}
}

func closeRootBestEffort(root *os.Root) {
	if root == nil {
		return
	}
	if err := root.Close(); err != nil {
		// Best-effort cleanup of auxiliary os.Root handles.
	}
}
