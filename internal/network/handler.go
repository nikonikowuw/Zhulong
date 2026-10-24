package network

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// Handler handles HTTP requests for system network configuration.
type Handler struct {
	service *NetworkService
}

// NewHandler creates a new network Handler.
func NewHandler(service *NetworkService) *Handler {
	return &Handler{service: service}
}

// RegisterProtectedRoutes mounts endpoints requiring authentication.
func (h *Handler) RegisterProtectedRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/network")
	group.GET("/interfaces", h.ListInterfaces)
	group.POST("/interfaces/:name/apply", h.ApplyConfig)
	group.GET("/status", h.GetStatus)
	group.POST("/ping", h.Ping)
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
func (h *Handler) PublicRouteRegistrar() RouteRegistrar {
	return &publicNetworkRegistrar{handler: h}
}

// RegisterPublicRoutes mounts endpoints that can be accessed with token or authenticated session.
func (h *Handler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/network")

	// Confirmation and rollback accept either a one-time token or an authenticated session.
	group.POST("/confirm", h.Confirm)
	group.POST("/rollback", h.Rollback)
}

// ListInterfaces godoc
// @Summary      List physical network interfaces
// @Description  Queries physical interfaces, marking the current one only when identified unambiguously.
// @Tags         network
// @Produce      json
// @Success      200  {object}  httputil.Response{data=[]InterfaceInfo}
// @Failure      500  {object}  httputil.Response
// @Router       /system/network/interfaces [get]
func (h *Handler) ListInterfaces(c *gin.Context) {
	origin := RequestOrigin{
		Host:     c.Request.Host,
		ClientIP: c.ClientIP(),
	}
	if localAddr, ok := c.Request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		origin.LocalAddr = localAddr.String()
	}
	if origin.LocalAddr == "" {
		origin.LocalAddr = origin.Host
	}

	ifaces, err := h.service.ListInterfaces(c.Request.Context(), origin)
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
		httputil.HandleBindError(c, err)
		return
	}

	resp, err := h.service.ApplyConfig(c.Request.Context(), iface, req, c.Request.Host)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, resp)
}

// extractToken reads the confirmation token from the query string or the JSON body.
func extractToken(c *gin.Context) string {
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}

	var req ConfirmRequest
	if err := c.ShouldBindJSON(&req); err == nil {
		return strings.TrimSpace(req.Token)
	}
	return ""
}

// requireTokenOrSession enforces that a token or an authenticated session is present.
func requireTokenOrSession(c *gin.Context, token string) error {
	_, hasUser := c.Get("user")
	_, hasSession := c.Get("session")

	hasToken := token != ""
	if hasToken || hasUser || hasSession {
		return nil
	}

	return httputil.NewError(
		http.StatusForbidden,
		"TOKEN_REQUIRED",
		"Authentication or valid confirmation token required",
		nil,
	)
}

// Confirm godoc
// @Summary      Confirm network trial configuration
// @Description  Solidifies the trial configuration and disarms the watchdog timer. Accepts a session or token.
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
	if err := requireTokenOrSession(c, token); err != nil {
		httputil.WriteError(c, err)
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
// @Description  Cancels the trial configuration and restores the previous state. Accepts a session or token.
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
	if err := requireTokenOrSession(c, token); err != nil {
		httputil.WriteError(c, err)
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
	httputil.Success(c, h.service.GetTransactionStatus())
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
		httputil.HandleBindError(c, err)
		return
	}

	resp, err := h.service.Ping(c.Request.Context(), req.Target)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, resp)
}
