package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/alertpush"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/clientlog"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/faceextract"
	"github.com/nikonikowuw/Zhulong/internal/gb28181"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/nativelog"
	"github.com/nikonikowuw/Zhulong/internal/network"
	"github.com/nikonikowuw/Zhulong/internal/openapi"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"github.com/nikonikowuw/Zhulong/internal/spatial"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"github.com/nikonikowuw/Zhulong/internal/systeminfo"
	"github.com/nikonikowuw/Zhulong/internal/systemtime"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"github.com/nikonikowuw/Zhulong/internal/webui"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// --- Database Module ---

type databaseOut struct {
	fx.Out

	Store     *database.Store
	Provider  database.DBProvider
	Lifecycle databaseLifecycle
}

func provideDatabase(config Config, logger *zap.Logger) databaseOut {
	store := database.New(config.DataDir, logger)
	return databaseOut{
		Store:     store,
		Provider:  store,
		Lifecycle: store,
	}
}

var databaseModule = fx.Module("database",
	fx.Provide(provideDatabase),
)

// --- Engine Module ---

type engineOut struct {
	fx.Out

	Engine    *engine.Engine
	Lifecycle engineLifecycle
}

func provideEngine(cfg Config) (engineOut, error) {
	eng := engine.New()
	var backend engine.DecodeBackend
	switch cfg.Decode.Backend {
	case "mpp":
		backend = engine.DecodeBackendMPP
	case "ffmpeg":
		backend = engine.DecodeBackendFFmpeg
	default:
		backend = engine.DecodeBackendAuto
	}
	policy := engine.DecodePolicy{
		Backend:               backend,
		AllowSoftwareFallback: cfg.Decode.AllowSoftwareFallback,
		MaxSessions:           cfg.Decode.MaxSessions,
	}
	if err := eng.ConfigureDecode(policy); err != nil {
		return engineOut{}, fmt.Errorf("configure decode policy: %w", err)
	}
	return engineOut{
		Engine:    eng,
		Lifecycle: eng,
	}, nil
}

var engineModule = fx.Module("engine",
	fx.Provide(provideEngine),
)

func provideNativeLogQueue(logger *zap.Logger) (*nativelog.Queue, error) {
	return nativelog.NewQueue(256, logger)
}

var nativeLogModule = fx.Module("native-log",
	fx.Provide(provideNativeLogQueue),
)

// --- Audit Module ---

type auditOut struct {
	fx.Out

	Store       *audit.Store
	Service     *audit.Service
	Handler     *audithttp.Handler
	Lifecycle   auditLifecycle
	AuditRoutes RouteRegistrar `group:"protected_routes"`
}

func provideAudit(dbProvider database.DBProvider, logger *zap.Logger) auditOut {
	store := audit.NewStore(dbProvider)
	svc := audit.NewService(store, logger)
	handler := audithttp.NewHandler(svc, logger)
	return auditOut{
		Store:       store,
		Service:     svc,
		Handler:     handler,
		Lifecycle:   svc,
		AuditRoutes: handler,
	}
}

var auditModule = fx.Module("audit",
	fx.Provide(provideAudit),
)

// --- Auth Module ---

type authOut struct {
	fx.Out

	UserStore    auth.UserStore
	SessionStore auth.SessionStore
	AuthService  auth.AuthService
	Handler      *auth.Handler
	PublicRoutes RouteRegistrar `group:"public_routes"`
}

func provideAuth(config Config, dbProvider database.DBProvider, logger *zap.Logger) authOut {
	userStore := auth.NewUserStore(dbProvider)
	sessionStore := auth.NewMemorySessionStore(config.SessionTTL)
	authSvc := auth.NewAuthService(userStore, sessionStore, nil, logger)
	handler := auth.NewHandler(authSvc)
	return authOut{
		UserStore:    userStore,
		SessionStore: sessionStore,
		AuthService:  authSvc,
		Handler:      handler,
		PublicRoutes: handler,
	}
}

var authModule = fx.Module("auth",
	fx.Provide(provideAuth),
)

// --- Camera Module ---

