package camera

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 受 auth.RequireAuth 保护
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 64 * 1024,
}

// Handler exposes REST API, SSE, and WebSocket endpoints for camera management.
type Handler struct {
	service    *CameraService
	hub        *EventHub
	streamHub  *StreamHub
	audio      *AudioChannelRegistry
	sessionVal func(token string) bool
	logger     *zap.Logger
}

// NewHandler creates a new Handler.
func NewHandler(
	service *CameraService,
	hub *EventHub,
	streamHub *StreamHub,
	logger *zap.Logger,
	sessionVal ...func(token string) bool,
) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	var valFn func(token string) bool
	if len(sessionVal) > 0 {
		valFn = sessionVal[0]
	}
	return &Handler{
		service:    service,
		hub:        hub,
		streamHub:  streamHub,
		audio:      NewAudioChannelRegistry(logger),
		sessionVal: valFn,
		logger:     logger.Named("camera.handler"),
	}
}

// RegisterRoutes registers all camera endpoints on the provided router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/cameras")
	{
		// SSE endpoint must be registered before param routes to avoid routing collisions
		group.GET("/events", h.Events)

		group.POST("", h.Create)
		group.GET("", h.List)
		group.GET("/:id", h.Get)
		group.GET("/:id/credentials", h.GetCredentials)
		group.PUT("/:id", h.Update)
		group.DELETE("/:id", h.Delete)
		group.POST("/:id/diagnose", h.Diagnose)
		group.GET("/:id/ai-stats", h.AIStreamStats)

		// WebSocket video streaming endpoints
		group.GET("/:id/ws", h.StreamWS)
		group.GET("/:id/stream/ws", h.StreamWS)
		group.GET("/:id/streams/:role/ws", h.StreamWS)
		// 按需音频通道：只在用户解禁某个格子时建立，与预览二进制帧完全独立
		group.GET("/:id/streams/:role/audio/ws", h.StreamAudioWS)
	}
}

// Create godoc
// @Summary      Create a new camera
// @Tags         camera
// @Accept       json
// @Produce      json
// @Param        request body CreateCameraRequest true "Camera parameters"
// @Success      201  {object}  httputil.Response{data=CameraResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Router       /cameras [post]
func (h *Handler) Create(c *gin.Context) {
	// Declared before binding so a rejected payload is still attributable.
	audithttp.Tag(
		c,
		audit.ActionCameraCreate,
		"camera",
		"Create camera",
	)

	var req CreateCameraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	res, err := h.service.Create(c.Request.Context(), req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	audithttp.TagTarget(c, "camera:"+res.ID)
	audithttp.TagDetail(c, createAuditDetail(req))
	httputil.SuccessWithStatus(c, http.StatusCreated, res)
}

// List godoc
// @Summary      List cameras
// @Tags         camera
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        limit query int false "Legacy pagination limit"
// @Param        offset query int false "Legacy pagination offset"
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[CameraResponse]}
// @Router       /cameras [get]
func (h *Handler) List(c *gin.Context) {
	var query httputil.PaginationQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		h.logger.Debug("bind pagination query failed, using defaults", zap.Error(err))
	}
	window := query.Normalize()

	cameras, total, err := h.service.List(c.Request.Context(), window.PageSize, window.Offset)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(
		c,
		cameras,
		total,
		window,
	)
}

// Get godoc
// @Summary      Get camera by ID
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=CameraResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	res, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httputil.Success(c, res)
}

// GetCredentials godoc
// @Summary      Get camera plaintext credentials
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=CameraCredentialsResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id}/credentials [get]
func (h *Handler) GetCredentials(c *gin.Context) {
	id := c.Param("id")
	res, err := h.service.GetCredentials(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httputil.Success(c, res)
}

// Update godoc
// @Summary      Update camera configuration
// @Tags         camera
// @Accept       json
// @Produce      json
// @Param        id path string true "Camera ID"
// @Param        request body UpdateCameraRequest true "Updated parameters"
// @Success      200  {object}  httputil.Response{data=CameraResponse}
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Router       /cameras/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	id := c.Param("id")
	// The route serves both update and toggle, and the distinction depends on
	// the request body. Declare update first so a bind failure is still
	// auditable, then refine the action once the body is known.
	audithttp.Tag(
		c,
		audit.ActionCameraUpdate,
		"camera:"+id,
		"",
	)

	var req UpdateCameraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	isToggleOnly := req.Enabled != nil && req.Name == nil && req.MainStream == nil && req.SubStream == nil
	if isToggleOnly {
		audithttp.TagAction(c, audit.ActionCameraToggle)
	}
	audithttp.TagDetail(c, updateAuditDetail(req))

	res, err := h.service.Update(c.Request.Context(), id, req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, res)
}

