// Package spatial provides 2D floorplan canvas management, camera calibration,
// and spatio-temporal trajectory reconstruction.
package spatial

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
)

// Options configures optional spatial domain collaborators.
type Options struct {
	FileStore filestore.Store
}

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
	mapsDir   string
	fileStore filestore.Store
	logger    *zap.Logger
}

// Start prepares the local base maps directory when operating in local mode.
func (l *spatialLifecycleImpl) Start(ctx context.Context) error {
	if l.fileStore == nil || l.fileStore.Backend() == filestore.BackendLocal {
		if err := os.MkdirAll(l.mapsDir, 0o755); err != nil {
			return fmt.Errorf("create canvas map directory %q: %w", l.mapsDir, err)
		}
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

// NewSpatialComponents constructs all spatial domain singletons backed by LocalDriver on mapsDir.
func NewSpatialComponents(
	dbProvider database.DBProvider,
	mapsDir string,
	logger *zap.Logger,
) *SpatialComponents {
	return NewSpatialComponentsWithOptions(
		dbProvider,
		mapsDir,
		logger,
		Options{},
	)
}

// NewSpatialComponentsWithOptions constructs spatial domain singletons with optional filestore injection.
func NewSpatialComponentsWithOptions(
	dbProvider database.DBProvider,
	mapsDir string,
	logger *zap.Logger,
	opts Options,
) *SpatialComponents {
	store := NewStore(dbProvider)
	service := NewServiceWithStore(
		store,
		mapsDir,
		opts.FileStore,
		logger,
	)
	handler := NewHandler(service, logger)

	return &SpatialComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		Lifecycle:       &spatialLifecycleImpl{mapsDir: mapsDir, fileStore: service.fileStore, logger: logger},
		ProtectedRoutes: handler,
		UploadRoutes:    handler.UploadRouteRegistrar(),
	}
}
