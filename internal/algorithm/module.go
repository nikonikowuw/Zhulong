package algorithm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// AlgorithmLifecycle defines start and stop actions for algorithm service.
type AlgorithmLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type algorithmLifecycleImpl struct {
	service    *Service
	runnerPath string
	logger     *zap.Logger
}

func (l *algorithmLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("Starting algorithm management lifecycle...")

	// Reclaim runners and artefacts left by a previous host process BEFORE any
	// auto-start. Pdeathsig stops new orphans from surviving a crash, but it
	// cannot touch runners orphaned before this fix shipped, and it never removes
	// the socket / shm files they were holding. A leftover runner would otherwise
	// stay pinned to no supervisor while burning a full CPU core, and a stale shm
	// segment would make the next stream fail to attach a broker.
	//
	// The sweep only acts on processes whose argv[0] is our runner binary and
	// whose parent is not another live host, so a second running instance is safe.
	if report, sweepErr := algorunner.ReapOrphanRunners(ctx, l.runnerPath, l.logger); sweepErr != nil {
		// Never block boot on a cleanup failure: the host is still usable, and a
		// stubborn leftover is reported by the supervisor's own crash handling.
		l.logger.Warn("startup orphan sweep reported problems",
			zap.Ints("killed_pids", report.Killed),
			zap.Strings("removed_sockets", report.RemovedSockets),
			zap.Strings("removed_shm", report.RemovedShm),
			zap.Error(sweepErr),
		)
	}

	// Auto-start active algorithms on host boot
	algos, err := l.service.ListAlgorithms(ctx)
	if err == nil {
		for _, algo := range algos {
			if algo.Status == StatusActive {
				l.logger.Info("auto-starting active algorithm", zap.String("id", algo.ID))
				if startErr := l.service.supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); startErr != nil {
					if errors.Is(startErr, ErrNoBrokerSockets) {
						l.logger.Info("active algorithm held in standby state on host startup (no active tasks)", zap.String("id", algo.ID))
					} else {
						l.logger.Error("failed to auto-start active algorithm",
							zap.String("id", algo.ID),
							zap.Error(startErr),
						)
					}
				}
			}
		}
	} else {
		l.logger.Error("failed to list algorithms on lifecycle startup", zap.Error(err))
	}
	return nil
}

func (l *algorithmLifecycleImpl) Stop(ctx context.Context) error {
	l.logger.Info("Stopping algorithm management lifecycle (stopping runner processes)...")
	l.service.StopAll(3 * time.Second)
	return nil
}

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// AlgorithmComponents bundles all instantiated algorithm components and adapters.
type AlgorithmComponents struct {
	Store           *Store
	Verifier        *Verifier
	Supervisor      *Supervisor
	Service         *Service
	Handler         *Handler
	Lifecycle       AlgorithmLifecycle
	ProtectedRoutes RouteRegistrar
	UploadRoutes    RouteRegistrar
}

// NewAlgorithmComponents instantiates and wires algorithm components.
func NewAlgorithmComponents(
	dbProvider database.DBProvider,
	dataDir string,
	hostTarget string,
	logger *zap.Logger,
) *AlgorithmComponents {
	store := NewStore(dbProvider)
	runnerPath := os.Getenv("ZHULONG_ALGO_RUNNER")
	if runnerPath == "" {
		runnerPath = "build/native/host/zhulong_algo_runner"
	}
	installDir := filepath.Join(dataDir, "algorithms")

	verifier := NewVerifier(runnerPath, hostTarget)
	supervisor := NewSupervisor(store, installDir, runnerPath, DefaultRecoveryOptions(), logger)
	service := NewService(store, verifier, supervisor, installDir, logger)
	handler := NewHandler(service, logger)

	return &AlgorithmComponents{
		Store:           store,
		Verifier:        verifier,
		Supervisor:      supervisor,
		Service:         service,
		Handler:         handler,
		Lifecycle:       &algorithmLifecycleImpl{service: service, runnerPath: runnerPath, logger: logger},
		ProtectedRoutes: handler,
		UploadRoutes:    handler.UploadRouteRegistrar(),
	}
}
