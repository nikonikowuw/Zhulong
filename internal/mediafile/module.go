package mediafile

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RouteRegistrar mounts this module's routes onto a Gin router group.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// MediaFileComponents bundles the wired media file service and its handler.
type MediaFileComponents struct {
	Service         *Service
	Handler         *Handler
	ProtectedRoutes RouteRegistrar
}

// NewMediaFileComponents constructs the media file module.
//
// The constructor is pure: it binds dependencies and allocates memory only. It
// opens no file and builds no os.Root handle, so application startup cannot
// fail because the media volume is not mounted yet.
func NewMediaFileComponents(
	provider MediaRootProvider,
	inspector DeviceInspector,
	logger *zap.Logger,
) MediaFileComponents {
	service := NewService(provider, inspector, logger)
	handler := NewHandler(service)

	return MediaFileComponents{
		Service:         service,
		Handler:         handler,
		ProtectedRoutes: handler,
	}
}