// Delete godoc
// @Summary      Delete camera
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(
		c,
		audit.ActionCameraDelete,
		"camera:"+id,
		"Delete camera "+id,
	)

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"id": id, "deleted": true})
}

// Diagnose godoc
// @Summary      Manually diagnose camera connection
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=DiagnoseResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id}/diagnose [post]
func (h *Handler) Diagnose(c *gin.Context) {
	id := c.Param("id")
	res, err := h.service.Diagnose(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, res)
}

// AIStreamStats godoc
// @Summary      Get per-algorithm frame delivery stats for a camera AI stream
// @Description  Reports in-flight / delivered / dropped frame counters for every algorithm
// @Description  package subscribed to this camera's AI analysis stream.
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=AIStreamStatsResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      503  {object}  httputil.Response
// @Router       /cameras/{id}/ai-stats [get]
func (h *Handler) AIStreamStats(c *gin.Context) {
	if h.streamHub == nil {
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"STREAM_HUB_NOT_INITIALIZED",
			"Stream hub not available",
			nil,
		))
		return
	}

	cameraID := c.Param("id")
	clients, total, err := h.streamHub.AIStreamBrokerStats(c.Request.Context(), cameraID)
	if err != nil {
		switch {
		case errors.Is(err, ErrAIStreamInactive):
			// 暂时状态而非配置错误：任务停用或算法未启动时就是这样。
			httputil.WriteError(c, apperr.New(apperr.KindNotFound, "AI_STREAM_INACTIVE",
				"Camera has no active AI analysis stream", err))
		case errors.Is(err, ErrStreamHubClosed), errors.Is(err, engine.ErrNotRunning), errors.Is(err, engine.ErrClosed):
			// 可恢复的暂时状态：宿主正在停机或引擎尚未就绪，重启后可再查。
			httputil.WriteError(c, apperr.New(apperr.KindPrecondition, "MEDIA_ENGINE_UNAVAILABLE",
				"Media engine is not running", err))
		default:
			httputil.WriteError(c, apperr.New(apperr.KindInternal, "AI_STREAM_STATS_FAILED",
				"Failed to read AI stream delivery stats", err))
		}
		return
	}

	res := AIStreamStatsResponse{
		CameraID:    cameraID,
		ClientCount: total,
		Clients:     make([]AIStreamClientInfo, 0, len(clients)),
	}
	for _, client := range clients {
		res.Clients = append(res.Clients, AIStreamClientInfo{
			ClientID:        client.ClientID,
			Name:            client.Name,
			InFlight:        client.InFlight,
			MaxInFlight:     client.MaxInFlight,
			DeliveredFrames: client.DeliveredFrames,
			DroppedFrames:   client.DroppedFrames,
		})
	}
	c.Header("Cache-Control", "no-store")
	httputil.Success(c, res)
}

