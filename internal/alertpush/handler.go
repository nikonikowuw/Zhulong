package alertpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

const (
	DefaultTestRateLimitPerMin = 30
	DefaultTestRateLimitBurst  = 10

	maxAuditTargetNameLen = 64
)

var errTestTargetStoreFailure = errors.New("load test destination from store")

// Handler implements HTTP REST API for alert destinations.
type Handler struct {
	service     *Service
	store       *Store
	testLimiter *httpmiddleware.IPRateLimiter
	logger      *zap.Logger
}

// NewHandler constructs a Handler with default connectivity-test rate limiting.
func NewHandler(service *Service, store *Store, logger *zap.Logger) *Handler {
	return NewHandlerWithTestLimiter(service, store, defaultTestRateLimiter(), logger)
}

// NewHandlerWithTestLimiter constructs a Handler with a caller-supplied test rate limiter.
func NewHandlerWithTestLimiter(
	service *Service,
	store *Store,
	testLimiter *httpmiddleware.IPRateLimiter,
	logger *zap.Logger,
) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service:     service,
		store:       store,
		testLimiter: testLimiter,
		logger:      logger.Named("alertpush.handler"),
	}
}

func defaultTestRateLimiter() *httpmiddleware.IPRateLimiter {
	return httpmiddleware.NewIPRateLimiter(httpmiddleware.RateLimitConfig{
		PerMinute:                DefaultTestRateLimitPerMin,
		Burst:                    DefaultTestRateLimitBurst,
		ProcessRequestsPerMinute: DefaultTestRateLimitPerMin * 4,
		ProcessMaxInFlight:       16,
		PerIPCode:                CodeAlertTestRateLimited,
		BudgetExhaustedCode:      CodeAlertTestRateLimited,
		ConcurrencyLimitCode:     CodeAlertTestRateLimited,
		LogScope:                 "alert push test",
	})
}

func (h *Handler) allowLoopback() bool {
	return h.service != nil && h.service.config.AllowLoopback
}

// RegisterRoutes registers the protected CRUD routes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/alert-destinations")
	{
		group.GET("", h.List)
		group.POST("", h.Create)
		group.PUT("/:id", h.Update)
		group.DELETE("/:id", h.Delete)
		group.POST("/:id/enable", h.Enable)
		group.POST("/:id/disable", h.Disable)
		group.POST("/test", httpmiddleware.RateLimit(h.testLimiter, h.logger), h.Test)
	}
}

// List godoc
// @Summary      List alert destinations
// @Description  Returns list of configured alert push destinations with real-time operational status.
// @Tags         alertpush
// @Produce      json
// @Success      200  {object}  httputil.Response{data=AlertDestinationListResponse}
// @Failure      401  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /alert-destinations [get]
func (h *Handler) List(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	items, err := h.store.List(c.Request.Context())
	if err != nil {
		httputil.WriteError(
			c,
			apperr.Internal("INTERNAL_ERROR", "Failed to list alert destinations", err),
		)
		return
	}

	responses := make([]AlertDestinationResponse, len(items))
	for i, dest := range items {
		status := h.service.dispatcher.GetStatus(dest.ID, dest.Enabled)
		resp, err := h.toResponse(dest, status)
		if err != nil {
			httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to project destination", err))
			return
		}
		responses[i] = resp
	}

	httputil.Success(c, AlertDestinationListResponse{Items: responses})
}

// Create godoc
// @Summary      Create alert destination
// @Description  Creates a new webhook or mqtt alert destination.
// @Tags         alertpush
// @Accept       json
// @Produce      json
// @Param        payload  body      CreateAlertDestinationRequest  true  "Destination configuration"
// @Success      200      {object}  httputil.Response{data=AlertDestinationResponse}
// @Failure      400      {object}  httputil.Response
// @Failure      401      {object}  httputil.Response
// @Failure      409      {object}  httputil.Response
// @Failure      422      {object}  httputil.Response
// @Failure      500      {object}  httputil.Response
// @Router       /alert-destinations [post]
func (h *Handler) Create(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	audithttp.Tag(
		c,
		ActionAlertDestinationCreate,
		"destination:new",
		"Create alert destination",
	)

	var req CreateAlertDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	if safeName := sanitizeAuditName(req.Name); safeName != "" {
		audithttp.TagTarget(c, "destination:"+safeName)
		audithttp.TagDetail(c, "Create alert destination "+safeName)
	}

	ctx := c.Request.Context()
	dest, secretRaw, err := h.parseCreateRequest(req)
	if err != nil {
		h.writeValidationError(c, err)
		return
	}

	if err := h.service.Create(ctx, dest, secretRaw); err != nil {
		if errors.Is(err, ErrDestinationNameConflict) {
			httputil.WriteError(
				c,
				apperr.Conflict(CodeAlertDestinationNameConflict, "Destination name already exists", err),
			)
			return
		}
		if errors.Is(err, ErrClientIDConflict) {
			httputil.WriteError(
				c,
				apperr.Conflict(CodeAlertDestinationClientIDConflict, "MQTT ClientID already exists", err),
			)
			return
		}
		if errors.Is(err, ErrMaxDestinationsReached) {
			httputil.WriteError(
				c,
				apperr.Invalid(
					CodeAlertMaxDestinationsReached,
					fmt.Sprintf("Maximum destination limit (%d) reached", MaxDestinations),
					err,
				),
			)
			return
		}
		h.writeValidationError(c, err)
		return
	}

	audithttp.TagTarget(c, "destination:"+dest.ID)

	status := h.service.dispatcher.GetStatus(dest.ID, dest.Enabled)
	resp, err := h.toResponse(dest, status)
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to project destination", err))
		return
	}
	httputil.Success(c, resp)
}

// Update godoc
// @Summary      Update alert destination
// @Description  Updates an existing destination configuration.
// @Tags         alertpush
// @Accept       json
// @Produce      json
// @Param        id       path      string                         true  "Destination ID"
// @Param        payload  body      UpdateAlertDestinationRequest  true  "Update payload"
// @Success      200      {object}  httputil.Response{data=AlertDestinationResponse}
// @Failure      400      {object}  httputil.Response
// @Failure      401      {object}  httputil.Response
// @Failure      404      {object}  httputil.Response
// @Failure      409      {object}  httputil.Response
// @Failure      422      {object}  httputil.Response
// @Failure      500      {object}  httputil.Response
// @Router       /alert-destinations/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id := c.Param("id")
	audithttp.Tag(
		c,
		ActionAlertDestinationUpdate,
		"destination:"+id,
		"Update alert destination "+id,
	)

	var req UpdateAlertDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	ctx := c.Request.Context()
	existing, err := h.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(
				c,
				apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err),
			)
			return
		}
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to get destination", err))
		return
	}

	params, err := h.parseUpdateRequest(existing, req)
	if err != nil {
		h.writeValidationError(c, err)
		return
	}

	if err := h.service.Update(ctx, existing, params); err != nil {
		if errors.Is(err, ErrDestinationNameConflict) {
			httputil.WriteError(
				c,
				apperr.Conflict(CodeAlertDestinationNameConflict, "Destination name already exists", err),
			)
			return
		}
		if errors.Is(err, ErrClientIDConflict) {
			httputil.WriteError(
				c,
				apperr.Conflict(CodeAlertDestinationClientIDConflict, "MQTT ClientID already exists", err),
			)
			return
		}
		h.writeValidationError(c, err)
		return
	}

	status := h.service.dispatcher.GetStatus(existing.ID, existing.Enabled)
	resp, err := h.toResponse(existing, status)
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to project destination", err))
		return
	}
	httputil.Success(c, resp)
}

