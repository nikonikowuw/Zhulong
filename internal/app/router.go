package app

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// DecodeStatus describes current video decoder metrics and admission state.
type DecodeStatus struct {
	Backend           string `json:"backend"`
	HardwareAvailable bool   `json:"hardware_available"`
	ActiveSessions    int    `json:"active_sessions"`
	MaxSessions       int    `json:"max_sessions"`
	RejectedRequests  int    `json:"rejected_requests"`
}

// DecodeStatsProvider provides decode metrics and hardware readiness.
type DecodeStatsProvider interface {
	GetDecodeStats() (engine.DecodeStats, error)
}

// HealthData describes the components required before the host serves requests.
type HealthData struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
	Decode     *DecodeStatus     `json:"decode,omitempty"`
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

	Logger        *zap.Logger
	Config        Config              `optional:"true"`
	Database      *database.Store     `optional:"true"`
	Native        *engine.Engine      `optional:"true"`
	DecodeStats   DecodeStatsProvider `optional:"true"`
	DatabaseReady ServiceReadiness    `optional:"true" name:"db_ready"`
	NativeReady   ServiceReadiness    `optional:"true" name:"native_ready"`
	AuthService   auth.AuthService    `optional:"true"`
	AuditService  *audit.Service      `optional:"true"`
	Assets        http.FileSystem
	// LogSink lets /health name a degraded log destination. Readiness itself stays
	// database + engine: a host whose log file is unwritable still records video.
	LogSink         loggerOutput      `optional:"true"`
	PublicRoutes    []RouteRegistrar  `group:"public_routes"`
	ProtectedRoutes []RouteRegistrar  `group:"protected_routes"`
	UploadRoutes    []RouteRegistrar  `group:"upload_routes"`
	APITokenService *apitoken.Service `optional:"true"`
	OpenAPIRoutes   []RouteRegistrar  `group:"openapi_routes"`
}

