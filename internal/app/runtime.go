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

// gb28181Lifecycle is a degradable subsystem: its Start failure must not abort the
// boot, because the web console is the only way to fix a bad stored config.
// Stop runs before camera so in-flight SIP dialogs release their media leases
// while the camera/engine side is still alive.
type gb28181Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
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

// snapshotLifecycle 管理抓拍写盘 worker 的起停。
//
// ⚠️ 必须在 record 之前启动、在 record 之后停止：record 的 Stop 之后不再有新的
// 抓拍任务入队，但队列里已接收的任务仍需被执行完，否则这些记录会留下指向不存在
// 文件的路径。
type snapshotLifecycle interface {
	Start()
	Stop()
}

type lifecycleRuntime struct {
	database    databaseLifecycle
	native      engineLifecycle
	nativeLog   *nativelog.Queue
	camera      cameraLifecycle
	gb28181     gb28181Lifecycle
	audit       auditLifecycle
	network     networkLifecycle
	systemTime  systemTimeLifecycle
	storage     storageLifecycle
	algorithm   algorithmLifecycle
	person      personLifecycle
	record      recordLifecycle
	snapshot    snapshotLifecycle
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
	recordCancel        context.CancelFunc
	databaseReady       bool
	nativeReady         bool
	nativeLogReady      bool
	nativeLogRegistered bool
	cameraReady         bool
	gb28181Ready        bool
	auditReady          bool
	networkReady        bool
	systemTimeReady     bool
	storageReady        bool
	algorithmReady      bool
	personReady         bool
	recordReady         bool
	snapshotReady       bool
	httpReady           bool
	loggerSynced        bool
	startAttempted      bool
	// degraded lists the subsystems whose Start failed without aborting the boot,
	// so the startup summary can name them instead of scattering the fact across
	// one warning per subsystem.
	degraded []string
}

type runtimeParams struct {
	fx.In

	Database    databaseLifecycle
	Native      engineLifecycle
	NativeLog   *nativelog.Queue
	Camera      cameraLifecycle      `optional:"true"`
	GB28181     gb28181Lifecycle     `optional:"true"`
	Audit       auditLifecycle       `optional:"true"`
	Network     networkLifecycle     `optional:"true"`
	SystemTime  systemTimeLifecycle  `optional:"true"`
	Storage     storageLifecycle     `optional:"true"`
	Algorithm   algorithmLifecycle   `optional:"true"`
	Person      personLifecycle      `optional:"true"`
	Record      recordLifecycle      `optional:"true"`
	Snapshot    snapshotLifecycle    `optional:"true"`
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
		gb28181:     p.GB28181,
		audit:       p.Audit,
		network:     p.Network,
		systemTime:  p.SystemTime,
		storage:     p.Storage,
		algorithm:   p.Algorithm,
		person:      p.Person,
		record:      p.Record,
		snapshot:    p.Snapshot,
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
			r.degradedStart("region_config_cache", err)
		}
	}

	if r.audit != nil {
		if err := r.audit.Start(ctx); err != nil {
			startupErr := fmt.Errorf("start audit service: %w", err)
			return r.failStart(startupErr, r.closeDatabase(ctx))
		}
		r.auditReady = true
	}

	// The remaining domains are optional at runtime: a failure downgrades the host
	// instead of aborting the boot, because a broken algorithm supervisor must not
	// cost the operator the whole web console.
	//
	// ready is set even when Start failed, on purpose: ready means "Stop must run",
	// not "started cleanly". A subsystem that failed half way still owns a worker,
	// a file handle or a socket, and only its own Stop releases them.
	if r.network != nil {
		if err := r.network.Start(ctx); err != nil {
			r.degradedStart("network", err)
		}
		r.networkReady = true
	}

	if r.systemTime != nil {
		if err := r.systemTime.Start(ctx); err != nil {
			r.degradedStart("system_time", err)
		}
		r.systemTimeReady = true
	}

	if r.storage != nil {
		if err := r.storage.Start(ctx); err != nil {
			r.degradedStart("storage", err)
		}
		r.storageReady = true
	}

	if r.person != nil {
		if err := r.person.Start(ctx); err != nil {
			r.degradedStart("person", err)
		}
		r.personReady = true
	}

	// 抓拍 worker 必须先于 record 启动：record 在 Start 之后就可能开始接收 AI 结果
	// 并登记抓拍任务，worker 未就绪会让队列迅速积满并持续丢弃。
	if r.snapshot != nil {
		r.snapshot.Start()
		r.snapshotReady = true
	}

	if r.record != nil {
		recordCtx, cancel := context.WithCancel(context.Background())
		r.recordCancel = cancel
		if err := r.record.Start(recordCtx); err != nil {
			r.degradedStart("record", err)
		}
		r.recordReady = true
	}

	if r.nativeLog != nil {
		if err := r.nativeLog.Start(); err != nil {
			return r.failStart(fmt.Errorf("start native log queue: %w", err), r.rollback(ctx)...)
		}
		r.nativeLogReady = true
		logEngine, ok := r.native.(nativeLogEngineLifecycle)
		if !ok {
			return r.failStart(errors.New("native engine does not support log callback lifecycle"), r.rollback(ctx)...)
		}
		if err := logEngine.SetNativeLogSink(r.nativeLog); err != nil {
			return r.failStart(fmt.Errorf("register native log callback: %w", err), r.rollback(ctx)...)
		}
		r.nativeLogRegistered = true
	}

	if err := r.native.Start(); err != nil {
		return r.failStart(fmt.Errorf("start native engine: %w", err), r.rollback(ctx)...)
	}
	r.nativeReady = true

	if r.camera != nil {
		if err := r.camera.InitCipher(ctx); err != nil {
			return r.failStart(fmt.Errorf("initialize camera cipher: %w", err), r.rollback(ctx)...)
		}
		if err := r.camera.Start(ctx); err != nil {
			return r.failStart(fmt.Errorf("start camera scheduler: %w", err), r.rollback(ctx)...)
		}
		r.cameraReady = true
	}

	// GB28181 接入属于可降级子系统：配置无效或 listener bind 失败时只记录降级，
	// 不能连带整个 Web 管理面起不来（现场只能通过 UI 修改该配置）。
	// ready 置位表示「必须执行 Stop」，不是「启动成功」。
	if r.gb28181 != nil {
		if err := r.gb28181.Start(ctx); err != nil {
			r.degradedStart("gb28181", err)
		}
		r.gb28181Ready = true
	}

	// 算法管理必须在 native 引擎启动与 camera 初始化之后启动：
	// 这样开机自动拉起 active 算法时，能够成功解密 URI 并通过 Engine.AcquireStream 建立 AI 物理流。
	if r.algorithm != nil {
		if err := r.algorithm.Start(ctx); err != nil {
			r.degradedStart("algorithm", err)
		}
		r.algorithmReady = true
	}

	listener, err := r.listen("tcp", r.server.Addr)
	if err != nil {
		return r.failStart(fmt.Errorf("listen on %s: %w", r.server.Addr, err), r.rollback(ctx)...)
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
	// One aggregated line, because the per-subsystem warnings above are easy to
	// lose in a long boot log and nothing else reports a degraded host: /health
	// only promises database and engine readiness.
	if len(r.degraded) > 0 {
		r.logger.Warn("host started with degraded subsystems; see the warnings above",
			zap.Strings("subsystems", r.degraded))
	}
	return nil
}

