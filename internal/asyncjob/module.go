package asyncjob

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// RouteRegistrar allows route registration.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// AsyncJobComponents groups instantiated async job services.
type AsyncJobComponents struct {
	Store           *Store
	Service         *Service
	Handler         *Handler
	ProtectedRoutes RouteRegistrar
}

// NewAsyncJobComponents constructs all asyncjob domain singletons.
func NewAsyncJobComponents(
	dbProvider database.DBProvider,
	logger *zap.Logger,
) *AsyncJobComponents {
	store := NewStore(dbProvider)
	service := NewService(store, logger)
	handler := NewHandler(service)

	return &AsyncJobComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		ProtectedRoutes: handler,
	}
}

// Start starts the async job background queue and recovers orphan jobs.
func (c *AsyncJobComponents) Start(ctx context.Context) error {
	return c.Service.Start(ctx)
}

// Stop stops the worker queue and waits for in-flight tasks.
func (c *AsyncJobComponents) Stop(ctx context.Context) error {
	return c.Service.Stop(ctx)
}
