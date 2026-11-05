package alertpush

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

const MaxDestinations = 32

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

	audithttp.Tag(c, ActionAlertDestinationCreate, "destination:"+req.Name, "Create alert destination "+req.Name)

	ctx := c.Request.Context()
	existing, err := h.store.List(ctx)
	if err != nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to check destination limit", err))
		return
	}
	if len(existing) >= MaxDestinations {
		httputil.WriteError(c, apperr.Invalid(CodeAlertDestinationConfigInvalid, "Maximum destination limit (32) reached", nil))
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
	resp, _ := h.toResponse(dest, status)
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

	audithttp.Tag(c, ActionAlertDestinationUpdate, "destination:"+id, "Update alert destination "+id)

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

	configJSON, filterJSON, secretRaw, clientID, err := h.parseUpdateRequest(existing, req)
	if err != nil {
		h.writeValidationError(c, err)
		return
	}

	if err := h.service.Update(ctx, existing, req.Name, req.ImageMode, configJSON, filterJSON, secretRaw, clientID); err != nil {
		if errors.Is(err, ErrDestinationNameConflict) || errors.Is(err, ErrClientIDConflict) {
			httputil.WriteError(c, apperr.Conflict(CodeAlertDestinationNameConflict, "Name or MQTT ClientID conflict", err))
			return
		}
		h.writeValidationError(c, err)
		return
	}

	status := h.service.dispatcher.GetStatus(existing.ID, existing.Enabled)
	resp, _ := h.toResponse(existing, status)
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
	audithttp.Tag(c, ActionAlertDestinationDelete, "destination:"+id, "Delete alert destination "+id)

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
	audithttp.Tag(c, ActionAlertDestinationEnable, "destination:"+id, "Enable alert destination "+id)

	ctx := c.Request.Context()
	if err := h.service.SetEnabled(ctx, id, true); err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(c, apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err))
			return
		}
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to enable destination", err))
		return
	}

	dest, _ := h.store.GetByID(ctx, id)
	status := h.service.dispatcher.GetStatus(id, true)
	resp, _ := h.toResponse(dest, status)
	httputil.Success(c, resp)
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
	audithttp.Tag(c, ActionAlertDestinationDisable, "destination:"+id, "Disable alert destination "+id)

	ctx := c.Request.Context()
	if err := h.service.SetEnabled(ctx, id, false); err != nil {
		if errors.Is(err, ErrDestinationNotFound) {
			httputil.WriteError(c, apperr.NotFound(CodeAlertDestinationNotFound, "Alert destination not found", err))
			return
		}
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Failed to disable destination", err))
		return
	}

	dest, _ := h.store.GetByID(ctx, id)
	status := h.service.dispatcher.GetStatus(id, false)
	resp, _ := h.toResponse(dest, status)
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

	audithttp.Tag(c, ActionAlertDestinationTest, "test:"+req.ChannelType, "Test alert destination connectivity")

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