// Events godoc
// @Summary      Subscribe to camera state events via SSE
// @Tags         camera
// @Produce      text/event-stream
// @Success      200  {string}  string "Server-Sent Events stream"
// @Router       /cameras/events [get]
func (h *Handler) Events(c *gin.Context) {
	sub, snapshotMsg, err := h.hub.Subscribe()
	if err != nil {
		if errors.Is(err, ErrSSEMaxClientsReached) {
			httputil.WriteError(c, apperr.New(
				apperr.KindRateLimited,
				"TOO_MANY_CONNECTIONS",
				"Too many active SSE connections",
				err,
			))
			return
		}
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"SSE_ERROR",
			"Failed to initiate SSE session",
			err,
		))
		return
	}
	defer h.hub.Unsubscribe(sub)

	// Set SSE HTTP response headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"STREAMING_UNSUPPORTED",
			"Streaming unsupported",
			nil,
		))
		return
	}

	// 1. Send initial snapshot
	snapshotBytes, err := snapshotMsg.FormatSSE()
	if err != nil {
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"SSE_ERROR",
			"Failed to format initial SSE snapshot",
			err,
		))
		return
	}
	if _, err := c.Writer.Write(snapshotBytes); err != nil {
		return
	}
	flusher.Flush()

	clientGone := c.Request.Context().Done()
	authTicker := time.NewTicker(30 * time.Second)
	defer authTicker.Stop()

	for {
		select {
		case <-clientGone:
			return
		case <-authTicker.C:
			if h.sessionVal != nil {
				var token string
				if cookie, err := c.Request.Cookie("zhulong_session"); err == nil {
					token = cookie.Value
				}
				if token == "" || !h.sessionVal(token) {
					return // Session expired or revoked
				}
			}
		case msg, ok := <-sub.Channel():
			if !ok {
				return // Channel closed (e.g. server shutdown or slow client eviction)
			}
			msgBytes, err := msg.FormatSSE()
			if err != nil {
				continue
			}
			if _, err := c.Writer.Write(msgBytes); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// StreamWS godoc
// @Summary      WebSocket 视频流实时预览
// @Tags         camera
// @Param        id   path   string  true  "摄像机 ID"
// @Param        role path   string  false "码流类型 (main 或 sub，默认 main)"
// @Router       /cameras/{id}/streams/{role}/ws [get]
func (h *Handler) StreamWS(c *gin.Context) {
	if h.streamHub == nil {
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"STREAM_HUB_NOT_INITIALIZED",
			"Stream hub not available",
			nil,
		))
		return
	}

	cameraID := c.Param("id")
	role := c.Param("role")
	if role == "" {
		role = c.DefaultQuery("role", "main")
	}

	// 1. 获取或拉起流分发器（若相机不存在、被禁用或流不存在，返回对应语义错误）
	disp, err := h.streamHub.GetOrCreateDispatcher(c.Request.Context(), cameraID, role)
	if err != nil {
		writeStreamAcquireError(c, err)
		return
	}

	// 2. 协议升级至 WebSocket
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Debug("websocket upgrade failed", zap.Error(err))
		return
	}

	// 3. 一次性能力帧必须在任何二进制帧之前、且只有一个写者时写出。
	//
	// 此处尚未注册客户端、也未启动写协程：GOP 补发还在后面，连接上只有本协程在写，
	// 因此既不会与补发交错，也不需要锁。老前端按 `instanceof ArrayBuffer` 判据忽略
	// 文本帧，协议版本与二进制布局都没有变化。
	h.writeStreamInfo(streamInfoParams{
		ctx:      c.Request.Context(),
		conn:     conn,
		disp:     disp,
		cameraID: cameraID,
		role:     role,
	})

	// 4. 创建客户端会话并登记
	client := NewStreamClient(conn, disp, h.logger)
	disp.RegisterClient(client)

	// 5. 运行写循环与读循环（阻塞当前 handler 协程直到客户端断开）
	go client.WritePump()
	client.ReadPump()
}

// writeStreamAcquireError 把拉流错误映射成统一的 HTTP 错误信封。
//
// 视频与音频两条 WS 共用同一套语义：同一个 vhost 上出现「视频报 404、音频报 500」
// 这种差异只会让前端为同一件事写两套分支。
func writeStreamAcquireError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrCameraNotFound):
		httputil.WriteError(c, apperr.New(
			apperr.KindNotFound,
			"CAMERA_NOT_FOUND",
			"Camera not found",
			err,
		))
	case errors.Is(err, ErrCameraDisabled):
		httputil.WriteError(c, apperr.New(
			apperr.KindPermissionDenied,
			"CAMERA_DISABLED",
			"Camera is disabled",
			err,
		))
	case errors.Is(err, ErrCameraStreamNotFound):
		httputil.WriteError(c, apperr.New(
			apperr.KindNotFound,
			"STREAM_NOT_FOUND",
			"Camera stream not found",
			err,
		))
	case errors.Is(err, ErrStreamHubClosed):
		httputil.WriteError(c, apperr.New(
			apperr.KindUnavailable,
			"STREAM_HUB_CLOSED",
			"Stream hub is shutting down",
			err,
		))
	default:
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"STREAM_ACQUIRE_FAILED",
			"Failed to acquire camera stream",
			err,
		))
	}
}

type streamInfoParams struct {
	ctx      context.Context
	conn     *websocket.Conn
	disp     *StreamDispatcher
	cameraID string
	role     string
}

