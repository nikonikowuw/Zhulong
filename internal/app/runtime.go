package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"

	"github.com/nikonikowuw/Zhulong/internal/nativelog"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// regionConfigReloader is the narrow view of the region config cache that the startup
// sequence needs. Depending on the interface rather than the concrete cache keeps
// lifecycleRuntime decoupled from the task domain's implementation.
type regionConfigReloader interface {
	Reload(ctx context.Context) error
}

// regionConfigReloaderProvider adapts the concrete cache to the narrow interface above.
func regionConfigReloaderProvider(cache *task.RegionConfigCache) regionConfigReloader {
	return cache
}

type databaseLifecycle interface {
	OpenAndMigrate(context.Context) error
	Close() error
}

type engineLifecycle interface {
	Start() error
	Close() error
}

type nativeLogEngineLifecycle interface {
	SetNativeLogSink(*nativelog.Queue) error
	Stop() error
	CloseNativeLogSink() error
}

type cameraLifecycle interface {
	InitCipher(context.Context) error
	Start(context.Context) error
	Stop()
}

type auditLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type networkLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type systemTimeLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type storageLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type algorithmLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type personLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type recordLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type lifecycleRuntime struct {
	database    databaseLifecycle
	native      engineLifecycle
	nativeLog   *nativelog.Queue
	camera      cameraLifecycle
	audit       auditLifecycle
	network     networkLifecycle
	systemTime  systemTimeLifecycle
	storage     storageLifecycle
	algorithm   algorithmLifecycle
	person      personLifecycle
	record      recordLifecycle
	configCache regionConfigReloader
	server      *http.Server
	logger      *zap.Logger
	config      Config
	logLevel    zap.AtomicLevel
	logSink     loggerOutput
	levelReload *logLevelReloader
	listen      func(string, string) (net.Listener, error)
	shutdown    func(context.Context) error

	listener            net.Listener
	serveDone           chan error
	databaseReady       bool
	nativeReady         bool
	nativeLogReady      bool
	nativeLogRegistered bool
	cameraReady         bool
	auditReady          bool
	networkReady        bool
	systemTimeReady     bool
	storageReady        bool
	algorithmReady      bool
	personReady         bool
	recordReady         bool
	httpReady           bool
	loggerSynced        bool
	startAttempted      bool
}

type runtimeParams struct {
	fx.In

	Database    databaseLifecycle
	Native      engineLifecycle
	NativeLog   *nativelog.Queue
	Camera      cameraLifecycle      `optional:"true"`
	Audit       auditLifecycle       `optional:"true"`
	Network     networkLifecycle     `optional:"true"`
	SystemTime  systemTimeLifecycle  `optional:"true"`
	Storage     storageLifecycle     `optional:"true"`
	Algorithm   algorithmLifecycle   `optional:"true"`
	Person      personLifecycle      `optional:"true"`
	Record      recordLifecycle      `optional:"true"`
	ConfigCache regionConfigReloader `optional:"true"`
	Server      *http.Server
	Logger      *zap.Logger
	Config      Config
	LogLevel    zap.AtomicLevel
	LogSink     loggerOutput
}

func newLifecycleRuntime(p runtimeParams) *lifecycleRuntime {
	var shutdown func(context.Context) error
	if p.Server != nil {
		shutdown = p.Server.Shutdown
	}
	return &lifecycleRuntime{
		database:    p.Database,
		native:      p.Native,
		nativeLog:   p.NativeLog,
		camera:      p.Camera,
		audit:       p.Audit,
		network:     p.Network,
		systemTime:  p.SystemTime,
		storage:     p.Storage,
		algorithm:   p.Algorithm,
		person:      p.Person,
		record:      p.Record,
		configCache: p.ConfigCache,
		server:      p.Server,
		logger:      p.Logger,
		config:      p.Config,
		logLevel:    p.LogLevel,
		logSink:     p.LogSink,
		listen:      net.Listen,
		shutdown:    shutdown,
	}
}

