package filestore

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// ManagerConfig initializes a hot-swappable filestore.Manager without
// performing network or disk I/O inside NewManager.
type ManagerConfig struct {
	Local         LocalConfig
	LocalFallback bool
	Logger        *zap.Logger
}

type managerSlot struct {
	primary   Store
	spool     *LocalDriver
	fallback  *LocalDriver
	refs      atomic.Int64
	retired   atomic.Bool
	closeOnce sync.Once
	logger    *zap.Logger
}

func newManagerSlot(primary Store, spool, fallback *LocalDriver, logger *zap.Logger) *managerSlot {
	slot := &managerSlot{
		primary:  primary,
		spool:    spool,
		fallback: fallback,
		logger:   logger,
	}
	slot.refs.Store(1)
	return slot
}

func (s *managerSlot) acquire() bool {
	if s == nil || s.retired.Load() {
		return false
	}
	s.refs.Add(1)
	if s.retired.Load() {
		s.release()
		return false
	}
	return true
}

func (s *managerSlot) release() {
	if s == nil {
		return
	}
	if s.refs.Add(-1) == 0 && s.retired.Load() {
		s.closeOnce.Do(s.closeOwned)
	}
}

func (s *managerSlot) retire() {
	if s == nil {
		return
	}
	s.retired.Store(true)
	s.release()
}

func (s *managerSlot) closeOwned() {
	if closer, ok := s.primary.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			s.logger.Warn("close retired primary filestore driver", zap.Error(err))
		}
	}
	if s.spool != nil {
		if err := s.spool.Close(); err != nil {
			s.logger.Warn("close retired spool driver", zap.Error(err))
		}
	}
	if s.fallback != nil {
		if err := s.fallback.Close(); err != nil {
			s.logger.Warn("close retired local fallback driver", zap.Error(err))
		}
	}
}

// Manager provides thread-safe runtime hot-switching between LocalFS and S3
// drivers with optional read-only local fallback for unmigrated files.
type Manager struct {
	mu            sync.RWMutex
	slot          *managerSlot
	localCfg      LocalConfig
	localFallback bool
	logger        *zap.Logger
	closed        bool
}

// NewManager creates a Manager initially backed by LocalDriver without opening
// any files, directories, or sockets during construction.
func NewManager(cfg ManagerConfig) *Manager {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	localCfg := cfg.Local
	if localCfg.Logger == nil {
		localCfg.Logger = logger
	}
	local := NewLocalDriver(localCfg)
	return &Manager{
		slot: newManagerSlot(
			local,
			nil,
			nil,
			logger,
		),
		localCfg:      localCfg,
		localFallback: cfg.LocalFallback,
		logger:        logger,
	}
}

// ConfigureLocal switches the active primary store to a LocalDriver built from cfg.
func (m *Manager) ConfigureLocal(cfg LocalConfig) error {
	if cfg.Logger == nil {
		cfg.Logger = m.logger
	}
	local := NewLocalDriver(cfg)
	return m.swapWithConfig(swapOptions{
		primary:  local,
		localCfg: &cfg,
	})
}

// ConfigureS3 builds a new S3Driver from s3Cfg and atomically replaces the
// active driver, attaching a read-only LocalDriver fallback when localFallback is true.
func (m *Manager) ConfigureS3(
	s3Cfg S3Config,
	localCfg LocalConfig,
	localFallback bool,
) error {
	return m.ConfigureS3WithSpool(
		s3Cfg,
		localCfg,
		localFallback,
		"",
	)
}