func newRouter(p RouterParams) *gin.Engine {
	router := gin.New()

	// This deployment has no reverse proxy in front of the edge device, so the
	// client IP must come from the TCP connection source. Gin's default trusts
	// forwarding headers from 0.0.0.0/0 and ::/0, which would let any caller
	// forge the address that audit records attribute an operation to.
	if err := router.SetTrustedProxies(nil); err != nil {
		p.Logger.Warn("failed to disable trusted proxies", zap.Error(err))
	}

	// Recovery stays outermost per the middleware guidelines. The audit
	// observer is notified after the sanitized 500 response is written, which
	// is the only point where a panicking request can be recorded as failed.
	router.Use(httpmiddleware.Recovery(p.Logger, audithttp.NewPanicObserver(p.AuditService)), httpmiddleware.RequestID())

	api := router.Group("/api/v1")
	api.Use(httpmiddleware.AccessLog(p.Logger), httpmiddleware.MaxBodyLimit())
	// Mounted after the body limit so a request rejected before the handler runs
	// is not audited, and before authentication so unauthenticated init/login
	// attempts are still covered. Requests that declare no action are skipped.
	if p.AuditService != nil {
		api.Use(audithttp.Middleware(p.AuditService))
	}

	var dbReady ServiceReadiness = p.DatabaseReady
	if p.Database != nil {
		dbReady = p.Database
	}

	var nativeReady ServiceReadiness = p.NativeReady
	if p.Native != nil {
		nativeReady = p.Native
	}

	var decodeStatsProvider DecodeStatsProvider = p.DecodeStats
	if p.Native != nil {
		decodeStatsProvider = p.Native
	}

	api.GET("/health", healthHandler(healthParams{
		Database:    dbReady,
		Native:      nativeReady,
		DecodeStats: decodeStatsProvider,
		LogSink:     p.LogSink,
		Config:      p.Config,
	}))

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

	// Mount upload routes with the archive size plus bounded multipart framing overhead.
	//
	// This is a second, parallel /api/v1 pipeline rather than a child of `api`: the
	// protected group inherits the 1 MiB MaxBodyLimit, and Gin evaluates group
	// middleware at Group() time, so the limit cannot be relaxed afterwards
	// (middleware-guidelines.md §1). The cost of the split is that AccessLog,
	// audit and RequireAuth appear twice — any middleware meant to cover every
	// /api/v1 endpoint has to be added to both groups.
	upload := router.Group("/api/v1")
	upload.Use(httpmiddleware.AccessLog(p.Logger),
		httpmiddleware.MaxBodyLimitWithLimitAndCode(algorithm.MaxUploadRequestBodySize, "FILE_TOO_LARGE"))
	// The upload group is mounted separately, so it needs its own audit
	// middleware to cover algorithm installation.
	if p.AuditService != nil {
		upload.Use(audithttp.Middleware(p.AuditService))
	}
	if p.AuthService != nil {
		upload.Use(auth.RequireAuth(p.AuthService))
	}
	for _, r := range p.UploadRoutes {
		r.RegisterRoutes(upload)
	}

	// Mount OpenAPI routes for M2M provisioning with dedicated Bearer authentication
	// and an 11 MiB ceiling for enrollment photo uploads.
	openapi := router.Group("/api/v1/openapi")
	openapi.Use(
		httpmiddleware.AccessLog(p.Logger),
		httpmiddleware.MaxBodyLimitWithLimit(11*1024*1024),
	)
	if p.AuditService != nil {
		openapi.Use(audithttp.Middleware(p.AuditService))
	}
	if p.APITokenService != nil {
		openapi.Use(apitoken.RequireBearer(p.APITokenService, p.Logger, p.AuditService))
	} else {
		openapi.Use(func(c *gin.Context) {
			httputil.WriteError(c, apperr.New(apperr.KindInternal, "INTERNAL_ERROR", "API token service is not initialized", nil))
			c.Abort()
		})
	}
	for _, r := range p.OpenAPIRoutes {
		r.RegisterRoutes(openapi)
	}

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

type healthParams struct {
	Database    ServiceReadiness
	Native      ServiceReadiness
	DecodeStats DecodeStatsProvider
	LogSink     loggerOutput
	Config      Config
}

// healthHandler godoc
// @Summary      Get host health
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=HealthData}
// @Failure      503  {object}  httputil.Response
// @Router       /health [get]
func healthHandler(p healthParams) gin.HandlerFunc {
	return func(c *gin.Context) {
		dbReady := p.Database != nil && p.Database.Ready()
		nativeReady := p.Native != nil && p.Native.Ready()
		if !dbReady || !nativeReady {
			httputil.WriteError(c, httputil.NewError(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Service is not ready", nil))
			return
		}
		components := map[string]string{
			"database": "ready",
			"engine":   "ready",
		}
		// A degraded logger belongs in the payload but not in the status code: the
		// host's job is recording, and refusing /health 200 over a log file would
		// take a working NVR out of rotation. Health must stay cheap — the sink
		// contract forbids touching the file here.
		if p.LogSink != nil {
			components["logging"] = "ready"
			if sinkErr, dropped := p.LogSink.Health(); sinkErr != nil || dropped > 0 {
				components["logging"] = "degraded"
			}
		}

		var decode *DecodeStatus
		if p.DecodeStats != nil {
			if stats, err := p.DecodeStats.GetDecodeStats(); err == nil {
				backend := p.Config.Decode.Backend
				if backend == "" {
					backend = "auto"
				}
				decode = &DecodeStatus{
					Backend:           backend,
					HardwareAvailable: stats.HardwareAvailable,
					ActiveSessions:    stats.ActiveSessions,
					MaxSessions:       stats.MaxSessions,
					RejectedRequests:  stats.RejectedRequests,
				}
				if (backend == "mpp" || !p.Config.Decode.AllowSoftwareFallback) && !stats.HardwareAvailable {
					components["engine"] = "degraded"
				}
			}
		}

		httputil.Success(c, HealthData{
			Status:     "ready",
			Components: components,
			Decode:     decode,
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
