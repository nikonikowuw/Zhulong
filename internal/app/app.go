package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/network"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"github.com/nikonikowuw/Zhulong/internal/systemtime"
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

func provideEngine() engineOut {
	eng := engine.New()
	return engineOut{
		Engine:    eng,
		Lifecycle: eng,
	}
}

var engineModule = fx.Module("engine",
	fx.Provide(provideEngine),
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

func provideAuth(dbProvider database.DBProvider, logger *zap.Logger) authOut {
	userStore := auth.NewUserStore(dbProvider)
	sessionStore := auth.NewMemorySessionStore(7 * 24 * time.Hour)
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

	Store        camera.CameraStore
	Service      *camera.CameraService
	StreamHub    *camera.StreamHub
	Handler      *camera.Handler
	Lifecycle    cameraLifecycle
	CameraRoutes RouteRegistrar `group:"protected_routes"`
}

func provideCamera(
	config Config,
	dbProvider database.DBProvider,
	eng *engine.Engine,
	authSvc auth.AuthService,
	auditSvc *audit.Service,
	logger *zap.Logger,
) cameraOut {
	camStore := camera.NewCameraStore(dbProvider)
	keyMgr := camera.NewKeyManager(config.DataDir)
	lazyC := camera.NewLazyCipher()
	reg := camera.NewStateRegistry()
	hub := camera.NewEventHub(reg)

	engAdapter := camera.NewEngineMediaAdapter(eng)
	streamHub := camera.NewStreamHub(engAdapter, camStore, lazyC, reg, hub, logger)
	prober := camera.NewProbeService(eng)
	describeClient := camera.NewDescribeClient()
	scheduler := camera.NewHealthScheduler(camStore, lazyC, reg, hub, describeClient, logger)
	camSvc := camera.NewCameraService(camStore, lazyC, prober, reg, hub, scheduler, logger)
	var validateSession func(string) bool
	if authSvc != nil {
		validateSession = func(token string) bool {
			_, ok := authSvc.ValidateSession(token)
			return ok
		}
	}
	camHandler := camera.NewHandler(camSvc, hub, streamHub, logger, validateSession)

	camMgr := camera.NewLifecycleManager(camStore, keyMgr, lazyC, scheduler, hub, streamHub)

	return cameraOut{
		Store:        camStore,
		Service:      camSvc,
		StreamHub:    streamHub,
		Handler:      camHandler,
		Lifecycle:    camMgr,
		CameraRoutes: camHandler,
	}
}

var cameraModule = fx.Module("camera",
	fx.Provide(provideCamera),
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
	comp := network.NewNetworkComponents(config.DataDir, config.CustomScript, logger)
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
	Supervisor         *algorithm.Supervisor
	Service            *algorithm.Service
	Handler            *algorithm.Handler
	Lifecycle          algorithmLifecycle
	GalleryBroadcaster *algorithm.GalleryBroadcaster
	ProtectedRoutes    RouteRegistrar `group:"protected_routes"`
	UploadRoutes       RouteRegistrar `group:"upload_routes"`
}

type appBrokerPathResolver struct {
	store     *algorithm.Store
	streamHub *camera.StreamHub
}

// SyncBrokerSubscriptions 解析某个算法包当前应当订阅的全部 FrameBroker socket 路径，
// 并把该包的订阅关系同步到它在 DB 中的最新任务集合。
//
// 它同时承担三件事，且三者必须在同一次同步里完成，否则订阅关系会与 DB 中的任务集合漂移：
//  1. 按该包 enabled 的任务解析出每一路相机的 AI 流路径（失败即整体失败）；
//  2. 提交订阅关系：新出现的相机 attach，已不在任务集合里的相机 release；
//  3. 按「该相机上所有 enabled 任务的 max(sample_fps)」下发发布节流率。
//
// ⚠️ 解析失败时不提交任何订阅变更，也不会遗留已 acquire 的流：
// 所有路径解析完毕后才进入提交阶段。
func (r *appBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	if r.store == nil || r.streamHub == nil {
		return nil, nil
	}
	tasks, err := r.store.ListTasksByAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, fmt.Errorf("list tasks for algorithm %s: %w", algorithmID, err)
	}

	// 该包要求订阅的相机及其本包内的最大采样率；order 保持 DB 的稳定顺序。
	wanted := make(map[string]struct{}, len(tasks))
	order := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		if _, err := algorithm.ValidateSampleFPS(task.SampleFPS); err != nil {
			return nil, fmt.Errorf("task %s: %w", task.ID, err)
		}
		if _, ok := wanted[task.CameraID]; ok {
			continue
		}
		wanted[task.CameraID] = struct{}{}
		order = append(order, task.CameraID)
	}

	// 阶段 1：解析全部路径。任一路失败则整体失败，不触碰订阅关系。
	paths := make([]string, 0, len(order))
	for _, cameraID := range order {
		path, err := r.streamHub.ResolveAIStreamBrokerPath(ctx, cameraID)
		if err != nil {
			return nil, fmt.Errorf("resolve broker path for camera %s: %w", cameraID, err)
		}
		paths = append(paths, path)
	}

	// 阶段 2：提交订阅关系。
	existing := r.streamHub.AICameraIDs()
	for _, cameraID := range order {
		r.streamHub.AttachAIStreamSubscriber(cameraID, algorithmID)
	}
	for _, cameraID := range existing {
		if _, ok := wanted[cameraID]; !ok {
			// 该相机已不在本包的任务集合中：注销订阅，最后一个订阅者离开时释放流与 broker。
			r.streamHub.ReleaseAIStreamSubscriber(cameraID, algorithmID)
		}
	}

	// 阶段 3：按全库 enabled 任务的 max(sample_fps) 下发节流率。
	// 一个 broker 只有一个发布率，因此必须跨算法包取 max（design.md §4.1）。
	rateByCamera, err := r.publishRateByCamera(ctx)
	if err != nil {
		return nil, err
	}
	for cameraID, fps := range rateByCamera {
		if err := r.streamHub.SetAIPublishRate(ctx, cameraID, fps); err != nil {
			return nil, fmt.Errorf("set AI publish rate for camera %s: %w", cameraID, err)
		}
	}

	return paths, nil
}