func (r *lifecycleRuntime) Start(ctx context.Context) error {
	if r.startAttempted {
		return errors.New("application is already started")
	}
	r.startAttempted = true
	if r.databaseReady || r.nativeReady || r.httpReady {
		return errors.New("application is already started")
	}
	if r.logSink != nil {
		if err := r.logSink.Prepare(); err != nil {
			return r.failStart(fmt.Errorf("prepare application log sink: %w", err))
		}
	}
	if err := r.database.OpenAndMigrate(ctx); err != nil {
		startupErr := fmt.Errorf("open database and apply migrations: %w", err)
		return r.failStart(startupErr, r.database.Close())
	}
	r.databaseReady = true

	if r.configCache != nil {
		if err := r.configCache.Reload(ctx); err != nil {
			r.logger.Warn("initial region config cache reload warning", zap.Error(err))
		}
	}

	if r.audit != nil {
		if err := r.audit.Start(ctx); err != nil {
			startupErr := fmt.Errorf("start audit service: %w", err)
			return r.failStart(startupErr, r.closeDatabase())
		}
		r.auditReady = true
	}

	if r.network != nil {
		if err := r.network.Start(ctx); err != nil {
			r.logger.Warn("network boot check warning", zap.Error(err))
		}
		r.networkReady = true
	}

	if r.systemTime != nil {
		if err := r.systemTime.Start(ctx); err != nil {
			r.logger.Warn("system time start warning", zap.Error(err))
		}
		r.systemTimeReady = true
	}

	if r.storage != nil {
		if err := r.storage.Start(ctx); err != nil {
			r.logger.Warn("storage start warning", zap.Error(err))
		}
		r.storageReady = true
	}

	if r.algorithm != nil {
		if err := r.algorithm.Start(ctx); err != nil {
			r.logger.Warn("algorithm start warning", zap.Error(err))
		}
		r.algorithmReady = true
	}

	if r.person != nil {
		if err := r.person.Start(ctx); err != nil {
			r.logger.Warn("person start warning", zap.Error(err))
		}
		r.personReady = true
	}

	if r.record != nil {
		if err := r.record.Start(ctx); err != nil {
			r.logger.Warn("record start warning", zap.Error(err))
		}
		r.recordReady = true
	}

	if r.nativeLog != nil {
		if err := r.nativeLog.Start(); err != nil {
			startupErr := fmt.Errorf("start native log queue: %w", err)
			return r.failStart(startupErr, r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
		}
		r.nativeLogReady = true
		logEngine, ok := r.native.(nativeLogEngineLifecycle)
		if !ok {
			startupErr := errors.New("native engine does not support log callback lifecycle")
			return r.failStart(startupErr, r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
		}
		if err := logEngine.SetNativeLogSink(r.nativeLog); err != nil {
			startupErr := fmt.Errorf("register native log callback: %w", err)
			return r.failStart(startupErr, r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
		}
		r.nativeLogRegistered = true
	}

	if err := r.native.Start(); err != nil {
		startupErr := fmt.Errorf("start native engine: %w", err)
		return r.failStart(startupErr, r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
	}
	r.nativeReady = true

	if r.camera != nil {
		if err := r.camera.InitCipher(ctx); err != nil {
			startupErr := fmt.Errorf("initialize camera cipher: %w", err)
			return r.failStart(startupErr, r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
		}
		if err := r.camera.Start(ctx); err != nil {
			startupErr := fmt.Errorf("start camera scheduler: %w", err)
			return r.failStart(startupErr, r.closeCamera(), r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
		}
		r.cameraReady = true
	}

	listener, err := r.listen("tcp", r.server.Addr)
	if err != nil {
		startupErr := fmt.Errorf("listen on %s: %w", r.server.Addr, err)
		return r.failStart(startupErr, r.closeCamera(), r.closePerson(ctx), r.closeAlgorithm(ctx), r.closeStorage(ctx), r.closeSystemTime(ctx), r.closeNetwork(ctx), r.closeAudit(ctx), r.closeNative(ctx), r.closeDatabase())
	}
	r.listener = listener
	r.serveDone = make(chan error, 1)
	go func() {
		serveErr := r.server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			r.logger.Error("HTTP server stopped unexpectedly", zap.Error(serveErr))
		}
		r.serveDone <- serveErr
		close(r.serveDone)
	}()
	r.httpReady = true
	if r.config.ConfigPath != "" {
		r.levelReload = newLogLevelReloader(r.config.ConfigPath, r.logLevel, r.logger)
		r.levelReload.Start()
	}
	r.logger.Info("HTTP server started", zap.String("address", listener.Addr().String()))
	return nil
}

func (r *lifecycleRuntime) failStart(startupErr error, cleanup ...error) error {
	if r.nativeReady || r.nativeLogRegistered || r.nativeLogReady {
		cleanup = append(cleanup, errors.New("native logging may still be active; application logger retained"))
	} else {
		cleanup = append(cleanup, r.syncLogger(), r.closeLogSink())
	}
	allErrors := append([]error{startupErr}, cleanup...)
	return errors.Join(allErrors...)
}

func (r *lifecycleRuntime) closeLogSink() error {
	if r.logSink == nil {
		return nil
	}
	sink := r.logSink
	r.logSink = nil
	closeErr := sink.Close()
	sinkErr, lost := sink.Health()
	var sinkErrors []error
	if closeErr != nil {
		sinkErrors = append(sinkErrors, closeErr)
	}
	if sinkErr != nil {
		sinkErrors = append(sinkErrors, fmt.Errorf("application log sink failed after %d refused writes: %w", lost, sinkErr))
	} else if lost > 0 {
		sinkErrors = append(sinkErrors, fmt.Errorf("application log sink refused %d writes", lost))
	}
	if err := errors.Join(sinkErrors...); err != nil {
		return fmt.Errorf("close application log sink: %w", err)
	}
	return nil
}

func (r *lifecycleRuntime) Stop(ctx context.Context) error {
	var stopErrors []error
	if r.levelReload != nil {
		r.levelReload.Stop()
		r.levelReload = nil
	}

	if r.httpReady {
		if err := r.shutdown(ctx); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("shut down HTTP server: %w", err))
			if closeErr := r.server.Close(); closeErr != nil {
				stopErrors = append(stopErrors, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		}
		select {
		case serveErr := <-r.serveDone:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				stopErrors = append(stopErrors, fmt.Errorf("HTTP server: %w", serveErr))
			}
		case <-ctx.Done():
			if closeErr := r.server.Close(); closeErr != nil {
				stopErrors = append(stopErrors, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		}
		r.httpReady = false
		r.listener = nil
	}

	if err := r.closeCamera(); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeAlgorithm(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closePerson(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeRecord(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeStorage(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeSystemTime(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeNetwork(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeAudit(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeNative(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeDatabase(); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if r.nativeReady || r.nativeLogRegistered || r.nativeLogReady {
		stopErrors = append(stopErrors, errors.New("native logging may still be active; application logger retained"))
	} else {
		if err := r.syncLogger(); err != nil {
			stopErrors = append(stopErrors, err)
		}
		if err := r.closeLogSink(); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	return errors.Join(stopErrors...)
}

func (r *lifecycleRuntime) closeCamera() error {
	if !r.cameraReady {
		return nil
	}
	r.cameraReady = false
	if r.camera != nil {
		r.camera.Stop()
	}
	return nil
}

func (r *lifecycleRuntime) closeNetwork(ctx context.Context) error {
	if !r.networkReady {
		return nil
	}
	r.networkReady = false
	if r.network != nil {
		if err := r.network.Stop(ctx); err != nil {
			return fmt.Errorf("stop network service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeStorage(ctx context.Context) error {
	if !r.storageReady {
		return nil
	}
	r.storageReady = false
	if r.storage != nil {
		if err := r.storage.Stop(ctx); err != nil {
			return fmt.Errorf("stop storage service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeAlgorithm(ctx context.Context) error {
	if !r.algorithmReady {
		return nil
	}
	r.algorithmReady = false
	if r.algorithm != nil {
		if err := r.algorithm.Stop(ctx); err != nil {
			return fmt.Errorf("stop algorithm service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closePerson(ctx context.Context) error {
	if !r.personReady {
		return nil
	}
	r.personReady = false
	if r.person != nil {
		if err := r.person.Stop(ctx); err != nil {
			return fmt.Errorf("stop person service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeRecord(ctx context.Context) error {
	if !r.recordReady {
		return nil
	}
	r.recordReady = false
	if r.record != nil {
		if err := r.record.Stop(ctx); err != nil {
			return fmt.Errorf("stop record service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeSystemTime(ctx context.Context) error {
	if !r.systemTimeReady {
		return nil
	}
	r.systemTimeReady = false
	if r.systemTime != nil {
		if err := r.systemTime.Stop(ctx); err != nil {
			return fmt.Errorf("stop system time service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeAudit(ctx context.Context) error {
	if !r.auditReady {
		return nil
	}
	r.auditReady = false
	if r.audit != nil {
		if err := r.audit.Stop(ctx); err != nil {
			return fmt.Errorf("stop audit service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeNative(ctx context.Context) error {
	if r.nativeReady {
		if native, ok := r.native.(nativeLogEngineLifecycle); ok {
			if err := native.Stop(); err != nil {
				return fmt.Errorf("stop native engine: %w", err)
			}
		}
		if err := r.native.Close(); err != nil {
			return fmt.Errorf("close native engine: %w", err)
		}
		r.nativeReady = false
	}

	if r.nativeLogRegistered {
		native, ok := r.native.(nativeLogEngineLifecycle)
		if !ok {
			return errors.New("native engine lost native log callback lifecycle")
		}
		if err := native.CloseNativeLogSink(); err != nil {
			return fmt.Errorf("unregister native log callback: %w", err)
		}
		r.nativeLogRegistered = false
	}

	if r.nativeLogReady {
		pending, err := r.nativeLog.Stop(ctx)
		if err != nil {
			return fmt.Errorf("stop native log queue with %d pending records: %w", pending, err)
		}
		if pending != 0 {
			return fmt.Errorf("stop native log queue left %d pending records", pending)
		}
		r.nativeLogReady = false
	}
	return nil
}

func (r *lifecycleRuntime) closeDatabase() error {
	if !r.databaseReady {
		return nil
	}
	r.databaseReady = false
	if err := r.database.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}

func (r *lifecycleRuntime) syncLogger() error {
	if r.loggerSynced {
		return nil
	}
	r.loggerSynced = true
	if r.logger == nil {
		return nil
	}
	if err := r.logger.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTTY) {
		return fmt.Errorf("sync application logger: %w", err)
	}
	return nil
}
