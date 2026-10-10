package network

import (
	"net"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for system network configuration.
type Handler struct {
	service *NetworkService
	logger  *zap.Logger
}

// NewHandler creates a new network Handler.
func NewHandler(service *NetworkService, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterProtectedRoutes mounts endpoints requiring authentication.
func (h *Handler) RegisterProtectedRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/network")
	{
		group.GET("/interfaces", h.ListInterfaces)
		group.POST("/interfaces/:name/apply", h.ApplyConfig)
		group.GET("/status", h.GetStatus)
		group.POST("/ping", h.Ping)
	}
}

// RegisterRoutes implements the app.RouteRegistrar interface for protected routes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	h.RegisterProtectedRoutes(rg)
}

type publicNetworkRegistrar struct {
	handler *Handler
}

func (r *publicNetworkRegistrar) RegisterRoutes(rg *gin.RouterGroup) {
	r.handler.RegisterPublicRoutes(rg)
}

// PublicRouteRegistrar returns a RouteRegistrar for public network endpoints.
func (h *Handler) PublicRouteRegistrar() interface{ RegisterRoutes(*gin.RouterGroup) } {
	return &publicNetworkRegistrar{handler: h}
}

// RegisterPublicRoutes mounts endpoints that can be accessed with token or authenticated session.
func (h *Handler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/network")
	{
		// Allow unified confirmation and rollback via either token or session
		group.POST("/confirm", h.Confirm)
		group.POST("/rollback", h.Rollback)
	}
}

// ListInterfaces godoc
// @Summary      List physical network interfaces
// @Description  Queries physical network interfaces, marking the current session interface only when it can be identified unambiguously.
// @Tags         network
// @Produce      json
// @Success      200  {object}  httputil.Response{data=[]InterfaceInfo}
// @Failure      500  {object}  httputil.Response
// @Router       /system/network/interfaces [get]
func (h *Handler) ListInterfaces(c *gin.Context) {
	localAddr := ""
	if la, ok := c.Request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		localAddr = la.String()
	}
	if localAddr == "" && c.Request.Host != "" {
		localAddr = c.Request.Host
	}

	clientIP := c.ClientIP()
	ifaces, err := h.service.ListInterfaces(c.Request.Context(), localAddr, c.Request.Host, clientIP)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, ifaces)
}

// ApplyConfig godoc
// @Summary      Apply interface configuration
// @Description  Arms a two-phase watchdog transaction, returns a confirmation token and starts delayed reconfiguration
// @Tags         network
// @Accept       json
// @Produce      json
// @Param        name  path      string           true  "Interface identifier, e.g. eth0"
// @Param        body  body      InterfaceConfig  true  "Configuration payload"
// @Success      200   {object}  httputil.Response{data=ApplyResponse}
// @Failure      400   {object}  httputil.Response
// @Failure      409   {object}  httputil.Response
// @Failure      422   {object}  httputil.Response
// @Failure      500   {object}  httputil.Response
// @Router       /system/network/interfaces/{name}/apply [post]
func (h *Handler) ApplyConfig(c *gin.Context) {
	iface := c.Param("name")
	var req InterfaceConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	currentHost := c.Request.Host
	clientIP := c.ClientIP()

	resp, err := h.service.ApplyConfig(c.Request.Context(), iface, req, currentHost, clientIP)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, resp)
}

func extractToken(c *gin.Context) string {
	token := strings.TrimSpace(c.Query("token"))
	if token != "" {
		return token
	}
	var req ConfirmRequest
	if err := c.ShouldBindJSON(&req); err == nil && req.Token != "" {
		return strings.TrimSpace(req.Token)
	}
	return ""
}

// Confirm godoc
// @Summary      Confirm network trial configuration
// @Description  Solidifies current trial configuration and disarms the watchdog timer. Accepts session authentication or one-time confirmation token.
// @Tags         network
// @Accept       json
// @Produce      json
// @Param        token  query     string          false  "Confirmation token"
// @Param        body   body      ConfirmRequest  false  "Confirmation token payload"
// @Success      200    {object}  httputil.Response
// @Failure      403    {object}  httputil.Response
// @Failure      404    {object}  httputil.Response
// @Router       /system/network/confirm [post]
func (h *Handler) Confirm(c *gin.Context) {
	token := extractToken(c)
	_, hasUser := c.Get("user")
	_, hasSession := c.Get("session")
	if token == "" && !hasUser && !hasSession {
		httputil.WriteError(c, httputil.NewError(http.StatusForbidden, "TOKEN_REQUIRED", "Authentication or valid confirmation token required", nil))
		return
	}

	if err := h.service.Confirm(c.Request.Context(), token); err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, gin.H{"status": "confirmed"})
}

// Rollback godoc
// @Summary      Rollback network trial configuration
// @Description  Immediately cancels trial configuration and restores previous safe state. Accepts session authentication or one-time confirmation token.
// @Tags         network
// @Accept       json
// @Produce      json
// @Param        token  query     string           false  "Confirmation token"
// @Param        body   body      RollbackRequest  false  "Rollback payload"
// @Success      200    {object}  httputil.Response
// @Failure      403    {object}  httputil.Response
// @Failure      404    {object}  httputil.Response
// @Failure      500    {object}  httputil.Response
// @Router       /system/network/rollback [post]
func (h *Handler) Rollback(c *gin.Context) {
	token := extractToken(c)
	_, hasUser := c.Get("user")
	_, hasSession := c.Get("session")
	if token == "" && !hasUser && !hasSession {
		httputil.WriteError(c, httputil.NewError(http.StatusForbidden, "TOKEN_REQUIRED", "Authentication or valid confirmation token required", nil))
		return
	}

	if err := h.service.Rollback(c.Request.Context(), token); err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, gin.H{"status": "rolled_back"})
}

// GetStatus godoc
// @Summary      Get active network transaction status
// @Description  Returns active watchdog transaction state if a network trial is currently running
// @Tags         network
// @Produce      json
// @Success      200  {object}  httputil.Response{data=TransactionState}
// @Router       /system/network/status [get]
func (h *Handler) GetStatus(c *gin.Context) {
	status := h.service.GetTransactionStatus()
	httputil.Success(c, status)
}

// Ping godoc
// @Summary      Probe network target connectivity
// @Description  Performs lightweight ICMP/TCP ping probe against gateway or IP address
// @Tags         network
// @Accept       json
// @Produce      json
// @Param        body  body      PingRequest  true  "Ping request payload"
// @Success      200   {object}  httputil.Response{data=PingResponse}
// @Failure      400   {object}  httputil.Response
// @Failure      422   {object}  httputil.Response
// @Router       /system/network/ping [post]
func (h *Handler) Ping(c *gin.Context) {
	var req PingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	resp, err := h.service.Ping(c.Request.Context(), req.Target)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, resp)
}
