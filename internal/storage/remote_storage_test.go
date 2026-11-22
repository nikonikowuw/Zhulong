package storage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type testAESGCMCipher struct {
	mu          sync.RWMutex
	initialized bool
	aead        cipher.AEAD
}

func newTestAESGCMCipher(t *testing.T, initialized bool) *testAESGCMCipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("read random key: %v", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new aes cipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new gcm: %v", err)
	}

	return &testAESGCMCipher{
		initialized: initialized,
		aead:        aead,
	}
}

func (c *testAESGCMCipher) setInitialized(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initialized = v
}

func (c *testAESGCMCipher) Encrypt(plaintext []byte, aad string) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.initialized {
		return nil, errors.New("cipher not initialized")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := c.aead.Seal(
		nonce,
		nonce,
		plaintext,
		[]byte(aad),
	)
	return sealed, nil
}

func (c *testAESGCMCipher) Decrypt(ciphertext []byte, aad string) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.initialized {
		return nil, errors.New("cipher not initialized")
	}
	ns := c.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return c.aead.Open(
		nil,
		ciphertext[:ns],
		ciphertext[ns:],
		[]byte(aad),
	)
}

func openStorageTestDB(t *testing.T) *database.Store {
	t.Helper()
	store := database.New(t.TempDir(), zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open and migrate test db: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("close test db: %v", err)
		}
	})
	return store
}

func baseValidS3StorageConfig(spoolDir string) StorageConfig {
	cfg := DefaultStorageConfig()
	cfg.Backend = BackendS3
	cfg.DeliveryMode = DeliveryModeRedirect
	cfg.SpoolDirectory = spoolDir
	cfg.SpoolMaxMB = 256
	cfg.S3 = &S3Config{
		Endpoint:     "https://minio.example.com:9000",
		Region:       "us-east-1",
		Bucket:       "zhulong-media",
		Prefix:       "/edge-node-1/",
		UsePathStyle: true,
		AccessKeyID:  "AKIAEXAMPLE123",
	}
	return cfg
}