type cameraOut struct {
	fx.Out

	Store     camera.CameraStore
	Service   *camera.CameraService
	StreamHub *camera.StreamHub
	Handler   *camera.Handler
	Lifecycle cameraLifecycle
	// Cipher and Manager are exported as concrete types so other domains can
	// (a) encrypt their own credentials with the same key and (b) register the fact
	// that camera.key must not be regenerated. Keeping them typed as interfaces
	// would force a reflection-based lookup in the assembly code.
	Cipher       *camera.LazyCipher
	Manager      *camera.LifecycleManager
	CameraRoutes RouteRegistrar `group:"protected_routes"`
}

type cameraParams struct {
	fx.In

	Config       Config
	DBProvider   database.DBProvider
	Engine       *engine.Engine
	AuthService  auth.AuthService
	AuditService *audit.Service
	Logger       *zap.Logger
}

func provideCamera(p cameraParams) cameraOut {
	camStore := camera.NewCameraStore(p.DBProvider)
	keyMgr := camera.NewKeyManager(p.Config.DataDir)
	lazyC := camera.NewLazyCipher()
	reg := camera.NewStateRegistry()
	hub := camera.NewEventHub(reg)

	engAdapter := camera.NewEngineMediaAdapter(p.Engine)
	streamHub := camera.NewStreamHub(engAdapter, camStore, lazyC, reg, hub, p.Logger)
	prober := camera.NewProbeService(p.Engine)
	describeClient := camera.NewDescribeClient()
	scheduler := camera.NewHealthScheduler(camStore, lazyC, reg, hub, describeClient, p.Logger)
	// 活跃流 4 秒超时判定只对 Go 侧逐包可见的预览流成立（见 PacketActivitySource）。
	scheduler.SetPacketActivitySource(streamHub)
	camSvc := camera.NewCameraService(camStore, lazyC, prober, reg, hub, scheduler, p.Logger)
	// authSvc is a required dependency of this provider (provideAuth always
	// supplies it), so the handler gets a real predicate rather than a nil one.
	validateSession := func(token string) bool {
		_, ok := p.AuthService.ValidateSession(token)
		return ok
	}
	camHandler := camera.NewHandler(camSvc, hub, streamHub, p.Logger, validateSession)

	camMgr := camera.NewLifecycleManager(camStore, keyMgr, lazyC, scheduler, hub, streamHub)

	return cameraOut{
		Store:        camStore,
		Service:      camSvc,
		StreamHub:    streamHub,
		Handler:      camHandler,
		Lifecycle:    camMgr,
		Cipher:       lazyC,
		Manager:      camMgr,
		CameraRoutes: camHandler,
	}
}

var cameraModule = fx.Module("camera",
	fx.Provide(provideCamera),
)

// --- GB28181 Module ---

type gb28181Out struct {
	fx.Out

	Store     *gb28181.Store
	Runtime   *gb28181.Runtime
	Service   *gb28181.Service
	Handler   *gb28181.Handler
	Lifecycle gb28181Lifecycle
	GBRoutes  RouteRegistrar `group:"protected_routes"`
}

type gb28181Params struct {
	fx.In

	DBProvider    database.DBProvider
	CameraCipher  *camera.LazyCipher
	CameraManager *camera.LifecycleManager
	CameraService *camera.CameraService
	Logger        *zap.Logger
}

func provideGB28181(p gb28181Params) gb28181Out {
	// The domain builds its own SIP ingress (S1); passing no override keeps the
	// production wiring out of this provider.
	comp := gb28181.NewComponents(gb28181.ComponentsOptions{
		DBProvider:    p.DBProvider,
		Cipher:        p.CameraCipher,
		CameraCreator: p.CameraService,
		CameraDeleter: p.CameraService,
		Logger:        p.Logger,
	})
	// The probe is registered on the camera lifecycle manager directly: that is the
	// only consumer, so it must not be published as a second fx value that nothing
	// reads (a reader could then believe the wiring happens through the graph).
	if p.CameraManager != nil {
		p.CameraManager.AddEncryptedDataProbe(comp.Store)
	}
	return gb28181Out{
		Store:     comp.Store,
		Runtime:   comp.Runtime,
		Service:   comp.Service,
		Handler:   comp.Handler,
		Lifecycle: comp.Lifecycle,
		GBRoutes:  comp.ProtectedRoutes,
	}
}

var gb28181Module = fx.Module("gb28181",
	fx.Provide(provideGB28181),
)

// --- Network Module ---

