package spatial

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// SpatialComponents groups instantiated spatial domain services.
type SpatialComponents struct {
	Store           *Store
	Service         *Service
	Handler         *Handler
	Lifecycle       SpatialLifecycle
	ProtectedRoutes RouteRegistrar
	UploadRoutes    RouteRegistrar
}

// SpatialLifecycle defines lifecycle hooks for the spatial domain.
type SpatialLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type spatialLifecycleImpl struct {
	mapsDir string
	logger  *zap.Logger
}

// Start creates the on-disk home of the base maps.
//
// The directory belongs here, not in NewService: constructors only bind
// dependencies (dependency-injection.md §1.3), and a data directory that is not
// writable must surface as a start failure the runtime can report as degraded.
func (l *spatialLifecycleImpl) Start(ctx context.Context) error {
	if err := os.MkdirAll(l.mapsDir, 0o755); err != nil {
		return fmt.Errorf("create canvas map directory %s: %w", l.mapsDir, err)
	}
	l.logger.Info("spatial service started", zap.String("maps_dir", l.mapsDir))
	return nil
}

func (l *spatialLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("spatial service stopped")
	return nil
}

// RouteRegistrar allows route registration.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// NewSpatialComponents constructs all spatial domain singletons.
func NewSpatialComponents(
	dbProvider database.DBProvider,
	mapsDir string,
	logger *zap.Logger,
) *SpatialComponents {
	store := NewStore(dbProvider)
	service := NewService(store, mapsDir, logger)
	handler := NewHandler(service, logger)

	return &SpatialComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		Lifecycle:       &spatialLifecycleImpl{mapsDir: mapsDir, logger: logger},
		ProtectedRoutes: handler,
		UploadRoutes:    handler.UploadRouteRegistrar(),
	}
}
