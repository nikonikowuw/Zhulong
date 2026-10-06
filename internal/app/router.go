package app

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/nikonikowuw/Zhulong/internal/apidocs"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// HealthData describes the components required before the host serves requests.
type HealthData struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

// ServiceReadiness represents a component reporting readiness for health checks.
type ServiceReadiness interface {
	Ready() bool
}

// RouteRegistrar defines a modular component that mounts its HTTP routes onto a Gin RouterGroup.
// Any feature module (auth, camera, recording, ai, etc.) implementing this interface can be
// automatically collected via Uber Fx Value Groups without modifying the router.
type RouteRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup)
}

// RouterParams specifies the dependencies needed to assemble the Gin HTTP router.
type RouterParams struct {
	fx.In

	Logger          *zap.Logger
	Database        *database.Store  `optional:"true"`
	Native          *engine.Engine   `optional:"true"`
	DatabaseReady   ServiceReadiness `optional:"true" name:"db_ready"`
	NativeReady     ServiceReadiness `optional:"true" name:"native_ready"`
	AuthService     auth.AuthService `optional:"true"`
	Assets          http.FileSystem
	PublicRoutes    []RouteRegistrar `group:"public_routes"`
	ProtectedRoutes []RouteRegistrar `group:"protected_routes"`
}

func newRouter(p RouterParams) *gin.Engine {
	router := gin.New()
	router.Use(httpmiddleware.Recovery(p.Logger), httpmiddleware.RequestID())

	api := router.Group("/api/v1")
	api.Use(httpmiddleware.AccessLog(p.Logger), httpmiddleware.MaxBodyLimit())

	var dbReady ServiceReadiness
	if p.Database != nil {
		dbReady = p.Database
	} else if p.DatabaseReady != nil {
		dbReady = p.DatabaseReady
	}

	var nativeReady ServiceReadiness
	if p.Native != nil {
		nativeReady = p.Native
	} else if p.NativeReady != nil {
		nativeReady = p.NativeReady
	}
	api.GET("/health", healthHandler(dbReady, nativeReady))

	// Mount public routes (e.g. auth login, status, init)
	for _, r := range p.PublicRoutes {
		r.RegisterRoutes(api)
	}

	// Mount protected routes (guarded by session authentication)
	protected := api.Group("")
	if p.AuthService != nil {
		protected.Use(auth.RequireAuth(p.AuthService))
	}
	for _, r := range p.ProtectedRoutes {
		r.RegisterRoutes(protected)
	}

	swaggerHandler := ginSwagger.WrapHandler(swaggerFiles.Handler)
	router.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/swagger/index.html")
	})
	router.GET("/swagger/*any", swaggerHandler)

	staticHandler := http.FileServer(p.Assets)
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
func healthHandler(database ServiceReadiness, native ServiceReadiness) gin.HandlerFunc {
	return func(c *gin.Context) {
		dbReady := database != nil && database.Ready()
		nativeReady := native != nil && native.Ready()
		if !dbReady || !nativeReady {
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

func newHTTPServer(config Config, router *gin.Engine) (*http.Server, error) {
	if err := validateHTTPAddress(config.HTTPAddress); err != nil {
		return nil, fmt.Errorf("validate HTTP address: %w", err)
	}

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
