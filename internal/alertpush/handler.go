package alertpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

const maxDestinations = 32

// Handler implements HTTP REST API for alert destinations.
type Handler struct {
	service *Service
	store   *Store
	logger  *zap.Logger
}

// NewHandler constructs a Handler.
func NewHandler(service *Service, store *Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		store:   store,
		logger:  logger.Named("alertpush.handler"),
	}
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
		group.POST("/test", h.Test)
	}
}

// List godoc
// @Summary      List alert destinations
// @Description  Returns list of configured alert push destinations with real-time operational status.
// @Tags         alertpush
// @Produce      json
// @Success      200  {object}  httputil.Response{data=AlertDestinationListResponse}
// @Failure      500  {object}  httputil.Response
// @Router       /alert-destinations [get]
func (h *Handler) List(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	items, err := h.store.List(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to list alert destinations", err))
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
// @Failure      409      {object}  httputil.Response
// @Failure      422      {object}  httputil.Response
// @Failure      500      {object}  httputil.Response
// @Router       /alert-destinations [post]
func (h *Handler) Create(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req CreateAlertDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, apperr.Invalid(CodeAlertDestinationConfigInvalid, "Invalid request body", err))
		return
	}

	audithttp.Tag(
		c,
		ActionAlertDestinationCreate,
		"destination:"+req.Name,
		"Create alert destination "+req.Name,
	)

	ctx := c.Request.Context()
	existing, err := h.store.List(ctx)
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to check destination limit", err))
		return
	}
	if len(existing) >= maxDestinations {
		httputil.WriteError(
			c,
			apperr.Invalid(CodeAlertDestinationConfigInvalid, "Maximum destination limit (32) reached", nil),
		)
		return
	}

	dest, secretRaw, err := h.parseCreateRequest(req)
	if err != nil {
		h.writeValidationError(c, err)
		return
	}

	if err := h.service.Create(ctx, dest, secretRaw); err != nil {
		if errors.Is(err, ErrDestinationNameConflict) || errors.Is(err, ErrClientIDConflict) {
			httputil.WriteError(c, apperr.Conflict(CodeAlertDestinationNameConflict, "Name or MQTT ClientID conflict", err))
			return
		}
		h.writeValidationError(c, err)
		return
	}

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
// @Failure      404      {object}  httputil.Response
// @Failure      409      {object}  httputil.Response
// @Failure      422      {object}  httputil.Response
// @Failure      500      {object}  httputil.Response
// @Router       /alert-destinations/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id := c.Param("id")
	var req UpdateAlertDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, apperr.Invalid(CodeAlertDestinationConfigInvalid, "Invalid request body", err))
		return
	}

	audithttp.Tag(
		c,
		ActionAlertDestinationUpdate,
		"destination:"+id,
		"Update alert destination "+id,
	)

	ctx := c.Request.Context()
	existing, err := h.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(c, apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err))
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
		if errors.Is(err, ErrDestinationNameConflict) || errors.Is(err, ErrClientIDConflict) {
			httputil.WriteError(c, apperr.Conflict(CodeAlertDestinationNameConflict, "Name or MQTT ClientID conflict", err))
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
			httputil.WriteError(c, apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err))
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
// @Failure      404  {object}  httputil.Response
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
// @Failure      404  {object}  httputil.Response
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
			httputil.WriteError(c, apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err))
			return
		}
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to update destination enabled state", err))
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
// @Failure      422      {object}  httputil.Response
// @Router       /alert-destinations/test [post]
func (h *Handler) Test(c *gin.Context) {
	var req TestAlertDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, apperr.Invalid(CodeAlertDestinationConfigInvalid, "Invalid request body", err))
		return
	}

	audithttp.Tag(
		c,
		ActionAlertDestinationTest,
		"test:"+req.ChannelType,
		"Test alert destination connectivity",
	)

	ctx := c.Request.Context()
	dest, secretRaw, err := h.resolveTestTarget(ctx, req)
	if err != nil {
		h.writeValidationError(c, err)
		return
	}

	res, err := h.service.Test(ctx, dest, secretRaw)
	if err != nil {
		h.writeValidationError(c, err)
		return
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
) (*AlertDestination, destinationSecret, error) {
	if req.ID != "" {
		return h.resolveExistingTestTarget(ctx, req)
	}
	return h.resolveDraftTestTarget(req)
}