func (h *Handler) resolveTestTarget(ctx context.Context, req TestAlertDestinationRequest) (*AlertDestination, any, error) {
	if req.ID != "" {
		existing, err := h.store.GetByID(ctx, req.ID)
		if err != nil {
			return nil, nil, err
		}
		if req.ChannelType == "" {
			req.ChannelType = existing.ChannelType
		}
		if req.ImageMode == "" {
			req.ImageMode = existing.ImageMode
		}

		if existing.ChannelType == ChannelTypeWebhook {
			sec, _ := h.store.DecryptWebhookSecret(existing.ID, existing.SecretCiphertext)
			if req.Secret != nil {
				if req.Secret.Token != nil {
					sec.Token = *req.Secret.Token
				}
				if req.Secret.Username != nil {
					sec.Username = *req.Secret.Username
				}
				if req.Secret.Password != nil {
					sec.Password = *req.Secret.Password
				}
				if req.Secret.Headers != nil {
					sec.Headers = req.Secret.Headers
				}
			}
			if req.Config.URL != "" {
				sec.URL = req.Config.URL
			}
			return existing, sec, nil
		} else if existing.ChannelType == ChannelTypeMQTT {
			sec, _ := h.store.DecryptMQTTSecret(existing.ID, existing.SecretCiphertext)
			if req.Secret != nil && req.Secret.Password != nil {
				sec.Password = *req.Secret.Password
			}
			return existing, sec, nil
		}
	}

	// Draft test
	dest := &AlertDestination{
		ChannelType: req.ChannelType,
		ImageMode:   req.ImageMode,
	}
	if req.ChannelType == ChannelTypeWebhook {
		timeout := 5
		if req.Config.TimeoutSeconds != nil && *req.Config.TimeoutSeconds > 0 {
			timeout = *req.Config.TimeoutSeconds
		}
		authType := AuthTypeNone
		if req.Config.AuthType != "" {
			authType = req.Config.AuthType
		}
		cfgJSON, _ := json.Marshal(WebhookConfig{
			AuthType:       authType,
			TimeoutSeconds: timeout,
		})
		dest.ConfigJSON = string(cfgJSON)

		sec := &webhookSecret{
			URL: req.Config.URL,
		}
		if req.Secret != nil {
			if req.Secret.Token != nil {
				sec.Token = *req.Secret.Token
			}
			if req.Secret.Username != nil {
				sec.Username = *req.Secret.Username
			}
			if req.Secret.Password != nil {
				sec.Password = *req.Secret.Password
			}
			if req.Secret.Headers != nil {
				sec.Headers = req.Secret.Headers
			}
		}
		return dest, sec, nil
	} else if req.ChannelType == ChannelTypeMQTT {
		qos := uint8(0)
		if req.Config.QoS != nil {
			qos = *req.Config.QoS
		}
		keepAlive := 30
		if req.Config.KeepAliveSeconds != nil && *req.Config.KeepAliveSeconds > 0 {
			keepAlive = *req.Config.KeepAliveSeconds
		}
		cfgJSON, _ := json.Marshal(MQTTConfig{
			Broker:           req.Config.Broker,
			Username:         req.Config.Username,
			Topic:            req.Config.Topic,
			QoS:              qos,
			KeepAliveSeconds: keepAlive,
			TLSSkipVerify:    req.Config.TLSSkipVerify,
		})
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
	}
	return nil, nil, ErrInvalidChannelType
}