// Delete godoc
// @Summary      Delete alert destination
// @Description  Removes an alert destination by ID.
// @Tags         alertpush
// @Produce      json
// @Param        id   path      string  true  "Destination ID"
// @Success      200  {object}  httputil.Response
// @Failure      401  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /alert-destinations/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(
		c,
		ActionAlertDestinationDelete,
		"destination:"+id,
		"Delete alert destination "+id,
	)

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(
				c,
				apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err),
			)
			return
		}
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to delete destination", err))
		return
	}
	httputil.Success(c, nil)
}

// Enable godoc
// @Summary      Enable alert destination
// @Description  Enables an alert destination.
// @Tags         alertpush
// @Produce      json
// @Param        id   path      string  true  "Destination ID"
// @Success      200  {object}  httputil.Response{data=AlertDestinationResponse}
// @Failure      401  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /alert-destinations/{id}/enable [post]
func (h *Handler) Enable(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id := c.Param("id")
	audithttp.Tag(
		c,
		ActionAlertDestinationEnable,
		"destination:"+id,
		"Enable alert destination "+id,
	)

	h.setDestinationEnabled(c, id, true)
}

// Disable godoc
// @Summary      Disable alert destination
// @Description  Disables an alert destination.
// @Tags         alertpush
// @Produce      json
// @Param        id   path      string  true  "Destination ID"
// @Success      200  {object}  httputil.Response{data=AlertDestinationResponse}
// @Failure      401  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /alert-destinations/{id}/disable [post]
func (h *Handler) Disable(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id := c.Param("id")
	audithttp.Tag(
		c,
		ActionAlertDestinationDisable,
		"destination:"+id,
		"Disable alert destination "+id,
	)

	h.setDestinationEnabled(c, id, false)
}

func (h *Handler) setDestinationEnabled(c *gin.Context, id string, enabled bool) {
	ctx := c.Request.Context()
	if err := h.service.SetEnabled(ctx, id, enabled); err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(
				c,
				apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err),
			)
			return
		}
		httputil.WriteError(
			c,
			apperr.Internal("INTERNAL_ERROR", "Failed to update destination enabled state", err),
		)
		return
	}

	dest, err := h.store.GetByID(ctx, id)
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to load destination", err))
		return
	}
	status := h.service.dispatcher.GetStatus(id, enabled)
	resp, err := h.toResponse(dest, status)
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to project destination", err))
		return
	}
	httputil.Success(c, resp)
}

// Test godoc
// @Summary      Test alert destination connectivity
// @Description  Validates connectivity for draft or saved destinations.
// @Tags         alertpush
// @Accept       json
// @Produce      json
// @Param        payload  body      TestAlertDestinationRequest  true  "Test parameters"
// @Success      200      {object}  httputil.Response{data=TestAlertDestinationResponse}
// @Failure      400      {object}  httputil.Response
// @Failure      401      {object}  httputil.Response
// @Failure      404      {object}  httputil.Response
// @Failure      422      {object}  httputil.Response
// @Failure      429      {object}  httputil.Response
// @Failure      500      {object}  httputil.Response
// @Router       /alert-destinations/test [post]
func (h *Handler) Test(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	audithttp.Tag(
		c,
		ActionAlertDestinationTest,
		"test",
		"Test alert destination connectivity",
	)

	var req TestAlertDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	if req.ID != "" {
		audithttp.TagTarget(c, "destination:"+req.ID)
	} else if ch := strings.TrimSpace(req.ChannelType); ch != "" {
		audithttp.TagTarget(c, "test:"+ch)
	}

	ctx := c.Request.Context()
	dest, secretRaw, isDraft, err := h.resolveTestTarget(ctx, req)
	if err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(
				c,
				apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err),
			)
			return
		}
		if errors.Is(err, errTestTargetStoreFailure) {
			httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to get destination", err))
			return
		}
		h.writeValidationError(c, err)
		return
	}

	res, err := h.service.TestWithOptions(ctx, dest, secretRaw, !isDraft)
	if err != nil {
		h.writeValidationError(c, err)
		return
	}

	if !res.OK {
		reason := res.Reason
		if reason == "" {
			reason = "connectivity test failed"
		}
		audithttp.TagError(c, "ALERT_TEST_FAILED", reason)
	}

	httputil.Success(c, TestAlertDestinationResponse{
		OK:        res.OK,
		LatencyMs: res.LatencyMs,
		Reason:    res.Reason,
	})
}

func (h *Handler) resolveTestTarget(
	ctx context.Context,
	req TestAlertDestinationRequest,
) (*AlertDestination, destinationSecret, bool, error) {
	if req.ID != "" {
		return h.resolveExistingTestTarget(ctx, req)
	}
	dest, sec, err := h.resolveDraftTestTarget(req)
	return dest, sec, true, err
}

