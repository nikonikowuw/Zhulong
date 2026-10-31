package person

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// PersonComponents groups instantiated person domain services.
type PersonComponents struct {
	Store           *Store
	Service         *Service
	Handler         *Handler
	JobHandler      *FaceImportJobHandler
	Lifecycle       PersonLifecycle
	ProtectedRoutes RouteRegistrar
	UploadRoutes    RouteRegistrar
}

// PersonLifecycle defines lifecycle hooks for person domain.
type PersonLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type personLifecycleImpl struct {
	service *Service
	logger  *zap.Logger
}

func (l *personLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("person service started")
	return nil
}

func (l *personLifecycleImpl) Stop(ctx context.Context) error {
	if l.service != nil {
		l.service.Close()
	}
	l.logger.Info("person service stopped")
	return nil
}

// RouteRegistrar allows route registration.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// NewPersonComponents constructs all person domain singletons.
//
// Every collaborator — including the optional gallery notifier and the record-domain
// key backfiller — is captured here. Nothing in this package supports swapping a
// dependency after construction.
func NewPersonComponents(
	dbProvider database.DBProvider,
	facesDir string,
	extractor FaceFeatureExtractor,
	notifier GalleryNotifier,
	opts Options,
	logger *zap.Logger,
) *PersonComponents {
	store := NewStore(dbProvider)
	service := NewService(store, facesDir, extractor, notifier, opts, logger)
	handler := NewHandler(service, logger)
	jobHandler := NewFaceImportJobHandler(service)

	return &PersonComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		JobHandler:      jobHandler,
		Lifecycle:       &personLifecycleImpl{service: service, logger: logger},
		ProtectedRoutes: handler,
		UploadRoutes:    handler.UploadRouteRegistrar(),
	}
}