func TestValidateConfig_RemoteAndSpool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		mutate    func(*StorageConfig)
		expectErr string
		check     func(*testing.T, StorageConfig)
	}{
		{
			name: "defaults applied for empty new fields",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = ""
				cfg.DeliveryMode = ""
				cfg.SpoolDirectory = ""
				cfg.SpoolMaxMB = 0
			},
			check: func(t *testing.T, cfg StorageConfig) {
				if cfg.Backend != BackendLocal || cfg.DeliveryMode != DeliveryModeProxy ||
					cfg.SpoolDirectory != DefaultSpoolDirectory || cfg.SpoolMaxMB != DefaultSpoolMaxMB {
					t.Fatalf("unexpected normalized defaults: %+v", cfg)
				}
			},
		},
		{
			name: "valid s3 normalizes URL scheme and slashes",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = " S3 "
				cfg.DeliveryMode = "REDIRECT"
				cfg.SpoolDirectory = "/var/spool/zhulong/"
				cfg.SpoolMaxMB = 512
				cfg.S3 = &S3Config{
					Endpoint:    "https://oss-cn-hangzhou.aliyuncs.com/",
					Region:      "",
					Bucket:      "prod-bucket",
					Prefix:      "/site-a/cam/",
					AccessKeyID: "AKID",
				}
			},
			check: func(t *testing.T, cfg StorageConfig) {
				if cfg.Backend != BackendS3 || cfg.DeliveryMode != DeliveryModeRedirect || cfg.S3 == nil {
					t.Fatalf("unexpected normalized backend/delivery: %+v", cfg)
				}
				if !cfg.S3.UseTLS || cfg.S3.Region != "us-east-1" || cfg.S3.Prefix != "site-a/cam" {
					t.Fatalf("unexpected normalized s3 config: %+v", cfg.S3)
				}
			},
		},
		{
			name: "unsupported backend rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = "ftp"
			},
			expectErr: "unsupported storage backend",
		},
		{
			name: "unsupported delivery mode rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.DeliveryMode = "cdn"
			},
			expectErr: "unsupported delivery mode",
		},
		{
			name: "spool max below 128 MB rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.SpoolMaxMB = 64
			},
			expectErr: "spool max MB",
		},
		{
			name: "s3 missing object config rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = BackendS3
				cfg.S3 = nil
			},
			expectErr: "s3 configuration is required",
		},
		{
			name: "s3 endpoint with path rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = BackendS3
				cfg.S3 = &S3Config{
					Endpoint:    "https://s3.example.com/custom-path",
					Bucket:      "b",
					AccessKeyID: "k",
				}
			},
			expectErr: "must not include a path",
		},
		{
			name: "s3 invalid scheme rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = BackendS3
				cfg.S3 = &S3Config{
					Endpoint:    "ftp://s3.example.com",
					Bucket:      "b",
					AccessKeyID: "k",
				}
			},
			expectErr: "scheme must be http or https",
		},
		{
			name: "s3 negative quota rejected",
			mutate: func(cfg *StorageConfig) {
				cfg.Backend = BackendS3
				cfg.S3 = &S3Config{
					Endpoint:    "http://127.0.0.1:9000",
					Bucket:      "b",
					AccessKeyID: "k",
					QuotaGB:     -1,
				}
			},
			expectErr: "quotaGb must be non-negative",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultStorageConfig()
			tc.mutate(&cfg)
			err := ValidateConfig(&cfg)
			if tc.expectErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectErr) {
					t.Fatalf("expected error containing %q, got %v", tc.expectErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected ValidateConfig error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

func TestRepository_S3SecretEncryptionAndPreservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbStore := openStorageTestDB(t)
	cipherImpl := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipherImpl, zap.NewNop())

	aware, ok := repo.(SecretAwareRepository)
	if !ok {
		t.Fatalf("expected SecretAwareRepository")
	}

	hasData, err := aware.HasExistingEncryptedData(ctx)
	if err != nil || hasData {
		t.Fatalf("expected no encrypted data initially, got hasData=%v err=%v", hasData, err)
	}

	cfgWithoutSecret := baseValidS3StorageConfig(t.TempDir())
	if _, err = repo.UpdateConfig(ctx, cfgWithoutSecret); !errors.Is(err, ErrS3SecretRequired) {
		t.Fatalf("expected ErrS3SecretRequired, got %v", err)
	}

	const initialSecret = "super-secret-minio-key-01"
	cfg := baseValidS3StorageConfig(t.TempDir())
	cfg.S3.SecretAccessKey = initialSecret

	saved, err := repo.UpdateConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if saved.S3 == nil || saved.S3.SecretAccessKey != "" || !saved.S3.HasSecretAccessKey {
		t.Fatalf("expected sanitized secret in returned config, got %+v", saved.S3)
	}

	hasData, err = aware.HasExistingEncryptedData(ctx)
	if err != nil || !hasData {
		t.Fatalf("expected encrypted data after save, got hasData=%v err=%v", hasData, err)
	}

	row, err := gorm.G[systemMetadataRow](dbStore.DB()).Where("key = ?", metadataKeyStorageConfig).First(ctx)
	if err != nil {
		t.Fatalf("query stored metadata row: %v", err)
	}
	if strings.Contains(row.Value, initialSecret) {
		t.Fatalf("plaintext secret must never be persisted in SQLite row")
	}

	gotCfg, err := repo.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if gotCfg.S3 == nil || gotCfg.S3.SecretAccessKey != "" || !gotCfg.S3.HasSecretAccessKey {
		t.Fatalf("expected stripped secret on GetConfig, got %+v", gotCfg.S3)
	}

	decrypted, err := aware.GetDecryptedS3Config(ctx)
	if err != nil {
		t.Fatalf("GetDecryptedS3Config failed: %v", err)
	}
	if decrypted.SecretAccessKey != initialSecret || decrypted.Prefix != "edge-node-1" || !decrypted.UseTLS {
		t.Fatalf("unexpected decrypted s3 config: %+v", decrypted)
	}

	// Update non-secret field while omitting SecretAccessKey preserves existing secret.
	cfgPreserve := *gotCfg
	s3Copy := *gotCfg.S3
	s3Copy.SecretAccessKey = ""
	s3Copy.Bucket = "zhulong-media-v2"
	cfgPreserve.S3 = &s3Copy

	saved2, err := repo.UpdateConfig(ctx, cfgPreserve)
	if err != nil {
		t.Fatalf("UpdateConfig preserve failed: %v", err)
	}
	if !saved2.S3.HasSecretAccessKey || saved2.S3.SecretAccessKey != "" {
		t.Fatalf("unexpected s3 metadata after preserve: %+v", saved2.S3)
	}

	decrypted2, err := aware.GetDecryptedS3Config(ctx)
	if err != nil {
		t.Fatalf("GetDecryptedS3Config after preserve failed: %v", err)
	}
	if decrypted2.Bucket != "zhulong-media-v2" || decrypted2.SecretAccessKey != initialSecret {
		t.Fatalf("expected preserved secret, got %+v", decrypted2)
	}

	// Explicitly supplying a new secret rotates ciphertext.
	const rotatedSecret = "rotated-secret-key-99"
	s3Rotate := *saved2.S3
	s3Rotate.SecretAccessKey = rotatedSecret
	cfgPreserve.S3 = &s3Rotate
	if _, err = repo.UpdateConfig(ctx, cfgPreserve); err != nil {
		t.Fatalf("UpdateConfig rotate failed: %v", err)
	}

	decrypted3, err := aware.GetDecryptedS3Config(ctx)
	if err != nil {
		t.Fatalf("GetDecryptedS3Config after rotate failed: %v", err)
	}
	if decrypted3.SecretAccessKey != rotatedSecret {
		t.Fatalf("expected rotated secret, got %q", decrypted3.SecretAccessKey)
	}
}