func (h *Handler) parseCreateRequest(req CreateAlertDestinationRequest) (*AlertDestination, any, error) {
	dest := &AlertDestination{
		Name:        strings.TrimSpace(req.Name),
		ChannelType: req.ChannelType,
		Enabled:     true,
		ImageMode:   req.ImageMode,
	}

	filter := DestinationFilter{}
	if req.Filter != nil {
		filter.EventTypes = req.Filter.EventTypes
		filter.AlgorithmIDs = req.Filter.AlgorithmIDs
		filter.CameraIDs = req.Filter.CameraIDs
	}
	filterBytes, _ := json.Marshal(filter)
	dest.FilterJSON = string(filterBytes)

	if req.ChannelType == ChannelTypeWebhook {
		if strings.TrimSpace(req.Config.URL) == "" {
			return nil, nil, errors.New("url is required for webhook")
		}
		u, err := url.Parse(req.Config.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, nil, errors.New("webhook url must be http or https without userinfo")
		}

		timeout := 5
		if req.Config.TimeoutSeconds != nil && *req.Config.TimeoutSeconds >= 1 && *req.Config.TimeoutSeconds <= 30 {
			timeout = *req.Config.TimeoutSeconds
		}
		authType := AuthTypeNone
		if req.Config.AuthType != "" {
			authType = req.Config.AuthType
		}

		sec := &webhookSecret{
			URL: req.Config.URL,
		}
		if req.Secret != nil {
			if req.Secret.Token != nil {
				sec.Token = *req.Secret.Token
			}
			if req.Secret.Username != nil {
				sec.Username = *req.Secret.Username
			}
			if req.Secret.Password != nil {
				sec.Password = *req.Secret.Password
			}
			if req.Secret.Headers != nil {
				for hk := range req.Secret.Headers {
					if IsReservedHeader(hk) {
						return nil, nil, ErrReservedHeaderUsed
					}
				}
				sec.Headers = req.Secret.Headers
			}
		}

		if authType == AuthTypeBearer && strings.TrimSpace(sec.Token) == "" {
			return nil, nil, errors.New("token is required for bearer auth")
		}
		if authType == AuthTypeBasic && (strings.TrimSpace(sec.Username) == "" || strings.TrimSpace(sec.Password) == "") {
			return nil, nil, errors.New("username and password are required for basic auth")
		}

		cfgJSON, _ := json.Marshal(WebhookConfig{
			AuthType:       authType,
			TimeoutSeconds: timeout,
		})
		dest.ConfigJSON = string(cfgJSON)
		return dest, sec, nil
	} else if req.ChannelType == ChannelTypeMQTT {
		if strings.TrimSpace(req.Config.Broker) == "" {
			return nil, nil, errors.New("broker url is required for mqtt")
		}
		u, err := url.Parse(req.Config.Broker)
		if err != nil || u.User != nil {
			return nil, nil, errors.New("mqtt broker url must not contain userinfo")
		}
		if strings.TrimSpace(req.Config.Topic) == "" {
			return nil, nil, errors.New("topic is required for mqtt")
		}
		if strings.ContainsAny(req.Config.Topic, "+#") {
			return nil, nil, errors.New("mqtt publish topic must not contain wildcards")
		}

		if req.Config.ClientID != "" {
			if len(req.Config.ClientID) > 23 {
				return nil, nil, errors.New("mqtt client id must not exceed 23 characters")
			}
			cID := req.Config.ClientID
			dest.MQTTClientID = &cID
		}

		qos := uint8(0)
		if req.Config.QoS != nil {
			qos = *req.Config.QoS
		}
		keepAlive := 30
		if req.Config.KeepAliveSeconds != nil && *req.Config.KeepAliveSeconds > 0 {
			keepAlive = *req.Config.KeepAliveSeconds
		}

		cfgJSON, _ := json.Marshal(MQTTConfig{
			Broker:           req.Config.Broker,
			Username:         req.Config.Username,
			Topic:            req.Config.Topic,
			QoS:              qos,
			KeepAliveSeconds: keepAlive,
			TLSSkipVerify:    req.Config.TLSSkipVerify,
		})
		dest.ConfigJSON = string(cfgJSON)

		sec := &mqttSecret{}
		if req.Secret != nil && req.Secret.Password != nil {
			sec.Password = *req.Secret.Password
		}
		return dest, sec, nil
	}
	return nil, nil, ErrInvalidChannelType
}