func (h *Handler) resolveExistingTestTarget(
	ctx context.Context,
	req TestAlertDestinationRequest,
) (*AlertDestination, destinationSecret, bool, error) {
	existing, err := h.store.GetByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			return nil, nil, false, err
		}
		return nil, nil, false, fmt.Errorf("%w: %w", errTestTargetStoreFailure, err)
	}

	if req.ChannelType != "" && strings.TrimSpace(req.ChannelType) != existing.ChannelType {
		return nil, nil, false, newFieldError(
			"channelType",
			CodeAlertChannelTypeImmutable,
			"channelType does not match existing destination",
			ErrInvalidConfig,
		)
	}

	origEvents := existing.EventsEnabled
	origHeartbeat := existing.HeartbeatEnabled
	origImageMode := existing.ImageMode

	if req.EventsEnabled != nil {
		existing.EventsEnabled = *req.EventsEnabled
	}
	if req.HeartbeatEnabled != nil {
		existing.HeartbeatEnabled = *req.HeartbeatEnabled
	}
	if req.ImageMode != "" {
		existing.ImageMode = strings.TrimSpace(req.ImageMode)
	}

	capabilitiesChanged := existing.EventsEnabled != origEvents ||
		existing.HeartbeatEnabled != origHeartbeat ||
		existing.ImageMode != origImageMode

	allowLoopback := h.allowLoopback()

	switch existing.ChannelType {
	case ChannelTypeWebhook:
		var cfg webhookConfig
		if existing.ConfigJSON != "" {
			if err := json.Unmarshal([]byte(existing.ConfigJSON), &cfg); err != nil {
				return nil, nil, false, err
			}
		}
		origCfg := cfg
		if req.Config.AuthType != nil {
			authType := strings.TrimSpace(*req.Config.AuthType)
			if authType == "" {
				return nil, nil, false, newFieldError("config.authType", "REQUIRED", "authType is required", ErrInvalidConfig)
			}
			if !isValidWebhookAuthType(authType) {
				return nil, nil, false, newFieldError(
					"config.authType",
					"INVALID_VALUE",
					fmt.Sprintf("unsupported webhook authType %q", authType),
					ErrInvalidConfig,
				)
			}
			cfg.AuthType = authType
		}
		if req.Config.TimeoutSeconds != nil {
			if err := validateWebhookTimeoutField(*req.Config.TimeoutSeconds); err != nil {
				return nil, nil, false, err
			}
			cfg.TimeoutSeconds = *req.Config.TimeoutSeconds
		}
		if req.Config.TLSSkipVerify != nil {
			cfg.TLSSkipVerify = *req.Config.TLSSkipVerify
		}
		if existing.HeartbeatEnabled && req.Config.HeartbeatIntervalSeconds != nil {
			if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
				return nil, nil, false, err
			}
			cfg.HeartbeatIntervalSeconds = *req.Config.HeartbeatIntervalSeconds
		}
		cfgBytes, err := json.Marshal(cfg)
		if err != nil {
			return nil, nil, false, err
		}
		existing.ConfigJSON = string(cfgBytes)

		sec, err := h.store.DecryptWebhookSecret(existing.ID, existing.SecretCiphertext)
		if err != nil {
			return nil, nil, false, err
		}
		origSec := cloneWebhookSecret(sec)

		// Security (U2): overriding the saved URL with a different origin must never
		// forward stored credentials, signing secrets, or custom headers to the new host.
		if req.Config.URL != nil {
			newURL := strings.TrimSpace(*req.Config.URL)
			if err := validateWebhookURLField("config.url", newURL, allowLoopback); err != nil {
				return nil, nil, false, err
			}
			if !sameOrigin(origSec.URL, newURL) {
				sec.Token = ""
				sec.Username = ""
				sec.Password = ""
				sec.SigningSecret = ""
				sec.Headers = nil
				sec.HeartbeatURL = ""
			}
			sec.URL = newURL
		}
		if existing.HeartbeatEnabled && req.Config.HeartbeatURL != nil {
			trimmed := strings.TrimSpace(*req.Config.HeartbeatURL)
			if trimmed != "" {
				if err := validateWebhookURLField("config.heartbeatUrl", trimmed, allowLoopback); err != nil {
					return nil, nil, false, err
				}
			}
			sec.HeartbeatURL = trimmed
		}
		if err := applyWebhookSecretOverrides(sec, req.Secret, true); err != nil {
			return nil, nil, false, err
		}
		normalizeWebhookAuthCredentials(cfg.AuthType, sec)
		if err := validateWebhookAuthCredentials(cfg.AuthType, sec); err != nil {
			return nil, nil, false, err
		}
		if existing.HeartbeatEnabled {
			if err := validateWebhookHeartbeatOrigin(cfg.AuthType, sec, allowLoopback); err != nil {
				return nil, nil, false, err
			}
		}
		isDraft := capabilitiesChanged ||
			!webhookConfigsEqual(origCfg, cfg) ||
			!webhookSecretsEqual(origSec, sec)
		return existing, sec, isDraft, nil

	case ChannelTypeMQTT:
		var cfg mqttConfig
		if existing.ConfigJSON != "" {
			if err := json.Unmarshal([]byte(existing.ConfigJSON), &cfg); err != nil {
				return nil, nil, false, err
			}
		}
		origCfg := cfg
		origClientID := effectiveMQTTClientID(existing.ID, existing.MQTTClientID)

		sec, err := h.store.DecryptMQTTSecret(existing.ID, existing.SecretCiphertext)
		if err != nil {
			return nil, nil, false, err
		}
		origSec := *sec

		// Security (U2): overriding the saved broker with a different broker must never
		// forward the stored MQTT password to the new broker.
		if req.Config.Broker != nil {
			newBroker := strings.TrimSpace(*req.Config.Broker)
			if err := validateMQTTBrokerField(newBroker, allowLoopback); err != nil {
				return nil, nil, false, err
			}
			if !sameMQTTBroker(origCfg.Broker, newBroker) {
				sec.Password = ""
			}
			cfg.Broker = newBroker
		}
		if req.Config.Username != nil {
			cfg.Username = strings.TrimSpace(*req.Config.Username)
		}
		if req.Config.Topic != nil {
			newTopic := strings.TrimSpace(*req.Config.Topic)
			if newTopic == "" {
				return nil, nil, false, newFieldError("config.topic", "REQUIRED", "topic is required for mqtt", ErrInvalidConfig)
			}
			if strings.ContainsAny(newTopic, "+#") {
				return nil, nil, false, newFieldError(
					"config.topic",
					CodeAlertMQTTWildcardForbidden,
					"mqtt publish topic must not contain wildcards",
					ErrInvalidConfig,
				)
			}
			cfg.Topic = newTopic
		}
		if existing.HeartbeatEnabled {
			if req.Config.HeartbeatTopic != nil {
				trimmed := strings.TrimSpace(*req.Config.HeartbeatTopic)
				if trimmed != "" && strings.ContainsAny(trimmed, "+#") {
					return nil, nil, false, newFieldError(
						"config.heartbeatTopic",
						CodeAlertMQTTWildcardForbidden,
						"mqtt heartbeat topic must not contain wildcards",
						ErrInvalidConfig,
					)
				}
				cfg.HeartbeatTopic = trimmed
			}
			if req.Config.HeartbeatIntervalSeconds != nil {
				if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
					return nil, nil, false, err
				}
				cfg.HeartbeatIntervalSeconds = *req.Config.HeartbeatIntervalSeconds
			}
			if req.Config.LWTEnabled != nil {
				lwt := *req.Config.LWTEnabled
				cfg.LWTEnabled = &lwt
			}
		}
		if req.Config.QoS != nil {
			if !isValidMQTTQoS(*req.Config.QoS) {
				return nil, nil, false, newFieldError("config.qos", "MAX_VALUE", "mqtt qos must be 0, 1 or 2", ErrInvalidConfig)
			}
			cfg.QoS = *req.Config.QoS
		}
		if req.Config.KeepAliveSeconds != nil {
			if err := validateKeepAliveField(*req.Config.KeepAliveSeconds); err != nil {
				return nil, nil, false, err
			}
			cfg.KeepAliveSeconds = *req.Config.KeepAliveSeconds
		}
		if req.Config.TLSSkipVerify != nil {
			cfg.TLSSkipVerify = *req.Config.TLSSkipVerify
		}
		if req.Config.InlineMaxKB != nil {
			if err := validateInlineMaxKBField(*req.Config.InlineMaxKB); err != nil {
				return nil, nil, false, err
			}
			cfg.InlineMaxKB = *req.Config.InlineMaxKB
		}
		if req.Config.ClientID != nil {
			cID := strings.TrimSpace(*req.Config.ClientID)
			if err := validateMQTTClientID(cID); err != nil {
				return nil, nil, false, err
			}
			if cID != "" {
				existing.MQTTClientID = &cID
			} else {
				existing.MQTTClientID = nil
			}
		}
		cfgBytes, err := json.Marshal(cfg)
		if err != nil {
			return nil, nil, false, err
		}
		existing.ConfigJSON = string(cfgBytes)

		if req.Secret != nil && req.Secret.Password != nil {
			sec.Password = *req.Secret.Password
		}
		isDraft := capabilitiesChanged ||
			effectiveMQTTClientID(existing.ID, existing.MQTTClientID) != origClientID ||
			!mqttConfigsEqual(origCfg, cfg) ||
			*sec != origSec
		return existing, sec, isDraft, nil
	default:
		return nil, nil, false, ErrInvalidChannelType
	}
}