func TestEmergencyGate_EvaluateSpool(t *testing.T) {
	t.Parallel()

	gate := NewEmergencyGate(zap.NewNop())
	var stops []string
	var resumes int
	var transitions []bool
	gate.SetCallbacks(
		func(reason string) { stops = append(stops, reason) },
		func() { resumes++ },
	)
	gate.RegisterObserver(func(blocked bool) {
		transitions = append(transitions, blocked)
	})

	cfg := DefaultStorageConfig()
	cfg.Backend = BackendS3
	cfg.SpoolMaxMB = 128

	stats := &FSStats{
		MountPoint: "/",
		FSType:     "ext4",
		TotalBytes: 100 << 30,
		FreeBytes:  80 << 30,
		UsedBytes:  20 << 30,
	}

	health0 := gate.EvaluateSpool(
		stats,
		&cfg,
		10<<20,
		false,
	)
	if health0 != StatusHealthy || !gate.CanWrite() {
		t.Fatalf("expected healthy writable gate, got health=%s canWrite=%v", health0, gate.CanWrite())
	}

	// 85% of 128 MiB -> StatusWarning, writes still allowed.
	healthWarn := gate.EvaluateSpool(
		stats,
		&cfg,
		110<<20,
		false,
	)
	if healthWarn != StatusWarning || !gate.CanWrite() {
		t.Fatalf("expected warning writable gate, got health=%s canWrite=%v", healthWarn, gate.CanWrite())
	}

	// >= 128 MiB -> EmergencyStopped, stop callback fires once.
	health := gate.EvaluateSpool(
		stats,
		&cfg,
		128<<20,
		false,
	)
	if health != StatusEmergencyStopped || gate.CanWrite() || !strings.Contains(gate.Reason(), "spool") {
		t.Fatalf(
			"expected spool emergency stop, got health=%s canWrite=%v reason=%q",
			health,
			gate.CanWrite(),
			gate.Reason(),
		)
	}

	// Repeated check stays stopped without duplicating edge callbacks.
	health = gate.EvaluateSpool(
		stats,
		&cfg,
		130<<20,
		false,
	)
	if health != StatusEmergencyStopped || len(stops) != 1 {
		t.Fatalf("expected single stop callback on steady emergency state, got health=%s stops=%d", health, len(stops))
	}

	// Recover below threshold.
	health = gate.EvaluateSpool(
		stats,
		&cfg,
		16<<20,
		false,
	)
	if health != StatusHealthy || !gate.CanWrite() || resumes != 1 ||
		len(transitions) != 2 || !transitions[0] || transitions[1] {
		t.Fatalf(
			"unexpected recovery state: health=%s canWrite=%v resumes=%d transitions=%v",
			health,
			gate.CanWrite(),
			resumes,
			transitions,
		)
	}
}

