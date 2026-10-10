package systemtime

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// TimeLifecycle defines start and stop actions for system time service.
type TimeLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type timeLifecycleImpl struct {
	service *TimeService
	logger  *zap.Logger
}

func (l *timeLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("Starting system time lifecycle...")
	if err := l.service.OnBootCheck(ctx); err != nil {
		l.logger.Warn("System time on-boot check encountered warning", zap.Error(err))
	}
	return l.service.Start(ctx)
}

func (l *timeLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("Stopping system time lifecycle...")
	return l.service.Stop(ctx)
}

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// TimeComponents bundles all instantiated system time services and adapters.
type TimeComponents struct {
	Driver          ClockDriver
	Repository      Repository
	Service         *TimeService
	Handler         *Handler
	Lifecycle       TimeLifecycle
	ProtectedRoutes RouteRegistrar
}

// NewTimeComponents instantiates and wires system time components.
func NewTimeComponents(
	dbProvider database.DBProvider,
	auditor *audit.Service,
	logger *zap.Logger,
) TimeComponents {
	if logger == nil {
		logger = zap.NewNop()
	}

	driver := NewDefaultClockDriver()
	repo := NewRepository(dbProvider)
	sntpClient := NewSNTPClient()
	svc := NewTimeService(repo, driver, sntpClient, logger)
	if auditor != nil {
		svc.SetAuditor(auditor)
	}

	handler := NewHandler(svc, logger)
	lifecycle := &timeLifecycleImpl{
		service: svc,
		logger:  logger,
	}

	return TimeComponents{
		Driver:          driver,
		Repository:      repo,
		Service:         svc,
		Handler:         handler,
		Lifecycle:       lifecycle,
		ProtectedRoutes: handler,
	}
}