func (h *Handler) resolveExistingTestTarget(
	ctx context.Context,
	req TestAlertDestinationRequest,
) (*AlertDestination, destinationSecret, error) {
	existing, err := h.store.GetByID(ctx, req.ID)
	if err != nil {
		return nil, nil, err
	}

	switch existing.ChannelType {
	case ChannelTypeWebhook:
		sec, err := h.store.DecryptWebhookSecret(existing.ID, existing.SecretCiphertext)
		if err != nil {
			return nil, nil, err
		}
		if err := applyWebhookSecretOverrides(sec, req.Secret, false); err != nil {
			return nil, nil, err
		}
		if req.Config.URL != "" {
			sec.URL = req.Config.URL
		}
		return existing, sec, nil
	case ChannelTypeMQTT:
		sec, err := h.store.DecryptMQTTSecret(existing.ID, existing.SecretCiphertext)
		if err != nil {
			return nil, nil, err
		}
		if req.Secret != nil && req.Secret.Password != nil {
			sec.Password = *req.Secret.Password
		}
		return existing, sec, nil
	default:
		return nil, nil, ErrInvalidChannelType
	}
}

func (h *Handler) resolveDraftTestTarget(
	req TestAlertDestinationRequest,
) (*AlertDestination, destinationSecret, error) {
	dest := &AlertDestination{
		ChannelType: req.ChannelType,
		ImageMode:   req.ImageMode,
	}

	switch req.ChannelType {
	case ChannelTypeWebhook:
		timeout := 5
		if req.Config.TimeoutSeconds != nil && *req.Config.TimeoutSeconds > 0 {
			timeout = *req.Config.TimeoutSeconds
		}
		authType := AuthTypeNone
		if req.Config.AuthType != "" {
			authType = req.Config.AuthType
		}
		cfgJSON, err := json.Marshal(webhookConfig{
			AuthType:       authType,
			TimeoutSeconds: timeout,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal webhook config: %w", err)
		}
		dest.ConfigJSON = string(cfgJSON)

		sec := &webhookSecret{
			URL: req.Config.URL,
		}
		if err := applyWebhookSecretOverrides(sec, req.Secret, false); err != nil {
			return nil, nil, err
		}
		return dest, sec, nil
	case ChannelTypeMQTT:
		var qos uint8
		if req.Config.QoS != nil {
			qos = *req.Config.QoS
		}
		keepAlive := 30
		if req.Config.KeepAliveSeconds != nil && *req.Config.KeepAliveSeconds > 0 {
			keepAlive = *req.Config.KeepAliveSeconds
		}
		cfgJSON, err := json.Marshal(mqttConfig{
			Broker:           req.Config.Broker,
			Username:         req.Config.Username,
			Topic:            req.Config.Topic,
			QoS:              qos,
			KeepAliveSeconds: keepAlive,
			TLSSkipVerify:    req.Config.TLSSkipVerify,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal mqtt config: %w", err)
		}
		dest.ConfigJSON = string(cfgJSON)
		if req.Config.ClientID != "" {
			cID := req.Config.ClientID
			dest.MQTTClientID = &cID
		}
		sec := &mqttSecret{}
		if req.Secret != nil && req.Secret.Password != nil {
			sec.Password = *req.Secret.Password
		}
		return dest, sec, nil
	default:
		return nil, nil, ErrInvalidChannelType
	}
}

func (h *Handler) parseCreateRequest(
	req CreateAlertDestinationRequest,
) (*AlertDestination, destinationSecret, error) {
	dest := &AlertDestination{
		Name:        strings.TrimSpace(req.Name),
		ChannelType: req.ChannelType,
		Enabled:     true,
		ImageMode:   req.ImageMode,
	}

	filterJSON, err := marshalFilterInfo(req.Filter)
	if err != nil {
		return nil, nil, err
	}
	dest.FilterJSON = filterJSON

	switch req.ChannelType {
	case ChannelTypeWebhook:
		sec, err := h.parseWebhookCreate(dest, req)
		return dest, sec, err
	case ChannelTypeMQTT:
		sec, err := h.parseMQTTCreate(dest, req)
		return dest, sec, err
	default:
		return nil, nil, ErrInvalidChannelType
	}
}

func (h *Handler) parseWebhookCreate(
	dest *AlertDestination,
	req CreateAlertDestinationRequest,
) (*webhookSecret, error) {
	if err := validateWebhookURL(req.Config.URL); err != nil {
		return nil, err
	}

	timeout := 5
	if isValidTimeoutSeconds(req.Config.TimeoutSeconds) {
		timeout = *req.Config.TimeoutSeconds
	}
	authType := AuthTypeNone
	if req.Config.AuthType != "" {
		authType = req.Config.AuthType
	}

	sec := &webhookSecret{
		URL: req.Config.URL,
	}
	if err := applyWebhookSecretOverrides(sec, req.Secret, true); err != nil {
		return nil, err
	}
	if err := validateWebhookAuthCredentials(authType, sec); err != nil {
		return nil, err
	}

	cfgJSON, err := json.Marshal(webhookConfig{
		AuthType:       authType,
		TimeoutSeconds: timeout,
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
	if strings.TrimSpace(req.Config.Broker) == "" {
		return nil, errors.New("broker url is required for mqtt")
	}
	u, err := url.Parse(req.Config.Broker)
	if err != nil || u.User != nil {
		return nil, errors.New("mqtt broker url must not contain userinfo")
	}
	if strings.TrimSpace(req.Config.Topic) == "" {
		return nil, errors.New("topic is required for mqtt")
	}
	if strings.ContainsAny(req.Config.Topic, "+#") {
		return nil, errors.New("mqtt publish topic must not contain wildcards")
	}

	if req.Config.ClientID != "" {
		if len(req.Config.ClientID) > 23 {
			return nil, errors.New("mqtt client id must not exceed 23 characters")
		}
		cID := req.Config.ClientID
		dest.MQTTClientID = &cID
	}

	var qos uint8
	if req.Config.QoS != nil {
		qos = *req.Config.QoS
	}
	keepAlive := 30
	if req.Config.KeepAliveSeconds != nil && *req.Config.KeepAliveSeconds > 0 {
		keepAlive = *req.Config.KeepAliveSeconds
	}

	cfgJSON, err := json.Marshal(mqttConfig{
		Broker:           req.Config.Broker,
		Username:         req.Config.Username,
		Topic:            req.Config.Topic,
		QoS:              qos,
		KeepAliveSeconds: keepAlive,
		TLSSkipVerify:    req.Config.TLSSkipVerify,
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
	params := UpdateParams{
		Name:      req.Name,
		ImageMode: req.ImageMode,
	}

	if req.Filter != nil {
		fs, err := marshalFilterInfo(req.Filter)
		if err != nil {
			return UpdateParams{}, err
		}
		params.FilterJSON = &fs
	}

	switch existing.ChannelType {
	case ChannelTypeWebhook:
		if err := h.applyWebhookUpdate(existing, req, &params); err != nil {
			return UpdateParams{}, err
		}
	case ChannelTypeMQTT:
		if err := h.applyMQTTUpdate(existing, req, &params); err != nil {
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

	if req.Config != nil {
		if req.Config.URL != "" {
			if err := validateWebhookURL(req.Config.URL); err != nil {
				return err
			}
			curSec.URL = req.Config.URL
		}
		if req.Config.AuthType != "" {
			curCfg.AuthType = req.Config.AuthType
		}
		if isValidTimeoutSeconds(req.Config.TimeoutSeconds) {
			curCfg.TimeoutSeconds = *req.Config.TimeoutSeconds
		}
	}

	if err := applyWebhookSecretOverrides(curSec, req.Secret, true); err != nil {
		return err
	}

	// Normalize and validate by auth_type
	switch curCfg.AuthType {
	case AuthTypeNone:
		curSec.Token = ""
		curSec.Username = ""
		curSec.Password = ""
	case AuthTypeBearer:
		curSec.Username = ""
		curSec.Password = ""
	case AuthTypeBasic:
		curSec.Token = ""
	}
	if err := validateWebhookAuthCredentials(curCfg.AuthType, curSec); err != nil {
		return err
	}

	cb, err := json.Marshal(curCfg)
	if err != nil {
		return fmt.Errorf("marshal webhook config: %w", err)
	}
	cs := string(cb)
	params.ConfigJSON = &cs
	params.SecretRaw = curSec
	return nil
}

func (h *Handler) applyMQTTUpdate(
	existing *AlertDestination,
	req UpdateAlertDestinationRequest,
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

	if req.Config != nil {
		if req.Config.Broker != "" {
			curCfg.Broker = req.Config.Broker
		}
		if req.Config.Username != "" {
			curCfg.Username = req.Config.Username
		}
		if req.Config.Topic != "" {
			if strings.ContainsAny(req.Config.Topic, "+#") {
				return errors.New("mqtt topic must not contain wildcards")
			}
			curCfg.Topic = req.Config.Topic
		}
		if req.Config.QoS != nil {
			curCfg.QoS = *req.Config.QoS
		}
		if req.Config.KeepAliveSeconds != nil {
			curCfg.KeepAliveSeconds = *req.Config.KeepAliveSeconds
		}
		curCfg.TLSSkipVerify = req.Config.TLSSkipVerify

		if req.Config.ClientID != "" {
			cID := req.Config.ClientID
			params.ClientID = &cID
		}
	}

	if req.Secret != nil && req.Secret.Password != nil {
		curSec.Password = *req.Secret.Password
	}

	cb, err := json.Marshal(curCfg)
	if err != nil {
		return fmt.Errorf("marshal mqtt config: %w", err)
	}
	cs := string(cb)
	params.ConfigJSON = &cs
	params.SecretRaw = curSec
	return nil
}

func (h *Handler) toResponse(dest *AlertDestination, status AlertDestinationStatus) (AlertDestinationResponse, error) {
	resp := AlertDestinationResponse{
		ID:          dest.ID,
		Name:        dest.Name,
		ChannelType: dest.ChannelType,
		Enabled:     dest.Enabled,
		ImageMode:   dest.ImageMode,
		Status:      status,
	}

	var filter destinationFilter
	if dest.FilterJSON != "" {
		if err := json.Unmarshal([]byte(dest.FilterJSON), &filter); err != nil {
			return AlertDestinationResponse{}, fmt.Errorf("unmarshal filter json: %w", err)
		}
	}
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
		resp.Config.AuthType = cfg.AuthType
		resp.Config.TimeoutSeconds = &cfg.TimeoutSeconds

		sec, err := h.store.DecryptWebhookSecret(dest.ID, dest.SecretCiphertext)
		if err == nil && sec != nil {
			resp.Config.URL = sec.URL
			resp.SecretSet = sec.Token != "" || sec.Password != ""
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
		resp.Config.Broker = cfg.Broker
		resp.Config.Username = cfg.Username
		resp.Config.Topic = cfg.Topic
		resp.Config.QoS = &cfg.QoS
		resp.Config.KeepAliveSeconds = &cfg.KeepAliveSeconds
		resp.Config.TLSSkipVerify = cfg.TLSSkipVerify
		if dest.MQTTClientID != nil {
			resp.Config.ClientID = *dest.MQTTClientID
		}

		sec, err := h.store.DecryptMQTTSecret(dest.ID, dest.SecretCiphertext)
		if err == nil && sec != nil {
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
	httputil.WriteError(c, apperr.Invalid(CodeAlertDestinationConfigInvalid, err.Error(), err))
}

func marshalFilterInfo(info *AlertDestinationFilterInfo) (string, error) {
	filter := destinationFilter{
		EventTypes:   []string{},
		AlgorithmIDs: []string{},
		CameraIDs:    []string{},
	}
	if info != nil {
		filter.EventTypes = nonNilStrings(info.EventTypes)
		filter.AlgorithmIDs = nonNilStrings(info.AlgorithmIDs)
		filter.CameraIDs = nonNilStrings(info.CameraIDs)
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

func validateWebhookURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("url is required for webhook")
	}
	u, err := url.Parse(rawURL)
	isValidScheme := err == nil && (u.Scheme == "http" || u.Scheme == "https")
	if !isValidScheme || u.User != nil {
		return errors.New("webhook url must be http or https without userinfo")
	}
	return nil
}

func isValidTimeoutSeconds(v *int) bool {
	return v != nil && *v >= 1 && *v <= 30
}

func applyWebhookSecretOverrides(
	sec *webhookSecret,
	input *AlertDestinationSecretInfo,
	checkReserved bool,
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
	if input.Headers != nil {
		if checkReserved {
			for hk := range input.Headers {
				if isReservedHeader(hk) {
					return ErrReservedHeaderUsed
				}
			}
		}
		sec.Headers = input.Headers
	}
	return nil
}

func validateWebhookAuthCredentials(authType string, sec *webhookSecret) error {
	switch authType {
	case AuthTypeBearer:
		if strings.TrimSpace(sec.Token) == "" {
			return errors.New("token is required for bearer auth")
		}
	case AuthTypeBasic:
		missingBasicCreds := strings.TrimSpace(sec.Username) == "" || strings.TrimSpace(sec.Password) == ""
		if missingBasicCreds {
			return errors.New("username and password are required for basic auth")
		}
	}
	return nil
}
