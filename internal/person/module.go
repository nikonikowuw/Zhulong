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
	Lifecycle       PersonLifecycle
	ProtectedRoutes RouteRegistrar
}

// PersonLifecycle defines lifecycle hooks for person domain.
type PersonLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type personLifecycleImpl struct {
	logger *zap.Logger
}

func (l *personLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("person service started")
	return nil
}

func (l *personLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("person service stopped")
	return nil
}

// RouteRegistrar allows route registration.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// NewPersonComponents constructs all person domain singletons.
func NewPersonComponents(
	dbProvider database.DBProvider,
	auditor Auditor,
	logger *zap.Logger,
) *PersonComponents {
	store := NewStore(dbProvider)
	service := NewService(store, auditor, logger)
	handler := NewHandler(service, logger)

	return &PersonComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		Lifecycle:       &personLifecycleImpl{logger: logger},
		ProtectedRoutes: handler,
	}
}
