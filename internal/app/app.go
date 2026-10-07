package app

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/webui"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type applicationServices struct {
	database      *database.Store
	native        *engine.Engine
	auth          auth.AuthService
	auditSvc      *audit.Service
	auditHandler  *audit.Handler
	cameraSvc     *camera.CameraService
	cameraHub     *camera.EventHub
	cameraStream  *camera.StreamHub
	cameraHandler *camera.Handler
	cameraMgr     *camera.LifecycleManager
	logger        *zap.Logger
}

type servicesOut struct {
	fx.Out

	Database      *database.Store
	Native        *engine.Engine
	Auth          auth.AuthService
	AuditSvc      *audit.Service
	AuditHandler  *audit.Handler
	CameraSvc     *camera.CameraService
	CameraHub     *camera.EventHub
	CameraStream  *camera.StreamHub
	CameraHandler *camera.Handler
	CameraMgr     *camera.LifecycleManager
	Services      *applicationServices
	PublicRoutes  RouteRegistrar `group:"public_routes"`
	CameraRoutes  RouteRegistrar `group:"protected_routes"`
	AuditRoutes   RouteRegistrar `group:"protected_routes"`
}

// Module contains the application's constructor graph and lifecycle registration.
var Module = fx.Options(
	fx.Provide(
		newLogger,
		newServices,
		webui.FileSystem,
		newRouter,
		newHTTPServer,
		newLifecycleRuntime,
	),
	fx.Invoke(registerLifecycle),
)

// New assembles the host. Constructors do not open files, allocate native state, or bind sockets.
func New(config Config) *fx.App {
	return fx.New(fx.Supply(config), Module)
}

func newServices(config Config, logger *zap.Logger) servicesOut {
	dbStore := database.New(config.DataDir, logger)
	userStore := auth.NewUserStore(dbStore.DB)
	sessionStore := auth.NewMemorySessionStore(7 * 24 * time.Hour)
	authSvc := auth.NewAuthService(userStore, sessionStore, nil, logger)

	camStore := camera.NewCameraStore(dbStore.DB)
	keyMgr := camera.NewKeyManager(config.DataDir)
	lazyC := camera.NewLazyCipher()
	reg := camera.NewStateRegistry()
	hub := camera.NewEventHub(reg)

	eng := engine.New()
	engAdapter := camera.NewEngineMediaAdapter(eng)
	streamHub := camera.NewStreamHub(engAdapter, camStore, lazyC, reg, hub, logger)
	prober := camera.NewProbeService(eng)
	describeClient := camera.NewDescribeClient()
	scheduler := camera.NewHealthScheduler(camStore, lazyC, reg, hub, describeClient, logger)
	camSvc := camera.NewCameraService(camStore, lazyC, prober, reg, hub, scheduler, logger)
	camHandler := camera.NewHandler(camSvc, hub, streamHub, logger, func(token string) bool {
		_, ok := authSvc.ValidateSession(token)
		return ok
	})

	camMgr := camera.NewLifecycleManager(camStore, keyMgr, lazyC, scheduler, hub, streamHub)

	auditStore := audit.NewStore(dbStore.DB)
	auditSvc := audit.NewService(auditStore, logger)
	auditHandler := audit.NewHandler(auditSvc, logger)

	authHandler := auth.NewHandler(authSvc, auditSvc)
	camHandler.SetAuditor(auditSvc)

	appServices := &applicationServices{
		database:      dbStore,
		native:        eng,
		auth:          authSvc,
		auditSvc:      auditSvc,
		auditHandler:  auditHandler,
		cameraSvc:     camSvc,
		cameraHub:     hub,
		cameraStream:  streamHub,
		cameraHandler: camHandler,
		cameraMgr:     camMgr,
		logger:        logger,
	}

	return servicesOut{
		Database:      dbStore,
		Native:        eng,
		Auth:          authSvc,
		AuditSvc:      auditSvc,
		AuditHandler:  auditHandler,
		CameraSvc:     camSvc,
		CameraHub:     hub,
		CameraStream:  streamHub,
		CameraHandler: camHandler,
		CameraMgr:     camMgr,
		Services:      appServices,
		PublicRoutes:  authHandler,
		CameraRoutes:  camHandler,
		AuditRoutes:   auditHandler,
	}
}

func registerLifecycle(lifecycle fx.Lifecycle, runtime *lifecycleRuntime) {
	lifecycle.Append(fx.Hook{
		OnStart: runtime.Start,
		OnStop:  runtime.Stop,
	})
}
