package systeminfo

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// SystemInfoLifecycle defines start and stop actions for system hardware monitoring service.
type SystemInfoLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type systemInfoLifecycleImpl struct {
	service  *Service
	identity *IdentityService
	logger   *zap.Logger
}

func (l *systemInfoLifecycleImpl) Start(ctx context.Context) error {
	// Identity loads first: dispatchers read it right after startup and a late load
	// would leave the first pushed events unattributable.
	if l.identity != nil {
		if err := l.identity.Start(ctx); err != nil {
			return err
		}
	}
	l.logger.Info("Starting system hardware telemetry service...")
	return l.service.Start(ctx)
}

func (l *systemInfoLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("Stopping system hardware telemetry service...")
	return l.service.Stop(ctx)
}

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// SystemInfoComponents bundles all instantiated hardware telemetry services and adapters.
type SystemInfoComponents struct {
	Collector       Collector
	Service         *Service
	Identity        *IdentityService
	Handler         *Handler
	Lifecycle       SystemInfoLifecycle
	ProtectedRoutes RouteRegistrar
}

// NewSystemInfoComponents instantiates and wires systeminfo components.
func NewSystemInfoComponents(dbProvider database.DBProvider, logger *zap.Logger) SystemInfoComponents {
	if logger == nil {
		logger = zap.NewNop()
	}

	collector := NewDefaultCollector()
	svc := NewService(collector, logger)
	identity := NewIdentityService(dbProvider, logger)
	handler := NewHandler(svc, identity, logger)
	lifecycle := &systemInfoLifecycleImpl{
		service:  svc,
		identity: identity,
		logger:   logger,
	}

	return SystemInfoComponents{
		Collector:       collector,
		Service:         svc,
		Identity:        identity,
		Handler:         handler,
		Lifecycle:       lifecycle,
		ProtectedRoutes: handler,
	}
}
