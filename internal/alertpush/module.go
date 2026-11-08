package alertpush

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"go.uber.org/zap"
)

// Options holds parameters required to instantiate AlertPush components.
type Options struct {
	DBProvider   database.DBProvider
	Cipher       Cipher
	MediaService *mediafile.Service
	MediaHandler *mediafile.Handler
	DataDir      string
	Config       Config
	Logger       *zap.Logger
}

// Components bundles the initialized singletons of the alertpush domain.
type Components struct {
	Store              *Store
	Service            *Service
	Dispatcher         *Dispatcher
	Handler            *Handler
	SignedMediaHandler *SignedMediaHandler
	Lifecycle          Lifecycle
	ProtectedRoutes    RouteRegistrar
	SignedMediaRoutes  RouteRegistrar
}

// Lifecycle controls startup and shutdown of the alert push dispatcher.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type alertPushLifecycleImpl struct {
	signer     *URLSigner
	dataDir    string
	dispatcher *Dispatcher
	logger     *zap.Logger
}

func (l *alertPushLifecycleImpl) Start(ctx context.Context) error {
	if l.signer != nil && l.dataDir != "" {
		if err := InitSignerFromDataDir(l.signer, l.dataDir); err != nil {
			l.logger.Warn("failed to load or create media signing key, signed URL feature may fail", zap.Error(err))
		}
	}
	if l.dispatcher != nil {
		if err := l.dispatcher.Start(ctx); err != nil {
			return err
		}
	}
	l.logger.Info("alertpush dispatcher started")
	return nil
}

func (l *alertPushLifecycleImpl) Stop(ctx context.Context) error {
	if l.dispatcher != nil {
		if err := l.dispatcher.Stop(ctx); err != nil {
			return err
		}
	}
	l.logger.Info("alertpush dispatcher stopped")
	return nil
}

// RouteRegistrar allows route registration onto a router group.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// NewComponents constructs all AlertPush components.
func NewComponents(opts Options) Components {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	signer := NewEmptyURLSigner()
	store := NewStore(opts.DBProvider, opts.Cipher)
	dispatcher := NewDispatcher(
		store,
		signer,
		opts.MediaService,
		opts.Config,
		logger,
	)
	service := NewService(
		store,
		dispatcher,
		signer,
		opts.MediaService,
		opts.Config,
		logger,
	)
	handler := NewHandler(service, store, logger)
	signedMediaHandler := NewSignedMediaHandler(signer, opts.MediaHandler, logger)

	lifecycle := &alertPushLifecycleImpl{
		signer:     signer,
		dataDir:    opts.DataDir,
		dispatcher: dispatcher,
		logger:     logger.Named("alertpush.lifecycle"),
	}

	return Components{
		Store:              store,
		Service:            service,
		Dispatcher:         dispatcher,
		Handler:            handler,
		SignedMediaHandler: signedMediaHandler,
		Lifecycle:          lifecycle,
		ProtectedRoutes:    handler,
		SignedMediaRoutes:  signedMediaHandler,
	}
}