// publishRateByCamera 返回每个相机上所有 enabled 分析任务的最大采样帧率。
func (r *appBrokerPathResolver) publishRateByCamera(ctx context.Context) (map[string]uint32, error) {
	tasks, err := r.store.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list analysis tasks for publish rate: %w", err)
	}
	rates := make(map[string]uint32)
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		fps, err := algorithm.ValidateSampleFPS(task.SampleFPS)
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", task.ID, err)
		}
		if fps > rates[task.CameraID] {
			rates[task.CameraID] = fps
		}
	}
	return rates, nil
}

type personGalleryAdapter struct {
	broadcaster *algorithm.GalleryBroadcaster
}

func (a *personGalleryAdapter) BroadcastUpsert(ctx context.Context, item person.GalleryUpsertPayload) error {
	if a.broadcaster == nil {
		return nil
	}
	return a.broadcaster.BroadcastUpsert(ctx, algorithm.GalleryUpsertPayload{
		FaceID:     item.FaceID,
		PersonID:   item.PersonID,
		PersonName: item.PersonName,
		ImagePath:  item.ImagePath,
		Features:   item.Features,
	})
}

func (a *personGalleryAdapter) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	if a.broadcaster == nil {
		return nil
	}
	return a.broadcaster.BroadcastDelete(ctx, faceID, personID)
}

type supervisorGallerySyncAdapter struct {
	service *person.Service
}

func (a *supervisorGallerySyncAdapter) GetGallerySyncData(ctx context.Context) ([]algorithm.GallerySyncItem, error) {
	if a.service == nil {
		return nil, nil
	}
	items, err := a.service.GetGallerySyncData(ctx)
	if err != nil {
		return nil, err
	}
	algoItems := make([]algorithm.GallerySyncItem, len(items))
	for i, item := range items {
		algoItems[i] = algorithm.GallerySyncItem{
			FaceID:     item.FaceID,
			PersonID:   item.PersonID,
			PersonName: item.PersonName,
			ImagePath:  item.ImagePath,
			Features:   item.Features,
		}
	}
	return algoItems, nil
}

func provideAlgorithm(dbProvider database.DBProvider, streamHub *camera.StreamHub, eng *engine.Engine, config Config, logger *zap.Logger) algorithmOut {
	comp := algorithm.NewAlgorithmComponents(dbProvider, config.DataDir, config.AlgorithmTarget, logger)
	if streamHub != nil {
		comp.Supervisor.SetBrokerSubscriptionResolver(&appBrokerPathResolver{
			store:     comp.Store,
			streamHub: streamHub,
		})
	}
	galleryBroadcaster := algorithm.NewGalleryBroadcaster(eng, logger)
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

func providePerson(
	dbProvider database.DBProvider,
	config Config,
	eng *engine.Engine,
	broadcaster *algorithm.GalleryBroadcaster,
	supervisor *algorithm.Supervisor,
	logger *zap.Logger,
) personOut {
	facesDir := config.FacesDir
	if facesDir == "" {
		facesDir = filepath.Join(config.DataDir, "faces")
	}
	comp := person.NewPersonComponents(dbProvider, facesDir, eng, logger)
	if broadcaster != nil {
		comp.Service.SetGalleryNotifier(&personGalleryAdapter{broadcaster: broadcaster})
	}
	if supervisor != nil && broadcaster != nil {
		supervisor.SetGallerySync(&supervisorGallerySyncAdapter{service: comp.Service}, broadcaster)
	}
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

// Module contains the application's constructor graph and lifecycle registration.
var Module = fx.Options(
	fx.Provide(newLogger),
	databaseModule,
	engineModule,
	auditModule,
	authModule,
	cameraModule,
	networkModule,
	systemTimeModule,
	storageModule,
	mediaFileModule,
	algorithmModule,
	personModule,
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