type networkOut struct {
	fx.Out

	Provider        network.NetworkProvider
	Watchdog        *network.WatchdogManager
	Service         *network.NetworkService
	Handler         *network.Handler
	Lifecycle       networkLifecycle
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
	PublicRoutes    RouteRegistrar `group:"public_routes"`
}

func provideNetwork(config Config, logger *zap.Logger) networkOut {
	comp := network.NewNetworkComponents(network.Options{
		DataDir:      config.DataDir,
		CustomScript: config.CustomScript,
		HTTPAddress:  config.HTTPAddress,
	}, logger)
	return networkOut{
		Provider:        comp.Provider,
		Watchdog:        comp.Watchdog,
		Service:         comp.Service,
		Handler:         comp.Handler,
		Lifecycle:       comp.Lifecycle,
		ProtectedRoutes: comp.ProtectedRoutes,
		PublicRoutes:    comp.PublicRoutes,
	}
}

var networkModule = fx.Module("network",
	fx.Provide(provideNetwork),
)

// --- System Time Module ---

type systemTimeOut struct {
	fx.Out

	Driver          systemtime.ClockDriver
	Repository      systemtime.Repository
	Service         *systemtime.TimeService
	Handler         *systemtime.Handler
	Lifecycle       systemTimeLifecycle
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

func provideSystemTime(dbProvider database.DBProvider, auditSvc *audit.Service, logger *zap.Logger) systemTimeOut {
	comp := systemtime.NewTimeComponents(dbProvider, auditSvc, logger)
	return systemTimeOut{
		Driver:          comp.Driver,
		Repository:      comp.Repository,
		Service:         comp.Service,
		Handler:         comp.Handler,
		Lifecycle:       comp.Lifecycle,
		ProtectedRoutes: comp.ProtectedRoutes,
	}
}

var systemTimeModule = fx.Module("systemtime",
	fx.Provide(provideSystemTime),
)

// --- System Info Module ---

type systemInfoOut struct {
	fx.Out

	Service         *systeminfo.Service
	Identity        *systeminfo.IdentityService
	Handler         *systeminfo.Handler
	Lifecycle       systemInfoLifecycle
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

func provideSystemInfo(dbProvider database.DBProvider, logger *zap.Logger) systemInfoOut {
	comp := systeminfo.NewSystemInfoComponents(dbProvider, logger)
	return systemInfoOut{
		Service:         comp.Service,
		Identity:        comp.Identity,
		Handler:         comp.Handler,
		Lifecycle:       comp.Lifecycle,
		ProtectedRoutes: comp.ProtectedRoutes,
	}
}

var systemInfoModule = fx.Module("systeminfo",
	fx.Provide(provideSystemInfo),
)

// --- Storage Module ---

type storageOut struct {
	fx.Out

	Repository      storage.Repository
	Inspector       storage.PathInspector
	Gate            *storage.EmergencyGate
	Cleaner         *storage.CleanerEngine
	Service         *storage.Service
	Handler         *storage.Handler
	Lifecycle       storageLifecycle
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

func provideStorage(dbProvider database.DBProvider, auditSvc *audit.Service, logger *zap.Logger) storageOut {
	comp := storage.NewStorageComponents(dbProvider, auditSvc, logger)
	return storageOut{
		Repository:      comp.Repository,
		Inspector:       comp.Inspector,
		Gate:            comp.Gate,
		Cleaner:         comp.Cleaner,
		Service:         comp.Service,
		Handler:         comp.Handler,
		Lifecycle:       comp.Lifecycle,
		ProtectedRoutes: comp.ProtectedRoutes,
	}
}

var storageModule = fx.Module("storage",
	fx.Provide(provideStorage),
)

// --- Media File Module ---

type mediaFileOut struct {
	fx.Out

	Service         *mediafile.Service
	Handler         *mediafile.Handler
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

// provideMediaFile wires the read-only media file service onto the storage
// service, which owns the media root configuration and the filesystem
// telemetry used for fallthrough detection.
func provideMediaFile(storageSvc *storage.Service, inspector storage.PathInspector, logger *zap.Logger) mediaFileOut {
	comp := mediafile.NewMediaFileComponents(
		mediafile.NewMediaRootProvider(storageSvc),
		mediafile.NewDeviceInspector(inspector),
		logger,
	)
	return mediaFileOut{
		Service:         comp.Service,
		Handler:         comp.Handler,
		ProtectedRoutes: comp.ProtectedRoutes,
	}
}

var mediaFileModule = fx.Module("mediafile",
	fx.Provide(provideMediaFile),
)

// --- Algorithm Module ---

type algorithmOut struct {
	fx.Out

	Store              *algorithm.Store
	Verifier           *algorithm.Verifier
	Supervisor         *algorunner.Supervisor
	Service            *algorithm.Service
	Handler            *algorithm.Handler
	Lifecycle          algorithmLifecycle
	GalleryBroadcaster *algorunner.GalleryBroadcaster
	ProtectedRoutes    RouteRegistrar `group:"protected_routes"`
	UploadRoutes       RouteRegistrar `group:"upload_routes"`
}

// newAlgorithmRuntime resolves the algorithm subsystem's on-disk layout. This is
// the one place that derives it; every algorithm component then shares the
// resulting value.
//
// There is no runner binary to point at any more: each package is executed
// through its own entry (algorunner.ResolveEntry), so the only host-side input
// left is where packages live. The root is made absolute on purpose — package
// processes inherit it as argv[0] and the orphan sweep matches on the absolute
// install layout, so a relative data directory would make both depend on the
// host's CWD.
func newAlgorithmRuntime(dataDir string) algorunner.Runtime {
	root := dataDir
	if abs, err := filepath.Abs(dataDir); err == nil {
		root = abs
	}
	return algorunner.Runtime{AlgorithmsDir: filepath.Join(root, "algorithms")}
}

type algorithmParams struct {
	fx.In

	DBProvider database.DBProvider
	StreamHub  *camera.StreamHub
	Engine     *engine.Engine
	Config     Config
	TaskStore  *task.Store
	Logger     *zap.Logger
}

func provideAlgorithm(p algorithmParams) algorithmOut {
	comp := algorithm.NewAlgorithmComponents(
		p.DBProvider,
		newAlgorithmRuntime(p.Config.DataDir),
		algorithm.DetectHostPlatform(p.Config.AlgorithmTarget),
		p.Logger,
	)
	taskBrokerResolver := &appBrokerPathResolver{
		taskStore: p.TaskStore,
		streamHub: p.StreamHub,
		logger:    p.Logger,
	}
	comp.Supervisor.SetBrokerSubscriptionResolver(taskBrokerResolver)
	// 任务级配置（逐路流的 task_algorithms.config_json）走 Broker 广播 + 握手重放，
	// 进程级配置走 argv `--config`：argv 无法表达 per-stream 值。
	comp.Supervisor.SetTaskConfig(taskBrokerResolver, algorunner.NewTaskConfigBroadcaster(p.Engine, p.Logger))
	galleryBroadcaster := algorunner.NewGalleryBroadcaster(p.Engine, p.Logger)
	return algorithmOut{
		Store:              comp.Store,
		Verifier:           comp.Verifier,
		Supervisor:         comp.Supervisor,
		Service:            comp.Service,
		Handler:            comp.Handler,
		Lifecycle:          comp.Lifecycle,
		GalleryBroadcaster: galleryBroadcaster,
		ProtectedRoutes:    comp.ProtectedRoutes,
		UploadRoutes:       comp.UploadRoutes,
	}
}

var algorithmModule = fx.Module("algorithm",
	fx.Provide(provideAlgorithm),
)

// --- Task Module ---

type taskOut struct {
	fx.Out

	Service         *task.Service
	Handler         *task.Handler
	ConfigCache     *task.RegionConfigCache
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

func provideTaskStore(dbProvider database.DBProvider) *task.Store {
	return task.NewStore(dbProvider)
}

type taskParams struct {
	fx.In

	Lifecycle   fx.Lifecycle
	Store       *task.Store
	AlgoService *algorithm.Service
	StreamHub   *camera.StreamHub
	Logger      *zap.Logger
}

func provideTaskComponents(p taskParams) taskOut {
	configCache := task.NewRegionConfigCache(p.Store, p.Logger)
	p.StreamHub.SetRegionConfigProvider(configCache)

	// Both the algorithm supervisor and the region config cache must react to task changes.
	// The cache owns its own error handling, so no adapter is needed here.
	listeners := []task.TaskChangeListener{
		p.AlgoService,
		configCache,
	}
	// 任务级裁剪：Store 直接满足 camera.TaskCropProvider；保存后由监听器刷新媒体层。
	// 必须在 Service 构造后注册（保存事件在 UpdateTask/SaveTask 内同步广播）。
	p.StreamHub.SetTaskCropProvider(p.Store)
	service := task.NewService(p.Store, p.AlgoService, listeners, p.Logger)
	service.SetTaskSavedListener(taskCropListener{hub: p.StreamHub, logger: p.Logger})
	scheduleWorker := task.NewScheduleWorker(p.Store, listeners, p.Logger, 30*time.Second)

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			scheduleWorker.Start()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			scheduleWorker.Stop()
			return nil
		},
	})

	handler := task.NewHandler(service)
	return taskOut{
		Service:         service,
		Handler:         handler,
		ConfigCache:     configCache,
		ProtectedRoutes: handler,
	}
}

var taskModule = fx.Module("task",
	fx.Provide(
		provideTaskStore,
		provideTaskComponents,
		regionConfigReloaderProvider,
	),
)

// --- Person Module ---

type personOut struct {
	fx.Out

	Store           *person.Store
	Service         *person.Service
	Handler         *person.Handler
	Lifecycle       personLifecycle
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
	UploadRoutes    RouteRegistrar `group:"upload_routes"`
}

type personParams struct {
	fx.In

	DBProvider  database.DBProvider
	Config      Config
	AlgStore    *algorithm.Store
	Broadcaster *algorunner.GalleryBroadcaster
	Supervisor  *algorunner.Supervisor
	RecordStore *record.Store
	AsyncJobs   *asyncjob.Service
	Logger      *zap.Logger
}

func providePerson(p personParams) personOut {
	facesDir := p.Config.FacesDir
	if facesDir == "" {
		facesDir = filepath.Join(p.Config.DataDir, "faces")
	}
	// Collaborators are resolved before construction: the person components capture
	// their dependencies once and never swap them afterwards. All three are
	// required dependencies of this provider, so none of them can arrive nil.
	//
	// The face provider is resolved *per request* through the algorithm store: the
	// package can be upgraded, rolled back or reinstalled while the host runs, and a
	// client pinned to one path would keep enrolling with the old model.
	rt := newAlgorithmRuntime(p.Config.DataDir)
	providerID := p.Config.ExtractorAlgorithm
	extractor := &faceFeatureExtractorAdapter{client: faceextract.New(faceextract.Options{
		Logger: p.Logger,
		Entry: func(ctx context.Context) (string, error) {
			installed, err := p.AlgStore.GetAlgorithm(ctx, providerID)
			if err != nil {
				return "", fmt.Errorf("face provider %q: %w", providerID, err)
			}
			return algorunner.ResolveEntry(rt.PackageDir(installed.ID, installed.Version), installed.ID)
		},
	})}
	notifier := &personGalleryAdapter{broadcaster: p.Broadcaster}
	keyBackfiller := &personKeyBackfillerAdapter{store: p.RecordStore}

	opts := person.Options{
		Extractor:                    extractor,
		GalleryNotifier:              notifier,
		RequirePersonIDForEnrollment: p.Config.RequirePersonIDForEnrollment,
		RecognitionKeyBackfiller:     keyBackfiller,
		AsyncJobs:                    p.AsyncJobs,
	}
	comp := person.NewPersonComponents(p.DBProvider, facesDir, p.Logger, opts)
	if p.AsyncJobs != nil && comp.JobHandler != nil {
		p.AsyncJobs.RegisterHandler(comp.JobHandler)
	}
	p.Supervisor.SetGallerySync(&supervisorGallerySyncAdapter{service: comp.Service}, p.Broadcaster)
	return personOut{
		Store:           comp.Store,
		Service:         comp.Service,
		Handler:         comp.Handler,
		Lifecycle:       comp.Lifecycle,
		ProtectedRoutes: comp.ProtectedRoutes,
		UploadRoutes:    comp.UploadRoutes,
	}
}

var personModule = fx.Module("person",
	fx.Provide(providePerson),
)

// --- Async Job Module ---

type asyncJobOut struct {
	fx.Out

	Store           *asyncjob.Store
	Service         *asyncjob.Service
	Handler         *asyncjob.Handler
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

func provideAsyncJob(
	lifecycle fx.Lifecycle,
	dbProvider database.DBProvider,
	logger *zap.Logger,
) asyncJobOut {
	comp := asyncjob.NewAsyncJobComponents(dbProvider, logger)
	lifecycle.Append(fx.Hook{
		OnStart: comp.Start,
		OnStop:  comp.Stop,
	})
	return asyncJobOut{
		Store:           comp.Store,
		Service:         comp.Service,
		Handler:         comp.Handler,
		ProtectedRoutes: comp.ProtectedRoutes,
	}
}

var asyncJobModule = fx.Module("asyncjob",
	fx.Provide(provideAsyncJob),
)

// --- Client Log Module ---

type clientLogOut struct {
	fx.Out

	Handler      *clientlog.Handler
	Limiter      *httpmiddleware.IPRateLimiter
	PublicRoutes RouteRegistrar `group:"public_routes"`
}

// provideClientLog builds the unauthenticated browser error ingestion surface.
//
// The endpoint is public on purpose: browser errors peak when a session has
// expired. That means its bounds are the only protection, so they are
// constructed here rather than defaulted at the call site. The per-IP values are
// fixed by the API contract; the process-wide values are provisional defaults
// that a deployment profile may override and that are NOT a signed budget.
func provideClientLog(logger *zap.Logger) clientLogOut {
	limiter := httpmiddleware.NewIPRateLimiter(httpmiddleware.RateLimitConfig{})
	handler := clientlog.NewHandler(logger, limiter)
	return clientLogOut{
		Handler:      handler,
		Limiter:      limiter,
		PublicRoutes: handler,
	}
}

var clientLogModule = fx.Module("client-log",
	fx.Provide(provideClientLog),
)

// --- Record Module ---

type recordOut struct {
	fx.Out

	Store             *record.Store
	Service           *record.Service
	Handler           *record.Handler
	Snapshots         *record.SnapshotStore
	SnapshotLifecycle snapshotLifecycle
	Lifecycle         recordLifecycle
	ProtectedRoutes   RouteRegistrar `group:"protected_routes"`
}

type recordParams struct {
	fx.In

	DBProvider database.DBProvider
	StreamHub  *camera.StreamHub
	Storage    *storage.Service
	MediaFiles *mediafile.Service
	Logger     *zap.Logger
}

func provideRecord(p recordParams) recordOut {
	comp := record.NewRecordComponents(p.DBProvider, p.Logger)

	// 感知记录库表行的保留期与抓拍图 TTL 同源：图没了，行就只剩指向空文件的悬空引用。
	// 保留天数由 storage 域持有（用户在存储设置里改的就是这个值），这里只做转译。
	comp.Pruner.SetRetentionDaysProvider(recordRetentionDaysProvider{storage: p.Storage})
	comp.Pruner.SetSnapshotFileChecker(recordSnapshotFileChecker{media: p.MediaFiles})
	p.Storage.SetAfterPrune(func(ctx context.Context, prunedUnitIDs []string) {
		// 1. 先把已删文件的 ID 落到 pending 表，保证崩溃/ctx 取消后仍可重试。
		if len(prunedUnitIDs) > 0 {
			if _, err := comp.Pruner.EnqueueSnapshotDeletes(ctx, prunedUnitIDs); err != nil {
				// Durable outbox first. If SQLite is transiently unavailable, try
				// the direct primary-key delete as a best-effort fallback; if that
				// also fails, the TTL sweep remains the final safety net.
				p.Logger.Warn("enqueue pending snapshot deletes failed; trying direct delete",
					zap.Error(err),
					zap.Int("ids", len(prunedUnitIDs)),
				)
				if deleted, directErr := comp.Pruner.DeleteBySnapshotIDs(ctx, prunedUnitIDs); directErr != nil {
					p.Logger.Warn("direct snapshot row delete after enqueue failure failed", zap.Error(directErr))
				} else if deleted > 0 {
					p.Logger.Info("deleted snapshot rows without durable outbox", zap.Int64("deleted_rows", deleted))
				}
			}
		}
		// 2. 原有超期 TTL 对账；失败不阻断第 3 步。
		if result, err := comp.Pruner.PruneNow(ctx); err != nil {
			p.Logger.Warn("reconcile perception records after storage cleanup failed", zap.Error(err))
		} else if result.Total() > 0 {
			p.Logger.Info("reconciled perception records after storage cleanup",
				zap.Int64("deleted_rows", result.Total()))
		}
		// 3. 消费 pending（含本轮与历史残留）。
		if deleted, err := comp.Pruner.ProcessPendingSnapshotDeletes(ctx, 0); err != nil {
			p.Logger.Warn("process pending snapshot deletes failed", zap.Error(err))
		} else if deleted > 0 {
			p.Logger.Info("deleted pending snapshot records",
				zap.Int64("deleted_rows", deleted))
		}
	})

	// 历史 alarm 图可能没有 .lock；即使补锁尚未完成，storage 也按 alarm ID
	// 做最后一道失败关闭的保护。
	p.Storage.SetEvidenceGuard(func(ctx context.Context, ids []string) (map[string]bool, error) {
		return comp.Store.ExistingAlarmIDs(ctx, ids)
	})

	// 抓拍落盘组件：把 StreamHub（抓拍源）与 storage（媒体根 + 写入门闸）接进
	// record 域。两者都是窄接口，record 不认识 camera / storage 的内部类型。
	snapshots := record.NewSnapshotStore(
		p.StreamHub,
		mediafile.NewMediaRootProvider(p.Storage),
		p.Storage,
		0, // 0 = 使用 engine.DefaultSnapshotQuality
		p.Logger,
	)
	snapshots.SetPathCleaner(comp.Store)
	comp.Service.SetSnapshotStore(snapshots)

	p.StreamHub.SetAIConsumer(comp.Service)
	return recordOut{
		Store:             comp.Store,
		Service:           comp.Service,
		Handler:           comp.Handler,
		Snapshots:         snapshots,
		SnapshotLifecycle: snapshots,
		Lifecycle:         comp.Lifecycle,
		ProtectedRoutes:   comp.ProtectedRoutes,
	}
}

var recordModule = fx.Module("record",
	fx.Provide(provideRecord),
)

// --- Spatial Module ---

// spatialOut exposes only what the runtime and router actually consume: the
// domain has no server-side consumers of its store/service/handler beyond this
// module, and unused Fx providers are dead weight in the graph.
type spatialOut struct {
	fx.Out

	Lifecycle       spatialLifecycle
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
	UploadRoutes    RouteRegistrar `group:"upload_routes"`
}

func provideSpatial(
	dbProvider database.DBProvider,
	config Config,
	logger *zap.Logger,
) spatialOut {
	mapsDir := filepath.Join(config.DataDir, "maps")
	comps := spatial.NewSpatialComponents(dbProvider, mapsDir, logger)
	return spatialOut{
		Lifecycle:       comps.Lifecycle,
		ProtectedRoutes: comps.ProtectedRoutes,
		UploadRoutes:    comps.UploadRoutes,
	}
}

var spatialModule = fx.Module("spatial",
	fx.Provide(provideSpatial),
)

// --- API Token Module ---

type apiTokenOut struct {
	fx.Out

	Store           *apitoken.Store
	Service         *apitoken.Service
	Lifecycle       apiTokenLifecycle
	Handler         *apitoken.Handler
	ProtectedRoutes RouteRegistrar `group:"protected_routes"`
}

func provideAPIToken(provider database.DBProvider, logger *zap.Logger) apiTokenOut {
	store := apitoken.NewStore(provider)
	svc := apitoken.NewService(store, logger)
	handler := apitoken.NewHandler(svc, logger)
	return apiTokenOut{
		Store:           store,
		Service:         svc,
		Lifecycle:       svc,
		Handler:         handler,
		ProtectedRoutes: handler,
	}
}

var apiTokenModule = fx.Module("apitoken",
	fx.Provide(provideAPIToken),
)

// --- OpenAPI Module ---

type openAPIOut struct {
	fx.Out

	Handler       *openapi.Handler
	OpenAPIRoutes RouteRegistrar `group:"openapi_routes"`
}

func provideOpenAPI(personSvc *person.Service) openAPIOut {
	handler := openapi.NewHandler(personSvc, openapi.NewUploadLimiter())
	return openAPIOut{
		Handler:       handler,
		OpenAPIRoutes: handler,
	}
}

var openAPIModule = fx.Module("openapi",
	fx.Provide(provideOpenAPI),
)

// --- AlertPush Module ---

type alertPushOut struct {
	fx.Out

	Store             *alertpush.Store
	Service           *alertpush.Service
	Dispatcher        *alertpush.Dispatcher
	Handler           *alertpush.Handler
	SignedMedia       *alertpush.SignedMediaHandler
	Lifecycle         alertPushLifecycle
	ProtectedRoutes   RouteRegistrar `group:"protected_routes"`
	SignedMediaRoutes RouteRegistrar `group:"public_routes"`
}

type alertPushParams struct {
	fx.In

	DBProvider    database.DBProvider
	CameraCipher  *camera.LazyCipher
	CameraManager *camera.LifecycleManager
	MediaService  *mediafile.Service
	MediaHandler  *mediafile.Handler
	RecordService *record.Service
	SystemInfo    *systeminfo.IdentityService
	Config        Config
	Logger        *zap.Logger
}

func provideAlertPush(p alertPushParams) alertPushOut {
	comp := alertpush.NewComponents(alertpush.Options{
		DBProvider:   p.DBProvider,
		Cipher:       p.CameraCipher,
		MediaService: p.MediaService,
		MediaHandler: p.MediaHandler,
		DataDir:      p.Config.DataDir,
		Config: alertpush.Config{
			PublicBaseURL: p.Config.AlertPushPublicBaseURL,
			SignedURLTTL:  p.Config.AlertPushSignedURLTTL,
			DeviceID:      p.SystemInfo,
		},
		Logger: p.Logger,
	})

	p.CameraManager.AddEncryptedDataProbe(comp.Store)
	p.RecordService.SetAlarmListener(comp.Dispatcher)

	return alertPushOut{
		Store:             comp.Store,
		Service:           comp.Service,
		Dispatcher:        comp.Dispatcher,
		Handler:           comp.Handler,
		SignedMedia:       comp.SignedMediaHandler,
		Lifecycle:         comp.Lifecycle,
		ProtectedRoutes:   comp.ProtectedRoutes,
		SignedMediaRoutes: comp.SignedMediaRoutes,
	}
}

var alertPushModule = fx.Module("alertpush",
	fx.Provide(provideAlertPush),
)

// --- HTTP Module ---

var httpModule = fx.Module("http",
	fx.Provide(
		webui.FileSystem,
		newRouter,
		newHTTPServer,
	),
)

// --- Runtime Module ---

var runtimeModule = fx.Module("runtime",
	fx.Provide(newLifecycleRuntime),
	fx.Invoke(registerLifecycle),
)

// Lifecycle budgets. Fx defaults both to 15s, which is shorter than this host
// can legitimately need: startup runs SQLite migrations and initialises the
// native engine on eMMC, and shutdown drains the snapshot writer and the native
// log queue before destroying the engine. These values only have to be honest —
// exceeding a budget is not silent, Fx logs it and Run exits non-zero — so they
// stay well inside the 90s a default systemd TimeoutStopSec allows.
const (
	startupTimeout  = 60 * time.Second
	shutdownTimeout = 60 * time.Second
)

// Module contains the application's constructor graph and lifecycle registration.
var Module = fx.Options(
	fx.StartTimeout(startupTimeout),
	fx.StopTimeout(shutdownTimeout),
	fx.Provide(provideLogger),
	databaseModule,
	engineModule,
	nativeLogModule,
	auditModule,
	authModule,
	cameraModule,
	gb28181Module,
	networkModule,
	systemTimeModule,
	systemInfoModule,
	storageModule,
	mediaFileModule,
	algorithmModule,
	taskModule,
	asyncJobModule,
	personModule,
	clientLogModule,
	recordModule,
	spatialModule,
	apiTokenModule,
	openAPIModule,
	alertPushModule,
	httpModule,
	runtimeModule,
)

// New assembles the host. Constructors do not open files, allocate native state, or bind sockets.
func New(config Config) *fx.App {
	return fx.New(fx.Supply(config), Module)
}

func registerLifecycle(lifecycle fx.Lifecycle, runtime *lifecycleRuntime) {
	lifecycle.Append(fx.Hook{
		OnStart: runtime.Start,
		OnStop:  runtime.Stop,
	})
}
