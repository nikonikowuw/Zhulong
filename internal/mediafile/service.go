package mediafile

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// Service exposes controlled read access to media files under the media root.
//
// It performs byte transfer only: no decoding, demuxing, remuxing, transcoding
// or slicing happens here, and no directory listing exists. Listing, filtering
// and pagination belong to the event domain, which stores media references in
// its own table and queries them with SQL.
type Service struct {
	provider  MediaRootProvider
	inspector DeviceInspector
	logger    *zap.Logger

	mu       sync.RWMutex
	rootPath string
	resolver *resolver
}

// NewService creates the media file service.
//
// The constructor is pure: it opens no file and builds no os.Root handle. The
// handle is created lazily on the first request, because a constructor that
// touches the filesystem would run before the application lifecycle starts and
// would fail startup for a media directory that is not mounted yet.
func NewService(provider MediaRootProvider, inspector DeviceInspector, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		provider:  provider,
		inspector: inspector,
		logger:    logger.Named("mediafile"),
	}
}

// GetEntry returns metadata for a single media file.
//
// The path must be relative to the media root, with no leading slash. Callers
// holding a URL path segment (which always carries a leading slash) must strip
// it first; see the handler's relativeMediaPath.
func (s *Service) GetEntry(ctx context.Context, relPath string) (*MediaEntry, error) {
	r, err := s.currentResolver(ctx)
	if err != nil {
		return nil, err
	}

	clean, info, err := r.stat(relPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", nil)
	}

	return entryFromFileInfo(clean, info), nil
}

// OpenContent resolves a media file and returns an open handle plus its
// metadata, ready to be handed to http.ServeContent.
//
// The path must be relative to the media root, with no leading slash.
//
// The caller owns the returned file and must close it.
func (s *Service) OpenContent(ctx context.Context, relPath string) (*os.File, fs.FileInfo, error) {
	r, err := s.currentResolver(ctx)
	if err != nil {
		return nil, nil, err
	}

	file, err := r.open(relPath)
	if err != nil {
		return nil, nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, apperr.Internal("MEDIA_ENTRY_UNREADABLE", "Media entry could not be read", err)
	}
	// os.Root.Open succeeds on a directory, so the "must be a file" rule has
	// to be enforced here rather than assumed from a failed open.
	if info.IsDir() {
		file.Close()
		return nil, nil, apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", nil)
	}

	return file, info, nil
}

// GetRootInfo reports whether the media root is usable and what it is mounted
// on. It backs both the frontend availability probe and operator diagnostics.
//
// Availability reuses the resolver's own validation, so a root that has fallen
// through to the system filesystem is reported unavailable here for the same
// reason a read would be refused, instead of the endpoint advertising a root
// that content requests would then reject.
func (s *Service) GetRootInfo(ctx context.Context) (*MediaRootInfo, error) {
	rootPath, err := s.provider.MediaRoot(ctx)
	if err != nil {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", err)
	}

	info := &MediaRootInfo{
		MediaRoot:   rootPath,
		Directories: detectDirectories(rootPath),
	}

	if _, err := s.currentResolver(ctx); err != nil {
		info.Available = false
		return info, nil
	}
	info.Available = true

	// Device details are only meaningful where the inspector reads real kernel
	// telemetry; elsewhere storage returns synthetic values.
	if s.inspector != nil && deviceTelemetryIsReal {
		stats, statsErr := s.inspector.InspectFS(rootPath)
		if statsErr != nil {
			// The volume cannot be verified, so it must not be advertised as
			// usable: content requests would fail the same check.
			info.Available = false
			return info, nil
		}
		info.DeviceID = stats.DeviceID
		info.MountPoint = stats.MountPoint
		info.FSType = stats.FSType
		info.IsReadOnly = stats.IsReadOnly
		if rootDev, devErr := s.inspector.GetRootDeviceID(); devErr == nil {
			info.RootDevice = rootDev
			info.IsExternal = stats.DeviceID != rootDev
		}
	}

	return info, nil
}

// currentResolver returns a resolver bound to the currently configured media
// root, rebuilding the handle when the configuration changed.
func (s *Service) currentResolver(ctx context.Context) (*resolver, error) {
	rootPath, err := s.provider.MediaRoot(ctx)
	if err != nil {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", err)
	}

	s.mu.RLock()
	if s.resolver != nil && s.rootPath == rootPath {
		current := s.resolver
		s.mu.RUnlock()
		return current, nil
	}
	s.mu.RUnlock()

	return s.rebuildResolver(rootPath)
}

// rebuildResolver swaps in a handle for the configured root.
//
// The previous handle is closed while holding the write lock. That is safe for
// in-flight requests: an already-opened file keeps its own descriptor, which
// stays readable after the root handle is closed. What must not happen is
// closing a handle another goroutine is still calling Open on, which is exactly
// what the write lock prevents.
func (s *Service) rebuildResolver(rootPath string) (*resolver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Another goroutine may have rebuilt it while this one waited for the lock.
	if s.resolver != nil && s.rootPath == rootPath {
		return s.resolver, nil
	}

	fresh, err := newResolver(rootPath, s.inspector)
	if err != nil {
		return nil, err
	}

	previous := s.resolver
	s.resolver = fresh
	s.rootPath = rootPath

	if previous != nil {
		if err := previous.Close(); err != nil {
			s.logger.Warn("failed to close previous media root handle", zap.Error(err))
		}
	}

	return fresh, nil
}

// detectDirectories reports which media subdirectories exist.
//
// It stats the three known names directly and never enumerates a directory:
// listing is outside this service's contract, so production code here must
// contain no directory-enumeration call at all. resolver_test.go enforces that.
func detectDirectories(rootPath string) MediaDirectories {
	var dirs MediaDirectories
	targets := map[string]*bool{
		"recordings": &dirs.Recordings,
		"snapshots":  &dirs.Snapshots,
		"exports":    &dirs.Exports,
	}
	for name, target := range targets {
		info, err := os.Stat(filepath.Join(rootPath, name))
		*target = err == nil && info.IsDir()
	}
	return dirs
}

// entryFromFileInfo builds the public metadata payload from a path that the
// resolver has already validated.
func entryFromFileInfo(relPath string, info fs.FileInfo) *MediaEntry {
	return &MediaEntry{
		Path:       relPath,
		Name:       info.Name(),
		SizeBytes:  info.Size(),
		ModifiedAt: info.ModTime().UTC(),
	}
}
