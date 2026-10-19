package task

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// TaskComponents bundles all instantiated task components and adapters.
type TaskComponents struct {
	Store           *Store
	Service         *Service
	Handler         *Handler
	ProtectedRoutes RouteRegistrar
}

// NewTaskComponents instantiates and wires task components.
func NewTaskComponents(
	dbProvider database.DBProvider,
	algoProvider AlgorithmSpecProvider,
	listeners []TaskChangeListener,
	logger *zap.Logger,
) *TaskComponents {
	store := NewStore(dbProvider)
	service := NewService(store, algoProvider, listeners, logger)
	handler := NewHandler(service, logger)

	return &TaskComponents{
		Store:           store,
		Service:         service,
		Handler:         handler,
		ProtectedRoutes: handler,
	}
}
