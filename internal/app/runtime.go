package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

type databaseLifecycle interface {
	OpenAndMigrate(context.Context) error
	Close() error
}

type engineLifecycle interface {
	Start() error
	Close() error
}

type cameraLifecycle interface {
	InitCipher(context.Context) error
	Start(context.Context) error
	Stop()
}

type auditLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type lifecycleRuntime struct {
	database databaseLifecycle
	native   engineLifecycle
	camera   cameraLifecycle
	audit    auditLifecycle
	server   *http.Server
	logger   *zap.Logger
	listen   func(string, string) (net.Listener, error)
	shutdown func(context.Context) error

	listener      net.Listener
	serveDone     chan error
	databaseReady bool
	nativeReady   bool
	cameraReady   bool
	auditReady    bool
	httpReady     bool
	loggerSynced  bool
}

type runtimeParams struct {
	fx.In

	Database databaseLifecycle
	Native   engineLifecycle
	Camera   cameraLifecycle `optional:"true"`
	Audit    auditLifecycle  `optional:"true"`
	Server   *http.Server
	Logger   *zap.Logger
}

func newLifecycleRuntime(p runtimeParams) *lifecycleRuntime {
	var shutdown func(context.Context) error
	if p.Server != nil {
		shutdown = p.Server.Shutdown
	}
	return &lifecycleRuntime{
		database: p.Database,
		native:   p.Native,
		camera:   p.Camera,
		audit:    p.Audit,
		server:   p.Server,
		logger:   p.Logger,
		listen:   net.Listen,
		shutdown: shutdown,
	}
}

func (r *lifecycleRuntime) Start(ctx context.Context) error {
	if r.databaseReady || r.nativeReady || r.httpReady {
		return errors.New("application is already started")
	}
	if err := r.database.OpenAndMigrate(ctx); err != nil {
		startupErr := fmt.Errorf("open database and apply migrations: %w", err)
		return errors.Join(startupErr, r.database.Close(), r.syncLogger())
	}
	r.databaseReady = true

	if r.audit != nil {
		if err := r.audit.Start(ctx); err != nil {
			startupErr := fmt.Errorf("start audit service: %w", err)
			return errors.Join(startupErr, r.closeDatabase(), r.syncLogger())
		}
		r.auditReady = true
	}

	if err := r.native.Start(); err != nil {
		startupErr := fmt.Errorf("start native engine: %w", err)
		return errors.Join(startupErr, r.closeAudit(ctx), r.closeNative(), r.closeDatabase(), r.syncLogger())
	}
	r.nativeReady = true

	if r.camera != nil {
		if err := r.camera.InitCipher(ctx); err != nil {
			startupErr := fmt.Errorf("initialize camera cipher: %w", err)
			return errors.Join(startupErr, r.closeAudit(ctx), r.closeNative(), r.closeDatabase(), r.syncLogger())
		}
		if err := r.camera.Start(ctx); err != nil {
			startupErr := fmt.Errorf("start camera scheduler: %w", err)
			r.camera.Stop()
			return errors.Join(startupErr, r.closeAudit(ctx), r.closeNative(), r.closeDatabase(), r.syncLogger())
		}
		r.cameraReady = true
	}

	listener, err := r.listen("tcp", r.server.Addr)
	if err != nil {
		startupErr := fmt.Errorf("listen on %s: %w", r.server.Addr, err)
		return errors.Join(startupErr, r.closeCamera(), r.closeAudit(ctx), r.closeNative(), r.closeDatabase(), r.syncLogger())
	}
	r.listener = listener
	r.serveDone = make(chan error, 1)
	go func() {
		serveErr := r.server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			r.logger.Error("HTTP server stopped unexpectedly", zap.Error(serveErr))
		}
		r.serveDone <- serveErr
		close(r.serveDone)
	}()
	r.httpReady = true
	r.logger.Info("HTTP server started", zap.String("address", listener.Addr().String()))
	return nil
}

func (r *lifecycleRuntime) Stop(ctx context.Context) error {
	var stopErrors []error

	if r.httpReady {
		if err := r.shutdown(ctx); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("shut down HTTP server: %w", err))
			if closeErr := r.server.Close(); closeErr != nil {
				stopErrors = append(stopErrors, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		}
		select {
		case serveErr := <-r.serveDone:
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				stopErrors = append(stopErrors, fmt.Errorf("HTTP server: %w", serveErr))
			}
		case <-ctx.Done():
			if closeErr := r.server.Close(); closeErr != nil {
				stopErrors = append(stopErrors, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		}
		r.httpReady = false
		r.listener = nil
	}

	if err := r.closeCamera(); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeAudit(ctx); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeNative(); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.closeDatabase(); err != nil {
		stopErrors = append(stopErrors, err)
	}
	if err := r.syncLogger(); err != nil {
		stopErrors = append(stopErrors, err)
	}
	return errors.Join(stopErrors...)
}

func (r *lifecycleRuntime) closeCamera() error {
	if !r.cameraReady {
		return nil
	}
	r.cameraReady = false
	if r.camera != nil {
		r.camera.Stop()
	}
	return nil
}

func (r *lifecycleRuntime) closeAudit(ctx context.Context) error {
	if !r.auditReady {
		return nil
	}
	r.auditReady = false
	if r.audit != nil {
		if err := r.audit.Stop(ctx); err != nil {
			return fmt.Errorf("stop audit service: %w", err)
		}
	}
	return nil
}

func (r *lifecycleRuntime) closeNative() error {
	if !r.nativeReady {
		return nil
	}
	r.nativeReady = false
	if err := r.native.Close(); err != nil {
		return fmt.Errorf("close native engine: %w", err)
	}
	return nil
}

func (r *lifecycleRuntime) closeDatabase() error {
	if !r.databaseReady {
		return nil
	}
	r.databaseReady = false
	if err := r.database.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}

func (r *lifecycleRuntime) syncLogger() error {
	if r.loggerSynced {
		return nil
	}
	r.loggerSynced = true
	if r.logger == nil {
		return nil
	}
	if err := r.logger.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTTY) {
		return fmt.Errorf("sync application logger: %w", err)
	}
	return nil
}