func (h *Handler) resolveDraftTestTarget(
	req TestAlertDestinationRequest,
) (*AlertDestination, destinationSecret, error) {
	eventsEnabled := true
	if req.EventsEnabled != nil {
		eventsEnabled = *req.EventsEnabled
	}
	var heartbeatEnabled bool
	if req.HeartbeatEnabled != nil {
		heartbeatEnabled = *req.HeartbeatEnabled
	}
	imageMode := strings.TrimSpace(req.ImageMode)
	if !eventsEnabled && imageMode == "" {
		imageMode = ImageModeURL
	}

	dest := &AlertDestination{
		ChannelType:      strings.TrimSpace(req.ChannelType),
		EventsEnabled:    eventsEnabled,
		HeartbeatEnabled: heartbeatEnabled,
		ImageMode:        imageMode,
	}

	allowLoopback := h.allowLoopback()

	switch dest.ChannelType {
	case ChannelTypeWebhook:
		rawURL := derefTrimmed(req.Config.URL)
		if err := validateWebhookURLField("config.url", rawURL, allowLoopback); err != nil {
			return nil, nil, err
		}
		timeout := DefaultWebhookTimeoutSec
		if req.Config.TimeoutSeconds != nil {
			if err := validateWebhookTimeoutField(*req.Config.TimeoutSeconds); err != nil {
				return nil, nil, err
			}
			timeout = *req.Config.TimeoutSeconds
		}
		authType := AuthTypeNone
		if req.Config.AuthType != nil {
			at := strings.TrimSpace(*req.Config.AuthType)
			if at != "" {
				if !isValidWebhookAuthType(at) {
					return nil, nil, newFieldError(
						"config.authType",
						"INVALID_VALUE",
						fmt.Sprintf("unsupported webhook authType %q", at),
						ErrInvalidConfig,
					)
				}
				authType = at
			}
		}
		hbInterval := DefaultHeartbeatIntervalSeconds
		if heartbeatEnabled && req.Config.HeartbeatIntervalSeconds != nil {
			if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
				return nil, nil, err
			}
			hbInterval = *req.Config.HeartbeatIntervalSeconds
		}
		var tlsSkipVerify bool
		if req.Config.TLSSkipVerify != nil {
			tlsSkipVerify = *req.Config.TLSSkipVerify
		}
		cfgJSON, err := json.Marshal(webhookConfig{
			AuthType:                 authType,
			TimeoutSeconds:           timeout,
			TLSSkipVerify:            tlsSkipVerify,
			HeartbeatIntervalSeconds: hbInterval,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal webhook config: %w", err)
		}
		dest.ConfigJSON = string(cfgJSON)

		sec := &webhookSecret{
			URL: rawURL,
		}
		if heartbeatEnabled && req.Config.HeartbeatURL != nil {
			sec.HeartbeatURL = strings.TrimSpace(*req.Config.HeartbeatURL)
		}
		if err := applyWebhookSecretOverrides(sec, req.Secret, true); err != nil {
			return nil, nil, err
		}
		normalizeWebhookAuthCredentials(authType, sec)
		if err := validateWebhookAuthCredentials(authType, sec); err != nil {
			return nil, nil, err
		}
		if heartbeatEnabled {
			if err := validateWebhookHeartbeatOrigin(authType, sec, allowLoopback); err != nil {
				return nil, nil, err
			}
		}
		return dest, sec, nil

	case ChannelTypeMQTT:
		broker := derefTrimmed(req.Config.Broker)
		if err := validateMQTTBrokerField(broker, allowLoopback); err != nil {
			return nil, nil, err
		}
		topic := derefTrimmed(req.Config.Topic)
		if topic == "" {
			return nil, nil, newFieldError("config.topic", "REQUIRED", "topic is required for mqtt", ErrInvalidConfig)
		}
		if strings.ContainsAny(topic, "+#") {
			return nil, nil, newFieldError(
				"config.topic",
				CodeAlertMQTTWildcardForbidden,
				"mqtt publish topic must not contain wildcards",
				ErrInvalidConfig,
			)
		}
		var qos uint8
		if req.Config.QoS != nil {
			if !isValidMQTTQoS(*req.Config.QoS) {
				return nil, nil, newFieldError("config.qos", "MAX_VALUE", "mqtt qos must be 0, 1 or 2", ErrInvalidConfig)
			}
			qos = *req.Config.QoS
		}
		keepAlive := 30
		if req.Config.KeepAliveSeconds != nil {
			if err := validateKeepAliveField(*req.Config.KeepAliveSeconds); err != nil {
				return nil, nil, err
			}
			keepAlive = *req.Config.KeepAliveSeconds
		}
		hbInterval := DefaultHeartbeatIntervalSeconds
		var hbTopic string
		if heartbeatEnabled {
			if req.Config.HeartbeatIntervalSeconds != nil {
				if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
					return nil, nil, err
				}
				hbInterval = *req.Config.HeartbeatIntervalSeconds
			}
			if req.Config.HeartbeatTopic != nil {
				hbTopic = strings.TrimSpace(*req.Config.HeartbeatTopic)
				if hbTopic != "" && strings.ContainsAny(hbTopic, "+#") {
					return nil, nil, newFieldError(
						"config.heartbeatTopic",
						CodeAlertMQTTWildcardForbidden,
						"mqtt heartbeat topic must not contain wildcards",
						ErrInvalidConfig,
					)
				}
			}
		}
		var inlineMaxKB int
		if req.Config.InlineMaxKB != nil {
			if err := validateInlineMaxKBField(*req.Config.InlineMaxKB); err != nil {
				return nil, nil, err
			}
			inlineMaxKB = *req.Config.InlineMaxKB
		}
		var username string
		if req.Config.Username != nil {
			username = strings.TrimSpace(*req.Config.Username)
		}
		var tlsSkipVerify bool
		if req.Config.TLSSkipVerify != nil {
			tlsSkipVerify = *req.Config.TLSSkipVerify
		}
		cfgJSON, err := json.Marshal(mqttConfig{
			Broker:                   broker,
			Username:                 username,
			Topic:                    topic,
			QoS:                      qos,
			KeepAliveSeconds:         keepAlive,
			TLSSkipVerify:            tlsSkipVerify,
			HeartbeatIntervalSeconds: hbInterval,
			HeartbeatTopic:           hbTopic,
			InlineMaxKB:              inlineMaxKB,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal mqtt config: %w", err)
		}
		dest.ConfigJSON = string(cfgJSON)
		if req.Config.ClientID != nil {
			cID := strings.TrimSpace(*req.Config.ClientID)
			if err := validateMQTTClientID(cID); err != nil {
				return nil, nil, err
			}
			if cID != "" {
				dest.MQTTClientID = &cID
			}
		}
		sec := &mqttSecret{}
		if req.Secret != nil && req.Secret.Password != nil {
			sec.Password = *req.Secret.Password
		}
		return dest, sec, nil
	default:
		return nil, nil, newFieldError(
			"channelType",
			"INVALID_VALUE",
			fmt.Sprintf("unsupported channel type %q", dest.ChannelType),
			ErrInvalidChannelType,
		)
	}
}