// ConfigureS3WithSpool switches to S3Driver with optional local fallback and
// an optional local spool read driver for in-flight uploads under spoolDir.
func (m *Manager) ConfigureS3WithSpool(
	s3Cfg S3Config,
	localCfg LocalConfig,
	localFallback bool,
	spoolDir string,
) error {
	if s3Cfg.Logger == nil {
		s3Cfg.Logger = m.logger
	}
	if localCfg.Logger == nil {
		localCfg.Logger = m.logger
	}
	driver, err := NewS3Driver(s3Cfg)
	if err != nil {
		return err
	}
	var spool *LocalDriver
	if trimmed := strings.TrimSpace(spoolDir); trimmed != "" {
		spool = NewLocalDriver(LocalConfig{
			RootDir: trimmed,
			Logger:  m.logger,
		})
	}
	var fallback *LocalDriver
	if localFallback && strings.TrimSpace(localCfg.RootDir) != "" {
		fallback = NewLocalDriver(localCfg)
	}
	swapErr := m.swapWithConfig(swapOptions{
		primary:       driver,
		spool:         spool,
		fallback:      fallback,
		localCfg:      &localCfg,
		localFallback: localFallback,
	})
	if swapErr != nil {
		if spool != nil {
			if closeErr := spool.Close(); closeErr != nil {
				m.logger.Warn("close unused spool driver", zap.Error(closeErr))
			}
		}
		if fallback != nil {
			if closeErr := fallback.Close(); closeErr != nil {
				m.logger.Warn("close unused fallback driver", zap.Error(closeErr))
			}
		}
		return swapErr
	}
	return nil
}

// Swap atomically installs primary and optional fallback as the active stores,
// retiring the previous slot once all in-flight operations complete.
func (m *Manager) Swap(primary Store, fallback *LocalDriver) error {
	return m.swapWithConfig(swapOptions{
		primary:       primary,
		fallback:      fallback,
		localFallback: fallback != nil,
	})
}

type swapOptions struct {
	primary       Store
	spool         *LocalDriver
	fallback      *LocalDriver
	localCfg      *LocalConfig
	localFallback bool
}

func (m *Manager) swapWithConfig(opts swapOptions) error {
	if opts.primary == nil {
		return errors.New("primary filestore cannot be nil")
	}

	next := newManagerSlot(
		opts.primary,
		opts.spool,
		opts.fallback,
		m.logger,
	)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		next.retire()
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Filestore manager is closed", ErrStoreUnavailable)
	}
	prev := m.slot
	m.slot = next
	if opts.localCfg != nil {
		m.localCfg = *opts.localCfg
	}
	m.localFallback = opts.localFallback
	m.mu.Unlock()

	if prev != nil {
		prev.retire()
	}
	return nil
}

// Close permanently stops the manager and releases underlying LocalDriver handles.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	prev := m.slot
	m.slot = nil
	m.mu.Unlock()

	if prev != nil {
		prev.retire()
	}
	return nil
}

// Backend returns the active primary backend identifier ("local", "s3", or "memory").
func (m *Manager) Backend() string {
	slot, err := m.acquireSlot()
	if err != nil {
		return BackendLocal
	}
	defer slot.release()
	return slot.primary.Backend()
}

// LocalFallbackEnabled reports whether reads may fall back to LocalFS on remote miss.
func (m *Manager) LocalFallbackEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.localFallback
}

// Put writes object bytes to the currently active primary store.
func (m *Manager) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	opts PutOptions,
) error {
	slot, err := m.acquireSlot()
	if err != nil {
		return err
	}
	defer slot.release()
	return slot.primary.Put(
		ctx,
		key,
		r,
		opts,
	)
}

// PutFromLocalFile uploads or adopts a local file into the active primary store.
func (m *Manager) PutFromLocalFile(
	ctx context.Context,
	key string,
	srcPath string,
	opts PutOptions,
) error {
	slot, err := m.acquireSlot()
	if err != nil {
		return err
	}
	defer slot.release()
	return PutLocalFile(ctx, LocalFilePutInput{
		Store:      slot.primary,
		Key:        key,
		SourcePath: srcPath,
		Options:    opts,
	})
}