func TestService_RefreshStatus_S3Mode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbStore := openStorageTestDB(t)
	spoolDir := t.TempDir()

	cipherImpl := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipherImpl, zap.NewNop())

	cfg := baseValidS3StorageConfig(spoolDir)
	cfg.SpoolMaxMB = 128
	cfg.S3.SecretAccessKey = "test-secret"
	if _, err := repo.UpdateConfig(ctx, cfg); err != nil {
		t.Fatalf("seed s3 config: %v", err)
	}

	// Simulate early boot before camera.InitCipher finishes.
	cipherImpl.setInitialized(false)

	healthCalls := 0
	svc := NewService(ServiceDeps{
		Repo:   repo,
		Cipher: cipherImpl,
		S3Health: func(_ context.Context, _ filestore.S3Config) (filestore.HealthStatus, error) {
			healthCalls++
			return filestore.HealthStatus{
				Backend:   filestore.BackendS3,
				Available: true,
				Message:   "ok",
			}, nil
		},
		Logger: zap.NewNop(),
	})

	status, err := svc.RefreshStatus(ctx)
	if err != nil {
		t.Fatalf("RefreshStatus before cipher init failed: %v", err)
	}
	if status.Backend != BackendS3 || status.RemoteHealthy ||
		status.RemoteError != "storage encryption key initializing" || healthCalls != 0 {
		t.Fatalf("unexpected status before cipher init: %+v healthCalls=%d", status, healthCalls)
	}

	// After cipher unlocks, S3Health reports remote healthy.
	cipherImpl.setInitialized(true)
	status, err = svc.RefreshStatus(ctx)
	if err != nil {
		t.Fatalf("RefreshStatus after cipher init failed: %v", err)
	}
	if !status.RemoteHealthy || status.Status != StatusHealthy || !status.CanWrite || healthCalls != 1 {
		t.Fatalf("unexpected healthy status after cipher init: %+v healthCalls=%d", status, healthCalls)
	}

	// Writing a file >= SpoolMaxMB (128 MiB sparse file) engages emergency stop.
	spoolFilePath := filepath.Join(spoolDir, "pending.mp4")
	f, err := os.Create(spoolFilePath)
	if err != nil {
		t.Fatalf("create sparse spool file: %v", err)
	}
	if err := f.Truncate(128 << 20); err != nil {
		t.Fatalf("truncate sparse spool file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close sparse spool file: %v", err)
	}

	status, err = svc.RefreshStatus(ctx)
	if err != nil {
		t.Fatalf("RefreshStatus during spool overflow failed: %v", err)
	}
	if status.Status != StatusEmergencyStopped || status.CanWrite || status.SpoolUsedBytes < 128<<20 {
		t.Fatalf("expected spool overflow emergency stop, got %+v", status)
	}

	if err := os.Remove(spoolFilePath); err != nil {
		t.Fatalf("remove sparse spool file: %v", err)
	}
	status, err = svc.RefreshStatus(ctx)
	if err != nil {
		t.Fatalf("RefreshStatus after spool recovery failed: %v", err)
	}
	if status.Status != StatusHealthy || !status.CanWrite {
		t.Fatalf("expected recovery after removing spool file, got %+v", status)
	}
}

