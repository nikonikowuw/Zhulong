package filestore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

type memoryObject struct {
	data        []byte
	contentType string
	modTime     time.Time
	protected   bool
}

type memoryReadSeekCloser struct {
	*bytes.Reader
}

func (m *memoryReadSeekCloser) Close() error {
	return nil
}

// MemoryDriver is an in-memory Store, Presigner, and ProtectionMarkerStore
// designed for deterministic unit testing without disk or network I/O.
type MemoryDriver struct {
	mu          sync.RWMutex
	objects     map[string]memoryObject
	unavailable bool
	presignBase string
	now         func() time.Time
}

// NewMemoryDriver creates an empty in-memory filestore driver.
func NewMemoryDriver() *MemoryDriver {
	return &MemoryDriver{
		objects: map[string]memoryObject{},
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// SetUnavailable toggles simulated backend unavailability for Health/read/write tests.
func (m *MemoryDriver) SetUnavailable(unavailable bool) {
	m.mu.Lock()
	m.unavailable = unavailable
	m.mu.Unlock()
}

// SetPresignBase enables PresignGet URL synthesis for tests.
func (m *MemoryDriver) SetPresignBase(baseURL string) {
	m.mu.Lock()
	m.presignBase = baseURL
	m.mu.Unlock()
}

// Backend returns BackendMemory.
func (m *MemoryDriver) Backend() string {
	return BackendMemory
}

// Put stores a copy of r in memory under key.
func (m *MemoryDriver) Put(
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

	payload, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read object payload %q: %w", cleanKey, err)
	}

	ct := opts.ContentType
	if ct == "" {
		ct = ContentTypeForKey(cleanKey)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unavailable {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media store is unavailable", ErrStoreUnavailable)
	}

	prev, exists := m.objects[cleanKey]
	protected := opts.Protected || (exists && prev.protected)
	m.objects[cleanKey] = memoryObject{
		data:        payload,
		contentType: ct,
		modTime:     m.now().UTC(),
		protected:   protected,
	}
	return nil
}

// Open returns a seekable reader and metadata for key.
func (m *MemoryDriver) Open(
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

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavailable {
		return nil, ObjectInfo{}, apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media store is unavailable",
			ErrStoreUnavailable,
		)
	}

	obj, ok := m.objects[cleanKey]
	if !ok {
		return nil, ObjectInfo{}, apperr.NotFound("MEDIA_ENTRY_NOT_FOUND", "Media entry not found", ErrNotFound)
	}

	copied := make([]byte, len(obj.data))
	copy(copied, obj.data)
	return &memoryReadSeekCloser{Reader: bytes.NewReader(copied)}, ObjectInfo{
		Key:         cleanKey,
		Name:        path.Base(cleanKey),
		SizeBytes:   int64(len(obj.data)),
		ModifiedAt:  obj.modTime,
		ContentType: obj.contentType,
	}, nil
}

// Stat returns metadata for key without copying bytes.
func (m *MemoryDriver) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	cleanKey, err := ValidateKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavailable {
		return ObjectInfo{}, apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"Media store is unavailable",
			ErrStoreUnavailable,
		)
	}

	obj, ok := m.objects[cleanKey]
	if !ok {
		return ObjectInfo{}, apperr.NotFound("MEDIA_ENTRY_NOT_FOUND", "Media entry not found", ErrNotFound)
	}

	return ObjectInfo{
		Key:         cleanKey,
		Name:        path.Base(cleanKey),
		SizeBytes:   int64(len(obj.data)),
		ModifiedAt:  obj.modTime,
		ContentType: obj.contentType,
	}, nil
}

// Delete removes key idempotently.
func (m *MemoryDriver) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unavailable {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media store is unavailable", ErrStoreUnavailable)
	}

	delete(m.objects, cleanKey)
	return nil
}

// Exists reports whether key is stored.
func (m *MemoryDriver) Exists(ctx context.Context, key string) (bool, error) {
	_, err := m.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// Health returns availability of the memory store.
func (m *MemoryDriver) Health(ctx context.Context) (HealthStatus, error) {
	if err := ctx.Err(); err != nil {
		return HealthStatus{Backend: BackendMemory}, err
	}
	m.mu.RLock()
	available := !m.unavailable
	m.mu.RUnlock()

	msg := ""
	if !available {
		msg = "memory store marked unavailable"
	}
	return HealthStatus{
		Backend:   BackendMemory,
		Available: available,
		Message:   msg,
	}, nil
}

// SupportsPresign reports whether presignBase is configured on this test driver.
func (m *MemoryDriver) SupportsPresign() bool {
	m.mu.RLock()
	base := m.presignBase
	m.mu.RUnlock()
	return strings.TrimSpace(base) != ""
}

// PresignGet issues a synthetic presigned URL when presignBase is configured.
func (m *MemoryDriver) PresignGet(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return "", err
	}

	m.mu.RLock()
	base := m.presignBase
	unavailable := m.unavailable
	m.mu.RUnlock()

	if unavailable {
		return "", apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media store is unavailable", ErrStoreUnavailable)
	}
	if strings.TrimSpace(base) == "" {
		return "", apperr.Invalid(
			"PRESIGN_UNSUPPORTED",
			"Presign base URL is not configured",
			ErrPresignUnsupported,
		)
	}

	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse presign base URL: %w", err)
	}
	u.Path = path.Join(u.Path, cleanKey)
	q := u.Query()
	q.Set("ttl", ttl.String())
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// EnsureProtectionMarker marks key as protected in memory.
func (m *MemoryDriver) EnsureProtectionMarker(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj, ok := m.objects[cleanKey]; ok {
		obj.protected = true
		m.objects[cleanKey] = obj
	}
	return nil
}

// RemoveProtectionMarker clears key protection in memory.
func (m *MemoryDriver) RemoveProtectionMarker(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj, ok := m.objects[cleanKey]; ok {
		obj.protected = false
		m.objects[cleanKey] = obj
	}
	return nil
}

// HasProtectionMarker reports whether key is marked protected in memory.
func (m *MemoryDriver) HasProtectionMarker(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj, ok := m.objects[cleanKey]
	return ok && obj.protected, nil
}
