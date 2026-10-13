package algorithm

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// AlgorithmLifecycle defines start and stop actions for algorithm service.
type AlgorithmLifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type algorithmLifecycleImpl struct {
	service *Service
	logger  *zap.Logger
}

func (l *algorithmLifecycleImpl) Start(ctx context.Context) error {
	l.logger.Info("Starting algorithm management lifecycle...")
	// Auto-start active algorithms on host boot
	algos, err := l.service.ListAlgorithms(ctx)
	if err == nil {
		for _, algo := range algos {
			if algo.Status == StatusActive {
				l.logger.Info("auto-starting active algorithm", zap.String("id", algo.ID))
				if startErr := l.service.supervisor.StartAlgorithm(ctx, algo); startErr != nil {
					l.logger.Error("failed to auto-start active algorithm",
						zap.String("id", algo.ID),
						zap.Error(startErr),
					)
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
	auditor Auditor,
	dataDir string,
	hostTarget string,
	logger *zap.Logger,
) *AlgorithmComponents {
	store := NewStore(dbProvider)
	runnerPath := os.Getenv("ZHULONG_ALGO_RUNNER")
	if runnerPath == "" {
		runnerPath = "build/native/host/zhulong_algo_runner"
	}
	brokerSock := os.Getenv("ZHULONG_FRAME_BROKER_SOCK")
	if brokerSock == "" {
		// ⚠️ The runner CLIENT treats this value as a LITERAL socket path
		// (UnixSocketClient::connect does a bare connect(), no derivation),
		// while the FrameBroker SERVER derives
		// <base_dir>/zhulong_broker_<stream_id>.sock from its own base_dir
		// (frame_broker.cpp:20). The two sides are asymmetric and no broker
		// host exists yet, so any value fails with exit 5. A literal path is
		// kept because it yields ENOENT ("no such file"), which honestly
		// signals "host not implemented"; a directory yields ECONNREFUSED,
		// which misleadingly suggests "host is running but refused".
		//
		// The directory-vs-literal-path decision is deferred to the broker
		// host design in 10-10-algorithm-plugin-architecture. See
		// .trellis/spec/native/algo-plugin-contract.md §5.1.
		//
		// The pre-existing value is deliberately kept: Go has no stream_id
		// (that is a media-layer concept), so it cannot derive a correct
		// per-stream path. Inventing one here would be a guess.
		brokerSock = "/tmp/zhulong_framebroker.sock"
	}
	installDir := filepath.Join(dataDir, "algorithms")

	verifier := NewVerifier(runnerPath, hostTarget)
	supervisor := NewSupervisor(store, installDir, runnerPath, brokerSock, DefaultRecoveryOptions(), logger)
	service := NewService(store, verifier, supervisor, installDir, logger)
	handler := NewHandler(service, auditor, logger)

	return &AlgorithmComponents{
		Store:           store,
		Verifier:        verifier,
		Supervisor:      supervisor,
		Service:         service,
		Handler:         handler,
		Lifecycle:       &algorithmLifecycleImpl{service: service, logger: logger},
		ProtectedRoutes: handler,
		UploadRoutes:    handler.UploadRouteRegistrar(),
	}
}
