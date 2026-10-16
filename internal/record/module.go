package record

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// RecordComponents groups instantiated record domain services.
type RecordComponents struct {
	Store           *Store
	Service         *Service
	Handler         *Handler
	Lifecycle       RecordLifecycle
	ProtectedRoutes RouteRegistrar
}

// RecordLifecycle defines lifecycle hooks for record domain.
type RecordLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type recordLifecycleImpl struct {
	logger *zap.Logger
}

func (l *recordLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("record service started")
	return nil
}

func (l *recordLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("record service stopped")
	return nil
}

// RouteRegistrar allows route registration onto a router group.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// NewRecordComponents constructs all record domain singletons.
func NewRecordComponents(
	dbProvider database.DBProvider,
	logger *zap.Logger,
) RecordComponents {
	if logger == nil {
		logger = zap.NewNop()
	}
	store := NewStore(dbProvider)
	service := NewService(store, dbProvider, logger)
	handler := NewHandler(service, logger)

	return RecordComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		Lifecycle:       &recordLifecycleImpl{logger: logger.Named("record.lifecycle")},
		ProtectedRoutes: handler,
	}
}
