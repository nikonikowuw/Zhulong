package network

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// NetworkLifecycle manages host network startup checks (e.g. boot auto-rollback).
type NetworkLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type networkLifecycleImpl struct {
	service *NetworkService
	logger  *zap.Logger
}

func (l *networkLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("Checking network watchdog boot status...")
	return l.service.OnBootCheck(ctx)
}

func (l *networkLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("Stopping network service and disarming transient operations...")
	return l.service.Stop(ctx)
}

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// NetworkComponents bundles all instantiated network services and adapters.
type NetworkComponents struct {
	Provider        NetworkProvider
	Watchdog        *WatchdogManager
	Service         *NetworkService
	Handler         *Handler
	Lifecycle       NetworkLifecycle
	ProtectedRoutes RouteRegistrar
	PublicRoutes    RouteRegistrar
}

// NewNetworkComponents creates and wires the network module components.
func NewNetworkComponents(dataDir string, customScript string, logger *zap.Logger) NetworkComponents {
	if logger == nil {
		logger = zap.NewNop()
	}

	provider := DetectProvider(customScript, logger)
	watchdog := NewWatchdogManager(dataDir, provider, logger)
	service := NewNetworkService(provider, watchdog, logger)
	handler := NewHandler(service, logger)
	lifecycle := &networkLifecycleImpl{service: service, logger: logger}

	return NetworkComponents{
		Provider:        provider,
		Watchdog:        watchdog,
		Service:         service,
		Handler:         handler,
		Lifecycle:       lifecycle,
		ProtectedRoutes: handler,
		PublicRoutes:    handler.PublicRouteRegistrar(),
	}
}
