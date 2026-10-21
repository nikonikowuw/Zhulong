package algorithm

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler provides HTTP endpoints for algorithm packages, analysis tasks, and face galleries.
type Handler struct {
	service        *Service
	logger         *zap.Logger
	maxUploadLimit int64
}

// NewHandler creates a new Handler instance.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service:        service,
		logger:         logger.Named("algorithm.handler"),
		maxUploadLimit: MaxUploadSize,
	}
}

// SetMaxUploadLimitForTest overrides the max upload limit for testing.
func (h *Handler) SetMaxUploadLimitForTest(limit int64) {
	h.maxUploadLimit = limit
}

// RegisterRoutes registers control-plane algorithm endpoints on the provided router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/algorithms")
	{
		group.GET("", h.List)
		group.GET("/:id", h.Get)
		group.POST("/:id/enable", h.Enable)
		group.POST("/:id/disable", h.Disable)
		group.POST("/:id/reset", h.Reset)
		// 卸载影响面查询必须用独立的 GET，而不是让 DELETE 返回 409 + 载荷：
		// 后者会让「查询」与「执行」混在一个非幂等方法里，前端无法在用户
		// 确认前安全地预览后果。
		group.GET("/:id/deletion-impact", h.GetDeletionImpact)
		group.DELETE("/:id", h.Delete)
	}
}

// RegisterUploadRoutes registers large-body upload endpoints on the upload router group.
func (h *Handler) RegisterUploadRoutes(rg *gin.RouterGroup) {
	rg.POST("/algorithms/install", h.Install)
}

type uploadRouteRegistrar struct {
	handler *Handler
}

func (r *uploadRouteRegistrar) RegisterRoutes(rg *gin.RouterGroup) {
	r.handler.RegisterUploadRoutes(rg)
}

// UploadRouteRegistrar returns a RouteRegistrar adapter for upload endpoints.
func (h *Handler) UploadRouteRegistrar() RouteRegistrar {
	return &uploadRouteRegistrar{handler: h}
}

// Install godoc
// @Summary      Install an algorithm package
// @Description  Streams a multipart archive to a temporary file, then verifies platform target, resource budget, sample inference and config schema before registering the algorithm. Rejects platform mismatch, unsupported archive formats, zip-slip paths, decompression bombs, invalid manifest tokens and already installed versions.
// @Tags         algorithm
// @Accept       multipart/form-data
// @Produce      json
// @Param        file formData file true "Algorithm package archive (at most 500 MB)"
// @Success      201  {object}  httputil.Response{data=Algorithm}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/install [post]
func (h *Handler) Install(c *gin.Context) {
	// Declared before the multipart stream is read so a rejected archive is
	// still attributable. The target is refined once the algorithm is known.
	audithttp.Tag(c, ActionAlgorithmInstall, "algorithm:", "Install algorithm")

	filePart, cleanup, err := ExtractMultipartFile(c.Request, "file")
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	defer cleanup()

	algo, err := h.service.InstallAlgorithmFromStream(c.Request.Context(), filePart, h.maxUploadLimit)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.TagTarget(c, "algorithm:"+algo.ID)
	audithttp.TagDetail(c, algo.Name)

	httputil.SuccessWithStatus(c, http.StatusCreated, algo)
}

// List godoc
// @Summary      List registered algorithms
// @Tags         algorithm
// @Produce      json
// @Success      200  {object}  httputil.Response{data=[]Algorithm}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms [get]
func (h *Handler) List(c *gin.Context) {
	algos, err := h.service.ListAlgorithms(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	if algos == nil {
		algos = make([]Algorithm, 0)
	}
	httputil.Success(c, algos)
}

// Get godoc
// @Summary      Get algorithm details by ID
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=Algorithm}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	algo, err := h.service.GetAlgorithm(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, algo)
}

// Enable godoc
// @Summary      Enable an algorithm and start its runner process
// @Description  Starts the supervised runner subprocess and sets the algorithm status to ACTIVE. The runner is monitored and restarted according to the recovery policy until the crash-loop threshold is reached.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/enable [post]
func (h *Handler) Enable(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmEnable, "algorithm:"+id, "Enable algorithm "+id)

	if err := h.service.EnableAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"status": StatusActive})
}

// Disable godoc
// @Summary      Disable an algorithm and stop its runner process
// @Description  Stops the supervised runner subprocess and sets the algorithm status to DISABLED.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/disable [post]
func (h *Handler) Disable(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmDisable, "algorithm:"+id, "Disable algorithm "+id)

	if err := h.service.DisableAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"status": StatusDisabled})
}

// Reset godoc
// @Summary      Reset an algorithm stuck in CRASH_LOOP
// @Description  Clears the crash-loop circuit breaker and returns the algorithm to DISABLED so it can be enabled again. Returns 409 ALGORITHM_NOT_IN_CRASH_LOOP when the algorithm is not currently in CRASH_LOOP.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/reset [post]
func (h *Handler) Reset(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmReset, "algorithm:"+id, "Reset algorithm "+id)

	if err := h.service.ResetAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"status": StatusDisabled})
}

// GetDeletionImpact godoc
// @Summary      Preview the impact of deleting an algorithm
// @Description  Returns how many tasks, cameras and evidence records reference this algorithm. Read-only: it has no side effects and can be called freely before asking the operator to confirm. Note that deleting an algorithm does NOT delete evidence records — it only clears their algorithm reference, so `algorithmId` becomes empty while the `algorithmName` / `algorithmVersion` snapshots remain.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=DeletionImpact}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/deletion-impact [get]
func (h *Handler) GetDeletionImpact(c *gin.Context) {
	id := c.Param("id")

	// 刻意不打审计标签：这是只读预览，用户可能只是点开看看就取消，
	// 记录成操作会把审计日志淹没在无效条目里。
	impact, err := h.service.GetDeletionImpact(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, impact)
}

// Delete godoc
// @Summary      Delete an algorithm
// @Description  Removes the algorithm registration and its installed files. Deleting an algorithm that is currently ACTIVE returns 409 ALGORITHM_IS_ACTIVE; disable it first.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmDelete, "algorithm:"+id, "Delete algorithm "+id)

	// 卸载前先取一次影响面，写进审计详情。
	//
	// 理由：卸载之后这些关联信息就不可重建了（tasks 条目被删、记录失去
	// algorithm_id）。事后追查「当时卸载造成了什么后果」只能靠这条审计。
	// ⚠️ 这里是尽力而为：取不到影响面不应阻断卸载本身。
	var impact *DeletionImpact
	if computed, err := h.service.GetDeletionImpact(c.Request.Context(), id); err == nil {
		impact = computed
	}

	if err := h.service.DeleteAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}

	if impact != nil {
		audithttp.TagDetail(c, fmt.Sprintf(
			"uninstalled %s v%s; detached %d task(s) on %s, kept %d recognition(s)/%d alarm(s)/%d capture(s) with name snapshot",
			impact.AlgorithmName, impact.Version, impact.BoundTaskCount,
			strings.Join(impact.AffectedCameraIDs, ","),
			impact.RecognitionCount, impact.AlarmCount, impact.CaptureCount))
	}
	httputil.Success(c, gin.H{"deleted": true})
}
