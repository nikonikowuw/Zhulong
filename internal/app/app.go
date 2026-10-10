package app

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/network"
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
	Handler     *audit.Handler
	Lifecycle   auditLifecycle
	AuditRoutes RouteRegistrar `group:"protected_routes"`
}

func provideAudit(dbProvider database.DBProvider, logger *zap.Logger) auditOut {
	store := audit.NewStore(dbProvider)
	svc := audit.NewService(store, logger)
	handler := audit.NewHandler(svc, logger)
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

func provideAuth(dbProvider database.DBProvider, auditSvc *audit.Service, logger *zap.Logger) authOut {
	userStore := auth.NewUserStore(dbProvider)
	sessionStore := auth.NewMemorySessionStore(7 * 24 * time.Hour)
	authSvc := auth.NewAuthService(userStore, sessionStore, nil, logger)
	handler := auth.NewHandler(authSvc, auditSvc)
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
	if auditSvc != nil {
		camHandler.SetAuditor(auditSvc)
	}

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