// writeStreamInfo 写出预览 WS 的一次性能力帧。
//
// 写失败只记日志：能力帧丢失的后果是「前端听不到声音」，而让整条预览断掉
// 会把「没声音」升级成「黑屏」，与 I5（任一环不支持都退化为无声视频）相反。
func (h *Handler) writeStreamInfo(params streamInfoParams) {
	message := StreamInfoMessage{
		Type:     StreamFrameTypeInfo,
		Version:  int(WireProtocolVersion),
		CameraID: params.cameraID,
		Role:     params.role,
		Audio:    params.disp.AudioCapability(params.ctx),
	}
	payload, err := json.Marshal(message)
	if err != nil {
		h.logger.Debug("marshal stream info failed", zap.Error(err))
		return
	}
	_ = params.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	if err := params.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		h.logger.Debug("write stream info failed", zap.Error(err))
	}
}

// StreamAudioWS 提供按需音频通道。
//
// 与预览 WS 的关系是「同一路流的两种视图」：预览照旧只发视频二进制帧，
// 音频走这条独立连接，因此视频的背压、GOP、健康态与协议版本都不受音频影响。
//
// @Summary      按需音频通道（ZLA1）
// @Description  首个文本帧为 audio_info；随后为 20 字节 ZLA1 头 + 裸负载的二进制帧。
// @Description  同一登录会话只允许一条：新连接以 4001 接管旧的；全局上限 8 条（4002）；
// @Description  无音轨时先发 error 文本帧再以 4404 关闭；队列溢出以 4003 关闭。
// @Tags         camera
// @Param        id   path   string  true  "摄像机 ID"
// @Param        role path   string  false "码流类型 (main 或 sub，默认 main)"
// @Router       /cameras/{id}/streams/{role}/audio/ws [get]
func (h *Handler) StreamAudioWS(c *gin.Context) {
	if h.streamHub == nil {
		httputil.WriteError(c, apperr.New(
			apperr.KindInternal,
			"STREAM_HUB_NOT_INITIALIZED",
			"Stream hub not available",
			nil,
		))
		return
	}

	cameraID := c.Param("id")
	role := c.Param("role")
	if role == "" {
		role = c.DefaultQuery("role", "main")
	}

	sessionKey := sessionChannelKey(auth.GetSessionToken(c))
	if sessionKey == "" {
		httputil.WriteError(c, apperr.Unauthenticated("UNAUTHORIZED", "Authentication required", nil))
		return
	}

	// 1. 先解析相机与流并拿到音频能力：这些错误应当在协议升级**之前**以 HTTP 信封返回，
	//    否则前端拿到的是一个「连上了又立刻被关」的 WebSocket，无法区分原因。
	acquired, err := h.streamHub.AcquireAudioStream(c.Request.Context(), cameraID, role)
	if err != nil {
		writeStreamAcquireError(c, err)
		return
	}

	// 2. 协议升级
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Debug("audio websocket upgrade failed", zap.Error(err))
		_ = acquired.Stream.Close()
		return
	}

	// 3. 无音频：明确告知并关闭，不订阅、不注册
	if !acquired.Info.HasAudio {
		writeAudioTerminalFrame(conn, AudioErrorMessage{
			Type:   AudioFrameTypeError,
			Code:   AudioErrorNoAudio,
			Reason: "camera reports no playable audio track",
		})
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(AudioCloseNoAudio, "no audio"), time.Now().Add(time.Second))
		_ = conn.Close()
		_ = acquired.Stream.Close()
		h.logger.Info("audio channel rejected: no audio",
			zap.String("session", sessionKey), zap.String("camera_id", cameraID), zap.String("role", role))
		return
	}

	// 4. 音频订阅（先订阅成功再注册，避免订阅失败把健康的旧通道顶掉）
	sub, err := acquired.Stream.Subscribe(c.Request.Context(), engine.SubscriptionOptions{
		TrackMask:        engine.TrackMaskAudio,
		MaxPackets:       audioChannelQueueFrames,
		MaxPacketBytes:   8 * 1024,
		MaxBufferedBytes: 512 * 1024,
	})
	if err != nil {
		writeAudioTerminalFrame(conn, AudioErrorMessage{
			Type:   AudioFrameTypeError,
			Code:   AudioErrorSubscribeFailed,
			Reason: "failed to subscribe camera audio",
		})
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(AudioCloseInternal, "subscribe failed"), time.Now().Add(time.Second))
		_ = conn.Close()
		_ = acquired.Stream.Close()
		h.logger.Warn("audio subscribe failed",
			zap.String("session", sessionKey), zap.String("camera_id", cameraID),
			zap.String("role", role), zap.Error(err))
		return
	}

	channel := NewAudioChannel(AudioChannelConfig{
		Registry: h.audio,
		Conn:     conn,
		Stream:   acquired.Stream,
		Sub:      sub,
		Key:      sessionKey,
		CameraID: cameraID,
		Role:     role,
		Logger:   h.logger,
	})

	previous, err := h.audio.Open(sessionKey, channel)
	if err != nil {
		writeAudioTerminalFrame(conn, AudioErrorMessage{
			Type:   AudioFrameTypeError,
			Code:   AudioErrorLimit,
			Reason: "too many audio channels",
		})
		channel.closeWithReason(AudioCloseTooManyChannels, "too_many_channels")
		return
	}
	// 5. 先顶掉同一会话的旧连接，再开始推送：否则两个连接会同时出声
	if previous != nil {
		h.logger.Info("audio channel superseded",
			zap.String("session", sessionKey), zap.String("camera_id", cameraID), zap.String("role", role))
		previous.Supersede()
	}

	// 6. 文本帧 → 写协程 → 读协程（阻塞到客户端断开）
	if err := channel.SendInfo(AudioInfoMessage{
		Type:       AudioFrameTypeInfo,
		HasAudio:   true,
		Codec:      acquired.Info.Codec.String(),
		SampleRate: acquired.Info.SampleRate,
		Channels:   acquired.Info.Channels,
		Config:     audioCapability(acquired.Info, "").Config,
	}); err != nil {
		channel.closeWithReason(AudioCloseInternal, "info frame failed")
		return
	}

	h.logger.Info("audio channel opened",
		zap.String("session", sessionKey),
		zap.String("camera_id", cameraID),
		zap.String("role", role),
		zap.String("codec", acquired.Info.Codec.String()),
		zap.Int("sample_rate", acquired.Info.SampleRate),
		zap.Int("channels", acquired.Info.Channels),
		zap.Int("channels_total", h.audio.Len()),
	)

	pumpCtx, cancelPump := context.WithCancel(context.Background())
	defer cancelPump()
	go func() {
		defer cancelPump()
		channel.Pump(pumpCtx)
	}()
	go channel.WritePump()
	channel.ReadPump()
}

