package app

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/nikonikowuw/Zhulong/internal/apidocs"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// HealthData describes the components required before the host serves requests.
type HealthData struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

type serviceReadiness interface {
	Ready() bool
}

func newRouter(logger *zap.Logger, database serviceReadiness, native serviceReadiness, assets http.FileSystem) *gin.Engine {
	router := gin.New()
	router.Use(httpmiddleware.Recovery(logger), httpmiddleware.RequestID())

	api := router.Group("/api/v1")
	api.Use(httpmiddleware.AccessLog(logger))
	api.GET("/health", healthHandler(database, native))

	swaggerHandler := ginSwagger.WrapHandler(swaggerFiles.Handler)
	router.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/swagger/index.html")
	})
	router.GET("/swagger/*any", swaggerHandler)

	staticHandler := http.FileServer(assets)
	router.NoRoute(func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		if isReservedPath(requestPath) {
			httputil.WriteError(c, httputil.NewError(http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", nil))
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		if path.Clean(requestPath) == "/" || path.Ext(requestPath) == "" {
			c.Request.URL.Path = "/"
		}
		staticHandler.ServeHTTP(c.Writer, c.Request)
	})
	router.NoMethod(func(c *gin.Context) {
		if isReservedPath(c.Request.URL.Path) {
			httputil.WriteError(c, httputil.NewError(http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", nil))
			return
		}
		c.Status(http.StatusNotFound)
	})
	router.HandleMethodNotAllowed = true
	return router
}

// healthHandler godoc
// @Summary      Get host health
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=HealthData}
// @Failure      503  {object}  httputil.Response
// @Router       /health [get]
func healthHandler(database serviceReadiness, native serviceReadiness) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !database.Ready() || !native.Ready() {
			httputil.WriteError(c, httputil.NewError(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Service is not ready", nil))
			return
		}
		httputil.Success(c, HealthData{
			Status: "ready",
			Components: map[string]string{
				"database": "ready",
				"engine":   "ready",
			},
		})
	}
}

func newHTTPServer(config Config, services *applicationServices, assets http.FileSystem) (*http.Server, error) {
	if err := validateHTTPAddress(config.HTTPAddress); err != nil {
		return nil, fmt.Errorf("validate HTTP address: %w", err)
	}

	router := newRouter(services.logger, services.database, services.native, assets)
	return &http.Server{
		Addr:              config.HTTPAddress,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}, nil
}

func isReservedPath(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") ||
		requestPath == "/swagger" || strings.HasPrefix(requestPath, "/swagger/")
}