func (h *Handler) parseCreateRequest(
	req CreateAlertDestinationRequest,
) (*AlertDestination, destinationSecret, error) {
	eventsEnabled := true
	if req.EventsEnabled != nil {
		eventsEnabled = *req.EventsEnabled
	}
	var heartbeatEnabled bool
	if req.HeartbeatEnabled != nil {
		heartbeatEnabled = *req.HeartbeatEnabled
	}
	if !eventsEnabled && !heartbeatEnabled {
		return nil, nil, newFieldError(
			"eventsEnabled",
			CodeAlertCapabilityRequired,
			"at least one of eventsEnabled or heartbeatEnabled must be true",
			ErrInvalidConfig,
		)
	}

	imageMode := strings.TrimSpace(req.ImageMode)
	if !eventsEnabled && imageMode == "" {
		imageMode = ImageModeURL
	}

	channelType := strings.TrimSpace(req.ChannelType)
	dest := &AlertDestination{
		Name:             strings.TrimSpace(req.Name),
		ChannelType:      channelType,
		Enabled:          true,
		EventsEnabled:    eventsEnabled,
		HeartbeatEnabled: heartbeatEnabled,
		ImageMode:        imageMode,
	}

	if err := validateDestinationName(dest.Name); err != nil {
		return nil, nil, err
	}

	filterJSON, err := marshalFilterInfo(req.Filter)
	if err != nil {
		return nil, nil, err
	}
	dest.FilterJSON = filterJSON

	switch channelType {
	case ChannelTypeWebhook:
		sec, err := h.parseWebhookCreate(dest, req)
		return dest, sec, err
	case ChannelTypeMQTT:
		sec, err := h.parseMQTTCreate(dest, req)
		return dest, sec, err
	default:
		return nil, nil, newFieldError(
			"channelType",
			"INVALID_VALUE",
			fmt.Sprintf("unsupported channel type %q", channelType),
			ErrInvalidChannelType,
		)
	}
}

func (h *Handler) parseWebhookCreate(
	dest *AlertDestination,
	req CreateAlertDestinationRequest,
) (*webhookSecret, error) {
	allowLoopback := h.allowLoopback()
	rawURL := derefTrimmed(req.Config.URL)
	if err := validateWebhookURLField("config.url", rawURL, allowLoopback); err != nil {
		return nil, err
	}

	timeout := DefaultWebhookTimeoutSec
	if req.Config.TimeoutSeconds != nil {
		if err := validateWebhookTimeoutField(*req.Config.TimeoutSeconds); err != nil {
			return nil, err
		}
		timeout = *req.Config.TimeoutSeconds
	}
	authType := AuthTypeNone
	if req.Config.AuthType != nil {
		at := strings.TrimSpace(*req.Config.AuthType)
		if at != "" {
			if !isValidWebhookAuthType(at) {
				return nil, newFieldError(
					"config.authType",
					"INVALID_VALUE",
					fmt.Sprintf("unsupported webhook authType %q", at),
					ErrInvalidConfig,
				)
			}
			authType = at
		}
	}

	hbInterval := DefaultHeartbeatIntervalSeconds
	if dest.HeartbeatEnabled && req.Config.HeartbeatIntervalSeconds != nil {
		if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
			return nil, err
		}
		hbInterval = *req.Config.HeartbeatIntervalSeconds
	}

	sec := &webhookSecret{
		URL: rawURL,
	}
	if dest.HeartbeatEnabled && req.Config.HeartbeatURL != nil {
		sec.HeartbeatURL = strings.TrimSpace(*req.Config.HeartbeatURL)
	}
	if err := applyWebhookSecretOverrides(sec, req.Secret, true); err != nil {
		return nil, err
	}
	normalizeWebhookAuthCredentials(authType, sec)
	if err := validateWebhookAuthCredentials(authType, sec); err != nil {
		return nil, err
	}
	if dest.HeartbeatEnabled {
		if err := validateWebhookHeartbeatOrigin(authType, sec, allowLoopback); err != nil {
			return nil, err
		}
	}

	var tlsSkipVerify bool
	if req.Config.TLSSkipVerify != nil {
		tlsSkipVerify = *req.Config.TLSSkipVerify
	}

	cfgJSON, err := json.Marshal(webhookConfig{
		AuthType:                 authType,
		TimeoutSeconds:           timeout,
		TLSSkipVerify:            tlsSkipVerify,
		HeartbeatIntervalSeconds: hbInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook config: %w", err)
	}
	dest.ConfigJSON = string(cfgJSON)
	return sec, nil
}

func (h *Handler) parseMQTTCreate(
	dest *AlertDestination,
	req CreateAlertDestinationRequest,
) (*mqttSecret, error) {
	allowLoopback := h.allowLoopback()
	broker := derefTrimmed(req.Config.Broker)
	if err := validateMQTTBrokerField(broker, allowLoopback); err != nil {
		return nil, err
	}
	topic := derefTrimmed(req.Config.Topic)
	if topic == "" {
		return nil, newFieldError("config.topic", "REQUIRED", "topic is required for mqtt", ErrInvalidConfig)
	}
	if strings.ContainsAny(topic, "+#") {
		return nil, newFieldError(
			"config.topic",
			CodeAlertMQTTWildcardForbidden,
			"mqtt publish topic must not contain wildcards",
			ErrInvalidConfig,
		)
	}

	var hbTopic string
	hbInterval := DefaultHeartbeatIntervalSeconds
	lwtEnabled := true
	if dest.HeartbeatEnabled {
		if req.Config.HeartbeatTopic != nil {
			hbTopic = strings.TrimSpace(*req.Config.HeartbeatTopic)
			if hbTopic != "" && strings.ContainsAny(hbTopic, "+#") {
				return nil, newFieldError(
					"config.heartbeatTopic",
					CodeAlertMQTTWildcardForbidden,
					"mqtt heartbeat topic must not contain wildcards",
					ErrInvalidConfig,
				)
			}
		}
		if req.Config.HeartbeatIntervalSeconds != nil {
			if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
				return nil, err
			}
			hbInterval = *req.Config.HeartbeatIntervalSeconds
		}
		if req.Config.LWTEnabled != nil {
			lwtEnabled = *req.Config.LWTEnabled
		}
	}

	if req.Config.ClientID != nil {
		cID := strings.TrimSpace(*req.Config.ClientID)
		if err := validateMQTTClientID(cID); err != nil {
			return nil, err
		}
		if cID != "" {
			dest.MQTTClientID = &cID
		}
	}

	var qos uint8
	if req.Config.QoS != nil {
		if !isValidMQTTQoS(*req.Config.QoS) {
			return nil, newFieldError("config.qos", "MAX_VALUE", "mqtt qos must be 0, 1 or 2", ErrInvalidConfig)
		}
		qos = *req.Config.QoS
	}
	keepAlive := 30
	if req.Config.KeepAliveSeconds != nil {
		if err := validateKeepAliveField(*req.Config.KeepAliveSeconds); err != nil {
			return nil, err
		}
		keepAlive = *req.Config.KeepAliveSeconds
	}
	var inlineMaxKB int
	if req.Config.InlineMaxKB != nil {
		if err := validateInlineMaxKBField(*req.Config.InlineMaxKB); err != nil {
			return nil, err
		}
		inlineMaxKB = *req.Config.InlineMaxKB
	}
	var username string
	if req.Config.Username != nil {
		username = strings.TrimSpace(*req.Config.Username)
	}
	var tlsSkipVerify bool
	if req.Config.TLSSkipVerify != nil {
		tlsSkipVerify = *req.Config.TLSSkipVerify
	}

	cfgJSON, err := json.Marshal(mqttConfig{
		Broker:                   broker,
		Username:                 username,
		Topic:                    topic,
		QoS:                      qos,
		KeepAliveSeconds:         keepAlive,
		TLSSkipVerify:            tlsSkipVerify,
		HeartbeatIntervalSeconds: hbInterval,
		HeartbeatTopic:           hbTopic,
		LWTEnabled:               &lwtEnabled,
		InlineMaxKB:              inlineMaxKB,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mqtt config: %w", err)
	}
	dest.ConfigJSON = string(cfgJSON)

	sec := &mqttSecret{}
	if req.Secret != nil && req.Secret.Password != nil {
		sec.Password = *req.Secret.Password
	}
	return sec, nil
}

