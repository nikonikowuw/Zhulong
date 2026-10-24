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
	service  *NetworkService
	provider *lazyProvider
	logger   *zap.Logger
}

func (l *networkLifecycleImpl) Start(ctx context.Context) error {
	// Detection is deferred to here so that dependency injection wiring stays side-effect free.
	l.provider.resolve()

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

// Options carries the host-specific inputs the network components need at construction time.
type Options struct {
	DataDir      string
	CustomScript string
	// HTTPAddress is the host listen address, e.g. "127.0.0.1:8000". Only its port is used.
	HTTPAddress string
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
//
// Construction is side-effect free: host network stack detection happens in
// NetworkLifecycle.Start (see lazyProvider), not here.
func NewNetworkComponents(opts Options, logger *zap.Logger) NetworkComponents {
	if logger == nil {
		logger = zap.NewNop()
	}

	provider := newLazyProvider(opts.CustomScript, logger)
	watchdog := NewWatchdogManager(opts.DataDir, provider, logger)
	service := NewNetworkService(provider, watchdog, portOf(opts.HTTPAddress), logger)
	handler := NewHandler(service)
	lifecycle := &networkLifecycleImpl{
		service:  service,
		provider: provider,
		logger:   logger,
	}

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