// Open opens key from spool (when queued locally on a remote backend), then active
// primary store, falling back to LocalDriver on not-found when fallback is enabled.
func (m *Manager) Open(ctx context.Context, key string) (ReadSeekCloser, ObjectInfo, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	defer slot.release()

	if slot.spool != nil && slot.primary.Backend() != BackendLocal {
		if rc, info, spoolErr := slot.spool.Open(ctx, key); spoolErr == nil {
			return rc, info, nil
		} else if !IsNotFound(spoolErr) && !IsUnavailable(spoolErr) {
			return nil, ObjectInfo{}, spoolErr
		}
	}

	rc, info, err := slot.primary.Open(ctx, key)
	if err == nil {
		return rc, info, nil
	}
	if slot.fallback != nil && slot.primary.Backend() != BackendLocal && IsNotFound(err) {
		return slot.fallback.Open(ctx, key)
	}
	return nil, ObjectInfo{}, err
}

// Stat fetches object metadata from spool first (while queued on remote), then
// primary, or falls back to LocalDriver on miss.
func (m *Manager) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return ObjectInfo{}, err
	}
	defer slot.release()

	if slot.spool != nil && slot.primary.Backend() != BackendLocal {
		if info, spoolErr := slot.spool.Stat(ctx, key); spoolErr == nil {
			return info, nil
		} else if !IsNotFound(spoolErr) && !IsUnavailable(spoolErr) {
			return ObjectInfo{}, spoolErr
		}
	}

	info, err := slot.primary.Stat(ctx, key)
	if err == nil {
		return info, nil
	}
	if slot.fallback != nil && slot.primary.Backend() != BackendLocal && IsNotFound(err) {
		return slot.fallback.Stat(ctx, key)
	}
	return ObjectInfo{}, err
}

// Exists checks whether key is readable via spool, primary, or optional local fallback.
func (m *Manager) Exists(ctx context.Context, key string) (bool, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return false, err
	}
	defer slot.release()

	if slot.spool != nil && slot.primary.Backend() != BackendLocal {
		if ok, spoolErr := slot.spool.Exists(ctx, key); spoolErr == nil && ok {
			return true, nil
		}
	}

	ok, err := slot.primary.Exists(ctx, key)
	if err != nil || ok {
		return ok, err
	}
	if slot.fallback != nil && slot.primary.Backend() != BackendLocal {
		return slot.fallback.Exists(ctx, key)
	}
	return false, nil
}

// ExistsInSpool reports whether key is currently buffered in local spool storage.
func (m *Manager) ExistsInSpool(ctx context.Context, key string) (bool, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return false, err
	}
	defer slot.release()
	if slot.spool == nil {
		return false, nil
	}
	ok, err := slot.spool.Exists(ctx, key)
	if IsUnavailable(err) {
		return false, nil
	}
	return ok, err
}

// ExistsInPrimary checks primary presence without consulting spool or local fallback.
func (m *Manager) ExistsInPrimary(ctx context.Context, key string) (bool, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return false, err
	}
	defer slot.release()
	return slot.primary.Exists(ctx, key)
}

// Delete removes key from primary and best-effort cleans any spool/fallback copies.
func (m *Manager) Delete(ctx context.Context, key string) error {
	slot, err := m.acquireSlot()
	if err != nil {
		return err
	}
	defer slot.release()

	if delErr := slot.primary.Delete(ctx, key); delErr != nil {
		return delErr
	}
	if slot.spool != nil && slot.primary.Backend() != BackendLocal {
		if spErr := slot.spool.Delete(ctx, key); spErr != nil && !IsNotFound(spErr) && !IsUnavailable(spErr) {
			m.logger.Warn(
				"delete spool object after primary delete failed",
				zap.String("key", key),
				zap.Error(spErr),
			)
		}
	}
	if slot.fallback != nil && slot.primary.Backend() != BackendLocal {
		if fbErr := slot.fallback.Delete(ctx, key); fbErr != nil && !IsNotFound(fbErr) && !IsUnavailable(fbErr) {
			m.logger.Warn(
				"delete fallback local object after primary delete failed",
				zap.String("key", key),
				zap.Error(fbErr),
			)
		}
	}
	return nil
}

