package mediafile

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
)

const defaultPresignTTL = 10 * time.Minute

// Service exposes controlled read access to media objects stored in filestore.
//
// It performs byte transfer and presign resolution only: no decoding, demuxing,
// remuxing, transcoding, slicing, or directory enumeration happens here.
type Service struct {
	store        filestore.Store
	provider     MediaRootProvider
	inspector    DeviceInspector
	deliveryMode DeliveryModeProvider
	presignTTL   time.Duration
	logger       *zap.Logger

	mu       sync.RWMutex
	rootPath string
	localMgr *filestore.Manager
}

// NewService creates the legacy provider-backed media file service backed by filestore.LocalDriver.
//
// The constructor is pure: it opens no files, directories, or sockets.
func NewService(provider MediaRootProvider, inspector DeviceInspector, logger *zap.Logger) *Service {
	return NewServiceWithStore(StoreOptions{
		Provider:  provider,
		Inspector: inspector,
		Logger:    logger,
	})
}

// StoreOptions configures a Service backed directly by filestore.Store.
type StoreOptions struct {
	Store        filestore.Store
	Provider     MediaRootProvider
	Inspector    DeviceInspector
	DeliveryMode DeliveryModeProvider
	PresignTTL   time.Duration
	Logger       *zap.Logger
}

// NewServiceWithStore creates a media file service backed by a pluggable filestore.Store
// (and optional DeliveryModeProvider for HTTP redirect offloading).
func NewServiceWithStore(opts StoreOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	ttl := opts.PresignTTL
	if ttl <= 0 {
		ttl = defaultPresignTTL
	}
	return &Service{
		store:        opts.Store,
		provider:     opts.Provider,
		inspector:    opts.Inspector,
		deliveryMode: opts.DeliveryMode,
		presignTTL:   ttl,
		logger:       logger.Named("mediafile.service"),
	}
}

// GetEntry returns metadata for a single media file without opening its byte stream.
func (s *Service) GetEntry(ctx context.Context, relPath string) (*MediaEntry, error) {
	cleanKey, err := filestore.ValidateMediaKey(relPath)
	if err != nil {
		return nil, err
	}

	store, err := s.resolveStore(ctx)
	if err != nil {
		return nil, err
	}

	info, err := store.Stat(ctx, cleanKey)
	if err != nil {
		return nil, err
	}

	return entryFromObjectInfo(cleanKey, info), nil
}

// OpenContent resolves a media object and returns a seekable byte stream plus
// object metadata ready to be handed to http.ServeContent.
//
// The caller owns the returned ReadSeekCloser and must close it.
func (s *Service) OpenContent(ctx context.Context, relPath string) (io.ReadSeekCloser, filestore.ObjectInfo, error) {
	cleanKey, err := filestore.ValidateMediaKey(relPath)
	if err != nil {
		return nil, filestore.ObjectInfo{}, err
	}

	store, err := s.resolveStore(ctx)
	if err != nil {
		return nil, filestore.ObjectInfo{}, err
	}

	rc, info, err := store.Open(ctx, cleanKey)
	if err != nil {
		return nil, filestore.ObjectInfo{}, err
	}

	return rc, info, nil
}

// ResolveRedirect checks whether active deliveryMode == "redirect" applies to
// relPath and returns a time-limited direct S3 presigned GET URL.
//
// When deliveryMode != "redirect", active driver does not implement Presigner,
// or an unmigrated object exists only in local fallback, it returns ("", false, nil)
// so ServeContent seamlessly falls back to streaming bytes via OpenContent.
func (s *Service) ResolveRedirect(ctx context.Context, relPath string) (string, bool, error) {
	urlStr, ok, err := s.presignMedia(
		ctx,
		relPath,
		s.presignTTL,
		true,
	)
	return urlStr, ok, err
}

// PresignMediaURL resolves an optional direct presigned URL for alert dispatchers
// when remote redirect delivery is active for a media key.
func (s *Service) PresignMediaURL(
	ctx context.Context,
	relPath string,
	ttl time.Duration,
) (string, bool, error) {
	if ttl <= 0 {
		ttl = s.presignTTL
	}
	return s.presignMedia(
		ctx,
		relPath,
		ttl,
		false,
	)
}