func TestService_UpdateConfig_S3AndAuditSanitization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbStore := openStorageTestDB(t)
	cipherImpl := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipherImpl, zap.NewNop())

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.DefaultConfig())
	if err := auditSvc.Start(ctx); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() {
		if err := auditSvc.Stop(context.Background()); err != nil {
			t.Logf("stop audit service: %v", err)
		}
	})

	var probeErr error
	var probedSecret string
	svc := NewService(ServiceDeps{
		Repo:   repo,
		Cipher: cipherImpl,
		S3Probe: func(_ context.Context, s3Cfg filestore.S3Config) error {
			probedSecret = s3Cfg.SecretAccessKey
			return probeErr
		},
		S3Health: func(_ context.Context, _ filestore.S3Config) (filestore.HealthStatus, error) {
			return filestore.HealthStatus{Backend: filestore.BackendS3, Available: true}, nil
		},
		Logger: zap.NewNop(),
	})
	svc.SetAuditor(auditSvc)

	var listenerCalls int
	var lastNotifiedBackend string
	svc.SetConfigChangeListener(func(_ context.Context, updated *StorageConfig) {
		listenerCalls++
		lastNotifiedBackend = updated.Backend
	})

	spoolDir := t.TempDir()
	const secretVal = "audit-must-not-contain-this-secret"

	// Unreachable S3 rejects update without invoking listener or saving config.
	probeErr = errors.New("dial tcp 10.0.0.2:9000: connection refused")
	badCfg := baseValidS3StorageConfig(spoolDir)
	badCfg.S3.SecretAccessKey = secretVal
	if _, err := svc.UpdateConfig(
		ctx,
		badCfg,
		"admin",
		"127.0.0.1",
	); err == nil {
		t.Fatalf("expected error on unreachable S3 probe")
	}
	if listenerCalls != 0 {
		t.Fatalf("expected 0 listener calls on failed update, got %d", listenerCalls)
	}

	persisted, err := svc.GetConfig(ctx)
	if err != nil || persisted.Backend != BackendLocal {
		t.Fatalf("expected persisted backend to remain local, got %+v err=%v", persisted, err)
	}

	// Missing secret rejects switch when nothing stored yet.
	probeErr = nil
	noSecretCfg := baseValidS3StorageConfig(spoolDir)
	if _, err = svc.UpdateConfig(
		ctx,
		noSecretCfg,
		"admin",
		"127.0.0.1",
	); err == nil {
		t.Fatalf("expected error when S3 secret missing and not stored")
	}

	// Valid reachable S3 config saves cleanly, notifies listener, and omits secrets in audit.
	goodCfg := baseValidS3StorageConfig(spoolDir)
	goodCfg.MediaDirectory = t.TempDir()
	goodCfg.S3.SecretAccessKey = secretVal

	status, err := svc.UpdateConfig(
		ctx,
		goodCfg,
		"admin",
		"127.0.0.1",
	)
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if probedSecret != secretVal || status.Backend != BackendS3 || !status.RemoteHealthy ||
		listenerCalls != 1 || lastNotifiedBackend != BackendS3 {
		t.Fatalf(
			"unexpected update result: status=%+v probed=%q listenerCalls=%d last=%q",
			status,
			probedSecret,
			listenerCalls,
			lastNotifiedBackend,
		)
	}

	// Flush audit queue and confirm secret does not appear in audit records.
	if err := auditSvc.Stop(ctx); err != nil {
		t.Fatalf("flush audit service: %v", err)
	}
	logs, total, err := audit.NewStore(dbStore).List(ctx, audit.Filter{Action: ActionStorageUpdateConfig, Limit: 10})
	if err != nil || total < 1 {
		t.Fatalf("expected >=1 audit record, got total=%d err=%v", total, err)
	}
	for _, entry := range logs {
		if strings.Contains(entry.Detail, secretVal) {
			t.Fatalf("audit detail leaked S3 secret: %q", entry.Detail)
		}
		if !strings.Contains(entry.Detail, "backend=s3") {
			t.Fatalf("expected backend=s3 in audit detail, got %q", entry.Detail)
		}
	}
}

func TestService_TestConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbStore := openStorageTestDB(t)
	cipherImpl := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipherImpl, zap.NewNop())

	var probeErr error
	var seenSecret string
	svc := NewService(ServiceDeps{
		Repo:   repo,
		Cipher: cipherImpl,
		S3Probe: func(_ context.Context, cfg filestore.S3Config) error {
			seenSecret = cfg.SecretAccessKey
			return probeErr
		},
		Logger: zap.NewNop(),
	})

	// Local test connection succeeds on writable directory.
	localDir := t.TempDir()
	localRes, err := svc.TestConnection(
		ctx,
		ConnectionTestRequest{Backend: BackendLocal, Path: localDir},
		"admin",
		"127.0.0.1",
	)
	if err != nil || !localRes.Reachable || !localRes.Writable {
		t.Fatalf("expected reachable local dir, got res=%+v err=%v", localRes, err)
	}

	// Save encrypted S3 secret first so empty secret in subsequent test reuses it.
	storedCfg := baseValidS3StorageConfig(t.TempDir())
	storedCfg.S3.SecretAccessKey = "persisted-s3-secret"
	if _, err = repo.UpdateConfig(ctx, storedCfg); err != nil {
		t.Fatalf("seed stored s3 config: %v", err)
	}

	s3Req := ConnectionTestRequest{
		Backend: BackendS3,
		S3: &S3Config{
			Endpoint:    "http://127.0.0.1:9000",
			Bucket:      "zhulong-media",
			AccessKeyID: "AKIAEXAMPLE123",
		},
	}
	res, err := svc.TestConnection(
		ctx,
		s3Req,
		"admin",
		"127.0.0.1",
	)
	if err != nil || !res.Reachable || !res.Writable || seenSecret != "persisted-s3-secret" {
		t.Fatalf(
			"expected reachable s3 test reusing persisted secret, got res=%+v seen=%q err=%v",
			res,
			seenSecret,
			err,
		)
	}

	// Failing probe surfaces Reachable=false without turning into HTTP 500 error.
	probeErr = errors.New("access denied")
	resFail, err := svc.TestConnection(
		ctx,
		s3Req,
		"admin",
		"127.0.0.1",
	)
	if err != nil || resFail.Reachable || resFail.Writable || !strings.Contains(resFail.ErrorReason, "access denied") {
		t.Fatalf("expected non-error Reachable=false response, got res=%+v err=%v", resFail, err)
	}
}