// stopSequence is the single authoritative teardown order. Both Stop and the
// startup rollback walk this one list, so the two paths cannot drift apart:
// keeping two hand-maintained copies used to give a failed startup a different
// order than a clean shutdown.
//
// The relative order is the contract in dependency-injection.md §2: camera
// before algorithm before native.Close, and record before the snapshot worker
// drains before storage loses the media root. camera must precede native.Close
// in particular because StreamHub.Close releases engine.AIResultSubscription
// handles, which are backed by native state.
//
// Every close* self-checks its readiness flag, so walking the whole sequence is
// equivalent to truncating it at the failure point and never closes twice.
func (r *lifecycleRuntime) stopSequence() []func(context.Context) error {
	return []func(context.Context) error{
		// GB28181 first: stop SIP ingress and release media leases while camera and
		// the native engine are still alive to serve the teardown.
		r.closeGB28181,
		r.closeCamera,
		r.closeAlgorithm,
		r.closePerson,
		r.closeRecordAndSnapshot,
		r.closeStorage,
		r.closeSystemTime,
		r.closeNetwork,
		r.closeAudit,
		r.closeNative,
		r.closeDatabase,
	}
}

// rollback releases whatever a failed Start acquired, in stop order.
func (r *lifecycleRuntime) rollback(ctx context.Context) []error {
	var rollbackErrors []error
	for _, stop := range r.stopSequence() {
		if err := stop(ctx); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return rollbackErrors
}

// degradedStart records an optional subsystem that failed to start. The host
// keeps running, but the failure must stay visible: it goes into the one-line
// startup summary and is logged with the cause at the moment it happens.
func (r *lifecycleRuntime) degradedStart(subsystem string, startErr error) {
	r.degraded = append(r.degraded, subsystem)
	r.logger.Warn("subsystem failed to start; host continues degraded",
		zap.String("subsystem", subsystem), zap.Error(startErr))
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

	for _, stop := range r.stopSequence() {
		if err := stop(ctx); err != nil {
			stopErrors = append(stopErrors, err)
		}
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

// closeCamera stops the health scheduler and the StreamHub. It runs before
// closeAlgorithm per dependency-injection.md §2.
func (r *lifecycleRuntime) closeGB28181(ctx context.Context) error {
	if !r.gb28181Ready {
		return nil
	}
	r.gb28181Ready = false
	if r.gb28181 == nil {
		return nil
	}
	return r.gb28181.Stop(ctx)
}

func (r *lifecycleRuntime) closeCamera(context.Context) error {
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
	if r.recordCancel != nil {
		r.recordCancel()
		r.recordCancel = nil
	}
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

func (r *lifecycleRuntime) closeRecordAndSnapshot(ctx context.Context) error {
	err := r.closeRecord(ctx)
	r.closeSnapshot()
	return err
}

// closeSnapshot 在 record 之后停止抓拍 worker，把已入队的写盘任务做完。
func (r *lifecycleRuntime) closeSnapshot() {
	if !r.snapshotReady {
		return
	}
	r.snapshotReady = false
	if r.snapshot != nil {
		r.snapshot.Stop()
	}
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

// closeDatabase ignores the shutdown context: closing SQLite is a local syscall,
// and every domain that writes to it has already stopped by this stage.
func (r *lifecycleRuntime) closeDatabase(context.Context) error {
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
