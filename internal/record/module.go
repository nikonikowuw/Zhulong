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
	Pruner          *RetentionPruner
	Lifecycle       RecordLifecycle
	ProtectedRoutes RouteRegistrar
}

// RecordLifecycle defines lifecycle hooks for record domain.
type RecordLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type recordLifecycleImpl struct {
	logger  *zap.Logger
	pruner  *RetentionPruner
	service *Service
}

func (l *recordLifecycleImpl) Start(ctx context.Context) error {
	if l.service != nil {
		l.service.Start(ctx)
		// 历史告警图可能早于「先写锁再写图」的约定；优先补锁，guard 仍作为兜底。
		if err := l.service.EnsureAlarmEvidenceLocks(ctx); err != nil {
			l.logger.Warn("alarm evidence lock backfill failed; storage evidence guard remains the fallback",
				zap.Error(err))
		}
	}
	// 保留清理在应用生命周期 context 下后台巡检。
	if l.pruner != nil {
		l.pruner.Start(ctx)
	}
	l.logger.Info("record service started")
	return nil
}

func (l *recordLifecycleImpl) Stop(ctx context.Context) error {
	if l.service != nil {
		l.service.Stop()
	}
	// 先停清理：它要写 SQLite，必须在 native/数据库关闭之前退出。
	if l.pruner != nil {
		l.pruner.Stop()
	}
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
	service := NewService(store, logger)
	handler := NewHandler(service)
	pruner := NewRetentionPruner(store, logger, RetentionOptions{})

	return RecordComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		Pruner:          pruner,
		Lifecycle:       &recordLifecycleImpl{logger: logger.Named("record.lifecycle"), pruner: pruner, service: service},
		ProtectedRoutes: handler,
	}
}