func (h *Handler) parseUpdateRequest(
	existing *AlertDestination,
	req UpdateAlertDestinationRequest,
) (UpdateParams, error) {
	if req.ChannelType != nil {
		requested := strings.TrimSpace(*req.ChannelType)
		if requested != existing.ChannelType {
			return UpdateParams{}, newFieldError(
				"channelType",
				CodeAlertChannelTypeImmutable,
				"channelType cannot be modified after creation",
				ErrInvalidConfig,
			)
		}
	}

	if req.ImageMode != nil && strings.TrimSpace(*req.ImageMode) == "" {
		return UpdateParams{}, newFieldError("imageMode", "REQUIRED", "imageMode cannot be empty", ErrInvalidConfig)
	}

	params := UpdateParams{
		Name:             req.Name,
		EventsEnabled:    req.EventsEnabled,
		HeartbeatEnabled: req.HeartbeatEnabled,
		ImageMode:        req.ImageMode,
	}

	if req.Filter != nil {
		fs, err := marshalFilterInfo(req.Filter)
		if err != nil {
			return UpdateParams{}, err
		}
		params.FilterJSON = &fs
	}

	effectiveHeartbeat := existing.HeartbeatEnabled
	if req.HeartbeatEnabled != nil {
		effectiveHeartbeat = *req.HeartbeatEnabled
	}

	switch existing.ChannelType {
	case ChannelTypeWebhook:
		if err := h.applyWebhookUpdate(existing, req, effectiveHeartbeat, &params); err != nil {
			return UpdateParams{}, err
		}
	case ChannelTypeMQTT:
		if err := h.applyMQTTUpdate(existing, req, effectiveHeartbeat, &params); err != nil {
			return UpdateParams{}, err
		}
	default:
		return UpdateParams{}, ErrInvalidChannelType
	}

	return params, nil
}

func (h *Handler) applyWebhookUpdate(
	existing *AlertDestination,
	req UpdateAlertDestinationRequest,
	effectiveHeartbeat bool,
	params *UpdateParams,
) error {
	var curCfg webhookConfig
	if existing.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(existing.ConfigJSON), &curCfg); err != nil {
			return fmt.Errorf("unmarshal existing webhook config: %w", err)
		}
	}
	curSec, err := h.store.DecryptWebhookSecret(existing.ID, existing.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt existing webhook secret: %w", err)
	}
	origSec := cloneWebhookSecret(curSec)
	allowLoopback := h.allowLoopback()

	if req.Config != nil {
		if req.Config.URL != nil {
			newURL := strings.TrimSpace(*req.Config.URL)
			if err := validateWebhookURLField("config.url", newURL, allowLoopback); err != nil {
				return err
			}
			curSec.URL = newURL
		}
		if effectiveHeartbeat && req.Config.HeartbeatURL != nil {
			trimmed := strings.TrimSpace(*req.Config.HeartbeatURL)
			if trimmed != "" {
				if err := validateWebhookURLField("config.heartbeatUrl", trimmed, allowLoopback); err != nil {
					return err
				}
			}
			curSec.HeartbeatURL = trimmed
		}
		if req.Config.AuthType != nil {
			authType := strings.TrimSpace(*req.Config.AuthType)
			if authType == "" {
				return newFieldError("config.authType", "REQUIRED", "authType cannot be empty", ErrInvalidConfig)
			}
			if !isValidWebhookAuthType(authType) {
				return newFieldError(
					"config.authType",
					"INVALID_VALUE",
					fmt.Sprintf("unsupported webhook authType %q", authType),
					ErrInvalidConfig,
				)
			}
			curCfg.AuthType = authType
		}
		if req.Config.TimeoutSeconds != nil {
			if err := validateWebhookTimeoutField(*req.Config.TimeoutSeconds); err != nil {
				return err
			}
			curCfg.TimeoutSeconds = *req.Config.TimeoutSeconds
		}
		if req.Config.TLSSkipVerify != nil {
			curCfg.TLSSkipVerify = *req.Config.TLSSkipVerify
		}
		if effectiveHeartbeat && req.Config.HeartbeatIntervalSeconds != nil {
			if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
				return err
			}
			curCfg.HeartbeatIntervalSeconds = *req.Config.HeartbeatIntervalSeconds
		}
	}

	if err := applyWebhookSecretOverrides(curSec, req.Secret, true); err != nil {
		return err
	}

	// Normalize and validate by auth_type
	normalizeWebhookAuthCredentials(curCfg.AuthType, curSec)
	if err := validateWebhookAuthCredentials(curCfg.AuthType, curSec); err != nil {
		return err
	}
	if effectiveHeartbeat {
		if err := validateWebhookHeartbeatOrigin(curCfg.AuthType, curSec, allowLoopback); err != nil {
			return err
		}
	}

	if req.Config != nil {
		cb, err := json.Marshal(curCfg)
		if err != nil {
			return fmt.Errorf("marshal webhook config: %w", err)
		}
		cs := string(cb)
		params.ConfigJSON = &cs
	}
	if !webhookSecretsEqual(origSec, curSec) || len(existing.SecretCiphertext) == 0 {
		params.SecretRaw = curSec
	}
	return nil
}

