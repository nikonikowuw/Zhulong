package systeminfo

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SystemInfoLifecycle defines start and stop actions for system hardware monitoring service.
type SystemInfoLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type systemInfoLifecycleImpl struct {
	service *Service
	logger  *zap.Logger
}

func (l *systemInfoLifecycleImpl) Start(ctx context.Context) error {
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
	Handler         *Handler
	Lifecycle       SystemInfoLifecycle
	ProtectedRoutes RouteRegistrar
}

// NewSystemInfoComponents instantiates and wires systeminfo components.
func NewSystemInfoComponents(logger *zap.Logger) SystemInfoComponents {
	if logger == nil {
		logger = zap.NewNop()
	}

	collector := NewDefaultCollector()
	svc := NewService(collector, logger)
	handler := NewHandler(svc, logger)
	lifecycle := &systemInfoLifecycleImpl{
		service: svc,
		logger:  logger,
	}

	return SystemInfoComponents{
		Collector:       collector,
		Service:         svc,
		Handler:         handler,
		Lifecycle:       lifecycle,
		ProtectedRoutes: handler,
	}
}