func (s *Service) presignMedia(
	ctx context.Context,
	relPath string,
	ttl time.Duration,
	failIfMissingEverywhere bool,
) (string, bool, error) {
	cleanKey, err := filestore.ValidateMediaKey(relPath)
	if err != nil {
		return "", false, err
	}
	if !s.isRedirectDelivery(ctx) {
		return "", false, nil
	}

	store, err := s.resolveStore(ctx)
	if err != nil {
		return "", false, err
	}

	presigner, ok := store.(filestore.Presigner)
	if !ok {
		return "", false, nil
	}
	if capable, ok := store.(filestore.PresignCapable); ok && !capable.SupportsPresign() {
		return "", false, nil
	}

	if spoolCheck, hasSpoolCheck := store.(interface {
		ExistsInSpool(context.Context, string) (bool, error)
	}); hasSpoolCheck {
		if inSpool, spoolErr := spoolCheck.ExistsInSpool(ctx, cleanKey); spoolErr == nil && inSpool {
			return "", false, nil
		}
	}

	if checker, hasPrimaryCheck := store.(interface {
		ExistsInPrimary(context.Context, string) (bool, error)
	}); hasPrimaryCheck {
		inPrimary, primaryErr := checker.ExistsInPrimary(ctx, cleanKey)
		if primaryErr != nil {
			return "", false, primaryErr
		}
		if !inPrimary {
			// Object may still reside on local fallback or be completely absent;
			// let OpenContent stream fallback or produce the canonical 404 envelope.
			return "", false, nil
		}
	} else if failIfMissingEverywhere {
		exists, existsErr := store.Exists(ctx, cleanKey)
		if existsErr != nil {
			return "", false, existsErr
		}
		if !exists {
			return "", false, apperr.NotFound(
				"MEDIA_ENTRY_NOT_FOUND",
				"Media entry not found",
				filestore.ErrNotFound,
			)
		}
	}

	urlStr, err := presigner.PresignGet(ctx, cleanKey, ttl)
	if err != nil {
		if filestore.IsPresignUnsupported(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return urlStr, true, nil
}

// GetRootInfo reports active backend health, device identity, and logical namespace readiness.
func (s *Service) GetRootInfo(ctx context.Context) (*MediaRootInfo, error) {
	store, err := s.resolveStore(ctx)
	if err != nil {
		return nil, err
	}

	health, healthErr := store.Health(ctx)
	info := &MediaRootInfo{
		Available: healthErr == nil && health.Available,
		Directories: MediaDirectories{
			Recordings: hasNamespaceDir(ctx, store, filestore.NamespaceRecordings),
			Snapshots:  hasNamespaceDir(ctx, store, filestore.NamespaceSnapshots),
			Exports:    hasNamespaceDir(ctx, store, filestore.NamespaceExports),
		},
	}

	if inspector, ok := store.(filestore.LocalRootInspector); ok {
		info.MediaRoot = inspector.LocalRoot()
	}

	if healthErr == nil && (store.Backend() != filestore.BackendLocal || deviceTelemetryIsReal) {
		info.DeviceID = health.DeviceID
		info.RootDevice = health.RootDeviceID
		info.MountPoint = health.MountPoint
		info.FSType = health.FSType
		info.IsExternal = health.IsExternal
		info.IsReadOnly = health.IsReadOnly
	}

	return info, nil
}

func (s *Service) isRedirectDelivery(ctx context.Context) bool {
	if s.deliveryMode == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(s.deliveryMode.DeliveryMode(ctx)), "redirect")
}

func (s *Service) resolveStore(ctx context.Context) (filestore.Store, error) {
	if s.store != nil {
		return s.store, nil
	}
	if s.provider == nil {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", filestore.ErrStoreUnavailable)
	}

	rootPath, err := s.provider.MediaRoot(ctx)
	if err != nil {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", err)
	}
	if strings.TrimSpace(rootPath) == "" {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", filestore.ErrStoreUnavailable)
	}

	s.mu.RLock()
	if s.localMgr != nil && s.rootPath == rootPath {
		mgr := s.localMgr
		s.mu.RUnlock()
		return mgr, nil
	}
	s.mu.RUnlock()

	return s.syncLocalManager(rootPath)
}

func (s *Service) syncLocalManager(rootPath string) (filestore.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.localMgr != nil && s.rootPath == rootPath {
		return s.localMgr, nil
	}

	var mountInspector filestore.MountInspector
	if s.inspector != nil {
		mountInspector = &deviceInspectorMountAdapter{inspector: s.inspector}
	}
	localCfg := filestore.LocalConfig{
		RootDir:   rootPath,
		Inspector: mountInspector,
		Logger:    s.logger,
	}

	if s.localMgr == nil {
		s.localMgr = filestore.NewManager(filestore.ManagerConfig{
			Local:  localCfg,
			Logger: s.logger,
		})
	} else if err := s.localMgr.ConfigureLocal(localCfg); err != nil {
		return nil, err
	}
	s.rootPath = rootPath
	return s.localMgr, nil
}

func hasNamespaceDir(ctx context.Context, store filestore.Store, namespace string) bool {
	if inspector, ok := store.(filestore.LocalRootInspector); ok {
		return inspector.HasNamespaceDir(ctx, namespace)
	}
	return true
}

func entryFromObjectInfo(relPath string, info filestore.ObjectInfo) *MediaEntry {
	return &MediaEntry{
		Path:       relPath,
		Name:       info.Name,
		SizeBytes:  info.SizeBytes,
		ModifiedAt: info.ModifiedAt.UTC(),
	}
}

func closeReadSeekCloser(c io.Closer) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		// Intentionally ignored on read-only handles: stream is finished or primary error wins.
	}
}