// writeAudioTerminalFrame 尽力写一条终止性文本帧；失败无所谓，连接马上要关。
func writeAudioTerminalFrame(conn *websocket.Conn, message AudioErrorMessage) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	_ = conn.WriteMessage(websocket.TextMessage, payload)
}

// Ensure interface compliance
var _ io.Closer = (*Handler)(nil)

func (h *Handler) Close() error {
	// 先收音频通道：它们各自持有一条流引用与订阅，必须在事件总线停掉之前排空，
	// 否则写协程会对着已关闭的 hub 继续等数据。
	h.audio.CloseAll()
	return h.hub.Close()
}

func streamAuditDetail(req *CreateStreamRequest) map[string]any {
	if req == nil {
		return nil
	}
	return map[string]any{
		"protocol":  req.Protocol,
		"transport": req.Transport,
		"rtspUrl":   SanitizeURL(req.RTSPURL),
	}
}

func createAuditDetail(req CreateCameraRequest) string {
	detail := map[string]any{
		"name":       req.Name,
		"mainStream": streamAuditDetail(&req.MainStream),
	}
	if req.Enabled != nil {
		detail["enabled"] = *req.Enabled
	}
	if req.SubStream != nil {
		detail["subStream"] = streamAuditDetail(req.SubStream)
	}
	b, _ := json.Marshal(detail)
	return string(b)
}

func updateAuditDetail(req UpdateCameraRequest) string {
	detail := make(map[string]any)
	if req.Name != nil {
		detail["name"] = *req.Name
	}
	if req.Enabled != nil {
		detail["enabled"] = *req.Enabled
	}
	if req.MainStream != nil {
		detail["mainStream"] = streamAuditDetail(req.MainStream)
	}
	if req.SubStream != nil {
		detail["subStream"] = streamAuditDetail(req.SubStream)
	}
	b, _ := json.Marshal(detail)
	return string(b)
}
