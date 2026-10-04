package app

import (
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/webui"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type applicationServices struct {
	database *database.Store
	native   *engine.Engine
	logger   *zap.Logger
}

// Module contains the application's constructor graph and lifecycle registration.
var Module = fx.Options(
	fx.Provide(
		newLogger,
		newServices,
		webui.FileSystem,
		newHTTPServer,
		newLifecycleRuntime,
	),
	fx.Invoke(registerLifecycle),
)

// New assembles the host. Constructors do not open files, allocate native state, or bind sockets.
func New(config Config) *fx.App {
	return fx.New(fx.Supply(config), Module)
}

func newServices(config Config, logger *zap.Logger) *applicationServices {
	return &applicationServices{
		database: database.New(config.DataDir, logger),
		native:   engine.New(),
		logger:   logger,
	}
}

func registerLifecycle(lifecycle fx.Lifecycle, runtime *lifecycleRuntime) {
	lifecycle.Append(fx.Hook{
		OnStart: runtime.Start,
		OnStop:  runtime.Stop,
	})
}
