package storage

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// StorageLifecycle defines start and stop actions for media storage service.
type StorageLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type storageLifecycleImpl struct {
	service *Service
	logger  *zap.Logger
}

func (l *storageLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("starting system storage lifecycle")
	return l.service.Start(ctx)
}

// RegisterWriteObserver implements the host's optional write-state hook. It is
// kept out of StorageLifecycle so the interface stays a lifecycle contract.
func (l *storageLifecycleImpl) RegisterWriteObserver(observer func(blocked bool)) {
	l.service.RegisterWriteObserver(observer)
}

func (l *storageLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("stopping system storage lifecycle")
	return l.service.Stop(ctx)
}

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// StorageComponents bundles all instantiated media storage components and adapters.
type StorageComponents struct {
	Repository      Repository
	Inspector       PathInspector
	Gate            *EmergencyGate
	Cleaner         *CleanerEngine
	Service         *Service
	Handler         *Handler
	Lifecycle       StorageLifecycle
	ProtectedRoutes RouteRegistrar
}

// NewStorageComponents instantiates and wires media storage components.
func NewStorageComponents(
	dbProvider database.DBProvider,
	auditor *audit.Service,
	logger *zap.Logger,
) StorageComponents {
	if logger == nil {
		logger = zap.NewNop()
	}

	repo := NewRepository(dbProvider, logger)
	inspector := NewDefaultPathInspector()
	gate := NewEmergencyGate(logger)
	cleaner := NewCleanerEngine(inspector, logger)
	svc := NewService(ServiceDeps{
		Repo:      repo,
		Inspector: inspector,
		Gate:      gate,
		Cleaner:   cleaner,
		Logger:    logger,
	})
	if auditor != nil {
		svc.SetAuditor(auditor)
	}

	handler := NewHandler(svc, logger)
	lifecycle := &storageLifecycleImpl{
		service: svc,
		logger:  logger,
	}

	return StorageComponents{
		Repository:      repo,
		Inspector:       inspector,
		Gate:            gate,
		Cleaner:         cleaner,
		Service:         svc,
		Handler:         handler,
		Lifecycle:       lifecycle,
		ProtectedRoutes: handler,
	}
}