func (h *Handler) applyMQTTUpdate(
	existing *AlertDestination,
	req UpdateAlertDestinationRequest,
	effectiveHeartbeat bool,
	params *UpdateParams,
) error {
	var curCfg mqttConfig
	if existing.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(existing.ConfigJSON), &curCfg); err != nil {
			return fmt.Errorf("unmarshal existing mqtt config: %w", err)
		}
	}
	curSec, err := h.store.DecryptMQTTSecret(existing.ID, existing.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt existing mqtt secret: %w", err)
	}
	origSec := *curSec
	allowLoopback := h.allowLoopback()

	if req.Config != nil {
		if req.Config.Broker != nil {
			newBroker := strings.TrimSpace(*req.Config.Broker)
			if err := validateMQTTBrokerField(newBroker, allowLoopback); err != nil {
				return err
			}
			curCfg.Broker = newBroker
		}
		if req.Config.Username != nil {
			curCfg.Username = strings.TrimSpace(*req.Config.Username)
		}
		if req.Config.Topic != nil {
			newTopic := strings.TrimSpace(*req.Config.Topic)
			if newTopic == "" {
				return newFieldError("config.topic", "REQUIRED", "topic is required for mqtt", ErrInvalidConfig)
			}
			if strings.ContainsAny(newTopic, "+#") {
				return newFieldError(
					"config.topic",
					CodeAlertMQTTWildcardForbidden,
					"mqtt topic must not contain wildcards",
					ErrInvalidConfig,
				)
			}
			curCfg.Topic = newTopic
		}
		if effectiveHeartbeat {
			if req.Config.HeartbeatTopic != nil {
				trimmed := strings.TrimSpace(*req.Config.HeartbeatTopic)
				if trimmed != "" && strings.ContainsAny(trimmed, "+#") {
					return newFieldError(
						"config.heartbeatTopic",
						CodeAlertMQTTWildcardForbidden,
						"mqtt heartbeat topic must not contain wildcards",
						ErrInvalidConfig,
					)
				}
				curCfg.HeartbeatTopic = trimmed
			}
			if req.Config.HeartbeatIntervalSeconds != nil {
				if err := validateHeartbeatIntervalField(*req.Config.HeartbeatIntervalSeconds); err != nil {
					return err
				}
				curCfg.HeartbeatIntervalSeconds = *req.Config.HeartbeatIntervalSeconds
			}
			if req.Config.LWTEnabled != nil {
				lwt := *req.Config.LWTEnabled
				curCfg.LWTEnabled = &lwt
			}
		}
		if req.Config.QoS != nil {
			if !isValidMQTTQoS(*req.Config.QoS) {
				return newFieldError("config.qos", "MAX_VALUE", "mqtt qos must be 0, 1 or 2", ErrInvalidConfig)
			}
			curCfg.QoS = *req.Config.QoS
		}
		if req.Config.KeepAliveSeconds != nil {
			if err := validateKeepAliveField(*req.Config.KeepAliveSeconds); err != nil {
				return err
			}
			curCfg.KeepAliveSeconds = *req.Config.KeepAliveSeconds
		}
		if req.Config.TLSSkipVerify != nil {
			curCfg.TLSSkipVerify = *req.Config.TLSSkipVerify
		}
		if req.Config.InlineMaxKB != nil {
			if err := validateInlineMaxKBField(*req.Config.InlineMaxKB); err != nil {
				return err
			}
			curCfg.InlineMaxKB = *req.Config.InlineMaxKB
		}

		if req.Config.ClientID != nil {
			cID := strings.TrimSpace(*req.Config.ClientID)
			if err := validateMQTTClientID(cID); err != nil {
				return err
			}
			params.ClientID = &cID
		}
	}

	if req.Secret != nil && req.Secret.Password != nil {
		curSec.Password = *req.Secret.Password
	}

	if req.Config != nil {
		cb, err := json.Marshal(curCfg)
		if err != nil {
			return fmt.Errorf("marshal mqtt config: %w", err)
		}
		cs := string(cb)
		params.ConfigJSON = &cs
	}
	if *curSec != origSec || len(existing.SecretCiphertext) == 0 {
		params.SecretRaw = curSec
	}
	return nil
}

func (h *Handler) toResponse(
	dest *AlertDestination,
	status AlertDestinationStatus,
) (AlertDestinationResponse, error) {
	resp := AlertDestinationResponse{
		ID:               dest.ID,
		Name:             dest.Name,
		ChannelType:      dest.ChannelType,
		Enabled:          dest.Enabled,
		EventsEnabled:    dest.EventsEnabled,
		HeartbeatEnabled: dest.HeartbeatEnabled,
		ImageMode:        dest.ImageMode,
		Status:           status,
	}

	var filter destinationFilter
	if dest.FilterJSON != "" {
		if err := json.Unmarshal([]byte(dest.FilterJSON), &filter); err != nil {
			return AlertDestinationResponse{}, fmt.Errorf("unmarshal filter json: %w", err)
		}
	}
	filter.normalize()
	resp.Filter = AlertDestinationFilterInfo{
		EventTypes:   nonNilStrings(filter.EventTypes),
		AlgorithmIDs: nonNilStrings(filter.AlgorithmIDs),
		CameraIDs:    nonNilStrings(filter.CameraIDs),
	}

	switch dest.ChannelType {
	case ChannelTypeWebhook:
		var cfg webhookConfig
		if dest.ConfigJSON != "" {
			if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
				return AlertDestinationResponse{}, fmt.Errorf("unmarshal webhook config: %w", err)
			}
		}
		hbInterval := normalizeHeartbeatInterval(cfg.HeartbeatIntervalSeconds)
		tlsSkip := cfg.TLSSkipVerify
		if cfg.AuthType != "" {
			at := cfg.AuthType
			resp.Config.AuthType = &at
		}
		resp.Config.TimeoutSeconds = &cfg.TimeoutSeconds
		resp.Config.TLSSkipVerify = &tlsSkip
		resp.Config.HeartbeatIntervalSeconds = &hbInterval

		sec, err := h.store.DecryptWebhookSecret(dest.ID, dest.SecretCiphertext)
		if err != nil {
			h.logger.Error(
				"failed to decrypt webhook destination secret",
				zap.String("destination_id", dest.ID),
				zap.Error(err),
			)
		} else if sec != nil {
			if sec.URL != "" {
				u := sec.URL
				resp.Config.URL = &u
			}
			if sec.HeartbeatURL != "" {
				hbURL := sec.HeartbeatURL
				resp.Config.HeartbeatURL = &hbURL
			}
			resp.SecretSet = sec.Token != "" || sec.Password != ""
			resp.SigningSecretSet = sec.SigningSecret != ""
			if len(sec.Headers) > 0 {
				names := make([]string, 0, len(sec.Headers))
				for k := range sec.Headers {
					names = append(names, k)
				}
				slices.Sort(names)
				resp.HeaderNames = names
			}
		}
	case ChannelTypeMQTT:
		var cfg mqttConfig
		if dest.ConfigJSON != "" {
			if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
				return AlertDestinationResponse{}, fmt.Errorf("unmarshal mqtt config: %w", err)
			}
		}
		hbInterval := normalizeHeartbeatInterval(cfg.HeartbeatIntervalSeconds)
		lwt := cfg.effectiveLWTEnabled()
		tlsSkip := cfg.TLSSkipVerify
		if cfg.Broker != "" {
			b := cfg.Broker
			resp.Config.Broker = &b
		}
		if cfg.Username != "" {
			username := cfg.Username
			resp.Config.Username = &username
		}
		if cfg.Topic != "" {
			t := cfg.Topic
			resp.Config.Topic = &t
		}
		if cfg.HeartbeatTopic != "" {
			hbTopic := cfg.HeartbeatTopic
			resp.Config.HeartbeatTopic = &hbTopic
		}
		resp.Config.LWTEnabled = &lwt
		resp.Config.QoS = &cfg.QoS
		resp.Config.KeepAliveSeconds = &cfg.KeepAliveSeconds
		resp.Config.TLSSkipVerify = &tlsSkip
		resp.Config.HeartbeatIntervalSeconds = &hbInterval
		inlineMaxKB := normalizeInlineMaxKB(cfg.InlineMaxKB)
		resp.Config.InlineMaxKB = &inlineMaxKB
		if dest.MQTTClientID != nil && *dest.MQTTClientID != "" {
			clientID := *dest.MQTTClientID
			resp.Config.ClientID = &clientID
		}

		sec, err := h.store.DecryptMQTTSecret(dest.ID, dest.SecretCiphertext)
		if err != nil {
			h.logger.Error(
				"failed to decrypt mqtt destination secret",
				zap.String("destination_id", dest.ID),
				zap.Error(err),
			)
		} else if sec != nil {
			resp.SecretSet = sec.Password != ""
		}
	}

	return resp, nil
}

