package gb28181

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// RouteRegistrar matches the host's protected_routes value-group contract.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// Components bundles the GB28181 domain for assembly in internal/app.
type Components struct {
	Store           *Store
	Runtime         *Runtime
	Service         *Service
	Handler         *Handler
	Lifecycle       *LifecycleManager
	Ingress         Ingress
	ProtectedRoutes RouteRegistrar
}

// ComponentsOptions defines the host dependencies for the GB28181 domain.
type ComponentsOptions struct {
	DBProvider    database.DBProvider
	Cipher        Cipher
	CameraCreator CameraCreator
	CameraDeleter CameraDeleter
	Catalog       CatalogSyncer
	// Ingress overrides the SIP ingress. Production passes nil and gets the real
	// ingress; tests inject a stub to exercise the lifecycle without sockets.
	Ingress Ingress
	Addrs   InterfaceAddrs
	Logger  *zap.Logger
}

// NewComponents wires the domain as a pure constructor: no file access, no socket
// binding, no goroutines. Sockets are bound in LifecycleManager.Start.
func NewComponents(options ComponentsOptions) Components {
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}

	store := NewStore(options.DBProvider, options.Cipher)
	runtime := NewRuntime()
	ingress := options.Ingress
	if ingress == nil {
		ingress = newSIPIngress(
			store,
			runtime,
			options.Logger,
			defaultIngressLimits(),
		)
	}
	var syncer CatalogSyncer
	if options.Catalog != nil {
		syncer = options.Catalog
	} else if cs, ok := ingress.(CatalogSyncer); ok {
		syncer = cs
	}
	// Optional capabilities: a stub ingress in tests exposes neither, and both paths
	// degrade to "no stats" / "no reset" instead of failing the wiring.
	var stats IngressStatsProvider
	if provider, ok := ingress.(IngressStatsProvider); ok {
		stats = provider
	}

	service := NewService(ServiceOptions{
		Configs:       store,
		Devices:       store,
		Channels:      store,
		Catalog:       syncer,
		CameraCreator: options.CameraCreator,
		CameraDeleter: options.CameraDeleter,
		Runtime:       runtime,
		Addrs:         options.Addrs,
		Logger:        options.Logger,
		Ingress:       stats,
	})
	handler := NewHandler(service, options.Logger)
	lifecycle := NewLifecycleManager(LifecycleOptions{
		Repo:    store,
		Devices: store,
		Runtime: runtime,
		Ingress: ingress,
		Addrs:   options.Addrs,
		Logger:  options.Logger,
	})

	return Components{
		Store:           store,
		Runtime:         runtime,
		Service:         service,
		Handler:         handler,
		Lifecycle:       lifecycle,
		Ingress:         ingress,
		ProtectedRoutes: handler,
	}
}