func (h *Handler) parseUpdateRequest(existing *AlertDestination, req UpdateAlertDestinationRequest) (*string, *string, any, *string, error) {
	var configJSON *string
	var filterJSON *string
	var secretRaw any
	var clientID *string

	if req.Filter != nil {
		filter := DestinationFilter{
			EventTypes:   req.Filter.EventTypes,
			AlgorithmIDs: req.Filter.AlgorithmIDs,
			CameraIDs:    req.Filter.CameraIDs,
		}
		fb, _ := json.Marshal(filter)
		fs := string(fb)
		filterJSON = &fs
	}

	if existing.ChannelType == ChannelTypeWebhook {
		var curCfg WebhookConfig
		_ = json.Unmarshal([]byte(existing.ConfigJSON), &curCfg)
		curSec, _ := h.store.DecryptWebhookSecret(existing.ID, existing.SecretCiphertext)

		if req.Config != nil {
			if req.Config.URL != "" {
				u, err := url.Parse(req.Config.URL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
					return nil, nil, nil, nil, errors.New("webhook url must be http or https without userinfo")
				}
				curSec.URL = req.Config.URL
			}
			if req.Config.AuthType != "" {
				curCfg.AuthType = req.Config.AuthType
			}
			if req.Config.TimeoutSeconds != nil && *req.Config.TimeoutSeconds >= 1 && *req.Config.TimeoutSeconds <= 30 {
				curCfg.TimeoutSeconds = *req.Config.TimeoutSeconds
			}
		}

		if req.Secret != nil {
			if req.Secret.Token != nil {
				curSec.Token = *req.Secret.Token
			}
			if req.Secret.Username != nil {
				curSec.Username = *req.Secret.Username
			}
			if req.Secret.Password != nil {
				curSec.Password = *req.Secret.Password
			}
			if req.Secret.Headers != nil {
				for hk := range req.Secret.Headers {
					if IsReservedHeader(hk) {
						return nil, nil, nil, nil, ErrReservedHeaderUsed
					}
				}
				curSec.Headers = req.Secret.Headers
			}
		}

		// Normalize by auth_type
		if curCfg.AuthType == AuthTypeNone {
			curSec.Token = ""
			curSec.Username = ""
			curSec.Password = ""
		} else if curCfg.AuthType == AuthTypeBearer {
			if strings.TrimSpace(curSec.Token) == "" {
				return nil, nil, nil, nil, errors.New("token required for bearer auth")
			}
			curSec.Username = ""
			curSec.Password = ""
		} else if curCfg.AuthType == AuthTypeBasic {
			if strings.TrimSpace(curSec.Username) == "" || strings.TrimSpace(curSec.Password) == "" {
				return nil, nil, nil, nil, errors.New("username and password required for basic auth")
			}
			curSec.Token = ""
		}

		cb, _ := json.Marshal(curCfg)
		cs := string(cb)
		configJSON = &cs
		secretRaw = curSec
	} else if existing.ChannelType == ChannelTypeMQTT {
		var curCfg MQTTConfig
		_ = json.Unmarshal([]byte(existing.ConfigJSON), &curCfg)
		curSec, _ := h.store.DecryptMQTTSecret(existing.ID, existing.SecretCiphertext)

		if req.Config != nil {
			if req.Config.Broker != "" {
				curCfg.Broker = req.Config.Broker
			}
			if req.Config.Username != "" {
				curCfg.Username = req.Config.Username
			}
			if req.Config.Topic != "" {
				if strings.ContainsAny(req.Config.Topic, "+#") {
					return nil, nil, nil, nil, errors.New("mqtt topic must not contain wildcards")
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
				clientID = &cID
			}
		}

		if req.Secret != nil && req.Secret.Password != nil {
			curSec.Password = *req.Secret.Password
		}

		cb, _ := json.Marshal(curCfg)
		cs := string(cb)
		configJSON = &cs
		secretRaw = curSec
	}

	return configJSON, filterJSON, secretRaw, clientID, nil
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

	var filter DestinationFilter
	_ = json.Unmarshal([]byte(dest.FilterJSON), &filter)
	resp.Filter = AlertDestinationFilterInfo{
		EventTypes:   filter.EventTypes,
		AlgorithmIDs: filter.AlgorithmIDs,
		CameraIDs:    filter.CameraIDs,
	}
	if resp.Filter.EventTypes == nil {
		resp.Filter.EventTypes = []string{}
	}
	if resp.Filter.AlgorithmIDs == nil {
		resp.Filter.AlgorithmIDs = []string{}
	}
	if resp.Filter.CameraIDs == nil {
		resp.Filter.CameraIDs = []string{}
	}

	if dest.ChannelType == ChannelTypeWebhook {
		var cfg WebhookConfig
		_ = json.Unmarshal([]byte(dest.ConfigJSON), &cfg)
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
	} else if dest.ChannelType == ChannelTypeMQTT {
		var cfg MQTTConfig
		_ = json.Unmarshal([]byte(dest.ConfigJSON), &cfg)
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