// Health inspects health of the active primary driver.
func (m *Manager) Health(ctx context.Context) (HealthStatus, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return HealthStatus{
			Backend:   BackendLocal,
			Available: false,
			Message:   "filestore manager closed",
		}, err
	}
	defer slot.release()
	return slot.primary.Health(ctx)
}

// SupportsPresign reports whether the active primary driver currently supports
// presigned URL generation.
func (m *Manager) SupportsPresign() bool {
	slot, err := m.acquireSlot()
	if err != nil {
		return false
	}
	defer slot.release()

	if cap, ok := slot.primary.(PresignCapable); ok {
		return cap.SupportsPresign()
	}
	_, ok := slot.primary.(Presigner)
	return ok
}

// PresignGet delegates presigned GET generation when active primary supports Presigner.
func (m *Manager) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return "", err
	}
	defer slot.release()

	presigner, ok := slot.primary.(Presigner)
	if !ok {
		return "", apperr.Invalid(
			"PRESIGN_UNSUPPORTED",
			"Active storage backend does not support presigned URLs",
			ErrPresignUnsupported,
		)
	}
	return presigner.PresignGet(ctx, key, ttl)
}

// EnsureProtectionMarker forwards sidecar lock creation only when supported by primary.
func (m *Manager) EnsureProtectionMarker(ctx context.Context, key string) error {
	slot, err := m.acquireSlot()
	if err != nil {
		return err
	}
	defer slot.release()

	markers, ok := slot.primary.(ProtectionMarkerStore)
	if !ok {
		return nil
	}
	return markers.EnsureProtectionMarker(ctx, key)
}

// RemoveProtectionMarker forwards sidecar lock removal only when supported by primary.
func (m *Manager) RemoveProtectionMarker(ctx context.Context, key string) error {
	slot, err := m.acquireSlot()
	if err != nil {
		return err
	}
	defer slot.release()

	markers, ok := slot.primary.(ProtectionMarkerStore)
	if !ok {
		return nil
	}
	return markers.RemoveProtectionMarker(ctx, key)
}

// HasProtectionMarker checks sidecar lock state on local stores; remote stores return false.
func (m *Manager) HasProtectionMarker(ctx context.Context, key string) (bool, error) {
	slot, err := m.acquireSlot()
	if err != nil {
		return false, err
	}
	defer slot.release()

	markers, ok := slot.primary.(ProtectionMarkerStore)
	if !ok {
		return false, nil
	}
	return markers.HasProtectionMarker(ctx, key)
}

// LocalRoot returns the active local media root directory or configured local root.
func (m *Manager) LocalRoot() string {
	slot, err := m.acquireSlot()
	if err != nil {
		m.mu.RLock()
		root := m.localCfg.RootDir
		m.mu.RUnlock()
		return root
	}
	defer slot.release()

	if inspector, ok := slot.primary.(LocalRootInspector); ok {
		return inspector.LocalRoot()
	}
	if slot.fallback != nil {
		return slot.fallback.LocalRoot()
	}
	m.mu.RLock()
	root := m.localCfg.RootDir
	m.mu.RUnlock()
	return root
}

// HasNamespaceDir checks namespace directory readiness for local stores and
// always returns true for remote object-prefix stores.
func (m *Manager) HasNamespaceDir(ctx context.Context, namespace string) bool {
	slot, err := m.acquireSlot()
	if err != nil {
		return false
	}
	defer slot.release()

	if inspector, ok := slot.primary.(LocalRootInspector); ok {
		return inspector.HasNamespaceDir(ctx, namespace)
	}
	return true
}

func (m *Manager) acquireSlot() (*managerSlot, error) {
	m.mu.RLock()
	slot := m.slot
	closed := m.closed
	m.mu.RUnlock()

	if closed || slot == nil || !slot.acquire() {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media store is unavailable", ErrStoreUnavailable)
	}
	return slot, nil
}