func (h *Handler) writeValidationError(c *gin.Context, err error) {
	if errors.Is(err, ErrPublicBaseURLNotConfigured) {
		httputil.WriteError(c, apperr.Invalid(CodeAlertPublicBaseURLMissing, err.Error(), err))
		return
	}
	if errors.Is(err, ErrInvalidImageMode) {
		httputil.WriteError(c, apperr.Invalid(CodeAlertImageModeUnsupported, err.Error(), err))
		return
	}
	if errors.Is(err, ErrInvalidFilterEventType) {
		httputil.WriteError(c, apperr.Invalid(CodeAlertEventTypeInvalid, err.Error(), err))
		return
	}
	var valErr *ValidationError
	if errors.As(err, &valErr) && len(valErr.Details) > 0 {
		details := make([]httputil.FieldDetail, len(valErr.Details))
		for i, d := range valErr.Details {
			details[i] = httputil.FieldDetail{
				Field:   d.Field,
				Code:    d.Code,
				Message: d.Message,
			}
		}
		httputil.WriteError(c, httputil.NewValidationError("Validation failed", details))
		return
	}
	httputil.WriteError(c, apperr.Invalid(CodeAlertDestinationConfigInvalid, err.Error(), err))
}

func marshalFilterInfo(info *AlertDestinationFilterInfo) (string, error) {
	filter := destinationFilter{
		EventTypes:   []string{},
		AlgorithmIDs: []string{},
		CameraIDs:    []string{},
	}
	if info != nil {
		filter.EventTypes = dedupePreserveEmpty(info.EventTypes)
		filter.AlgorithmIDs = nonNilStrings(dedupeTrimmed(info.AlgorithmIDs))
		filter.CameraIDs = nonNilStrings(dedupeTrimmed(info.CameraIDs))
	}
	filterBytes, err := json.Marshal(filter)
	if err != nil {
		return "", fmt.Errorf("marshal destination filter: %w", err)
	}
	return string(filterBytes), nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func derefTrimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func sanitizeAuditName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, trimmed)
	runes := []rune(clean)
	if len(runes) > maxAuditTargetNameLen {
		return string(runes[:maxAuditTargetNameLen])
	}
	return clean
}

func normalizeWebhookAuthCredentials(authType string, sec *webhookSecret) {
	if sec == nil {
		return
	}
	switch authType {
	case "", AuthTypeNone:
		sec.Token = ""
		sec.Username = ""
		sec.Password = ""
	case AuthTypeBearer:
		sec.Username = ""
		sec.Password = ""
	case AuthTypeBasic:
		sec.Token = ""
	}
}

func applyWebhookSecretOverrides(
	sec *webhookSecret,
	input *AlertDestinationSecretInfo,
	checkHeaders bool,
) error {
	if input == nil {
		return nil
	}
	if input.Token != nil {
		sec.Token = *input.Token
	}
	if input.Username != nil {
		sec.Username = *input.Username
	}
	if input.Password != nil {
		sec.Password = *input.Password
	}
	if input.SigningSecret != nil {
		sec.SigningSecret = *input.SigningSecret
	}
	if input.Headers != nil {
		if checkHeaders {
			if err := validateWebhookHeaders(input.Headers); err != nil {
				return err
			}
		}
		sec.Headers = maps.Clone(input.Headers)
	}
	return nil
}

func validateWebhookAuthCredentials(authType string, sec *webhookSecret) error {
	switch authType {
	case AuthTypeBearer:
		if strings.TrimSpace(sec.Token) == "" {
			return newFieldError("secret.token", "REQUIRED", "token is required for bearer auth", ErrInvalidConfig)
		}
	case AuthTypeBasic:
		details := make([]FieldValidationError, 0, 2)
		if strings.TrimSpace(sec.Username) == "" {
			details = append(details, FieldValidationError{
				Field:   "secret.username",
				Code:    "REQUIRED",
				Message: "username is required for basic auth",
			})
		}
		if strings.TrimSpace(sec.Password) == "" {
			details = append(details, FieldValidationError{
				Field:   "secret.password",
				Code:    "REQUIRED",
				Message: "password is required for basic auth",
			})
		}
		if len(details) > 0 {
			return &ValidationError{Details: details, Cause: ErrInvalidConfig}
		}
	}
	return nil
}

func cloneWebhookSecret(sec *webhookSecret) *webhookSecret {
	if sec == nil {
		return &webhookSecret{}
	}
	return &webhookSecret{
		URL:           sec.URL,
		HeartbeatURL:  sec.HeartbeatURL,
		Token:         sec.Token,
		Username:      sec.Username,
		Password:      sec.Password,
		SigningSecret: sec.SigningSecret,
		Headers:       maps.Clone(sec.Headers),
	}
}

func webhookConfigsEqual(a, b webhookConfig) bool {
	return a.AuthType == b.AuthType &&
		a.TimeoutSeconds == b.TimeoutSeconds &&
		a.TLSSkipVerify == b.TLSSkipVerify &&
		normalizeHeartbeatInterval(a.HeartbeatIntervalSeconds) ==
			normalizeHeartbeatInterval(b.HeartbeatIntervalSeconds)
}

func webhookSecretsEqual(a, b *webhookSecret) bool {
	if a == nil {
		a = &webhookSecret{}
	}
	if b == nil {
		b = &webhookSecret{}
	}
	return a.URL == b.URL &&
		a.HeartbeatURL == b.HeartbeatURL &&
		a.Token == b.Token &&
		a.Username == b.Username &&
		a.Password == b.Password &&
		a.SigningSecret == b.SigningSecret &&
		maps.Equal(a.Headers, b.Headers)
}

func mqttConfigsEqual(a, b mqttConfig) bool {
	return a.Broker == b.Broker &&
		a.Username == b.Username &&
		a.Topic == b.Topic &&
		a.QoS == b.QoS &&
		a.KeepAliveSeconds == b.KeepAliveSeconds &&
		a.TLSSkipVerify == b.TLSSkipVerify &&
		a.HeartbeatTopic == b.HeartbeatTopic &&
		a.effectiveLWTEnabled() == b.effectiveLWTEnabled() &&
		normalizeHeartbeatInterval(a.HeartbeatIntervalSeconds) ==
			normalizeHeartbeatInterval(b.HeartbeatIntervalSeconds) &&
		normalizeInlineMaxKB(a.InlineMaxKB) == normalizeInlineMaxKB(b.InlineMaxKB)
}

func effectiveMQTTClientID(destID string, clientID *string) string {
	if clientID != nil && strings.TrimSpace(*clientID) != "" {
		return strings.TrimSpace(*clientID)
	}
	if len(destID) > 4 {
		return "z" + destID[4:]
	}
	return destID
}
