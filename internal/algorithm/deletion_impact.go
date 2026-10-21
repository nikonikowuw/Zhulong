package algorithm

import (
	"context"
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// DeletionImpact 描述卸载一个算法包会产生什么后果。
//
// 设计目的：卸载是**不可撤销**操作。数据不会被删（感知记录走 ON DELETE SET NULL，
// 见迁移 000015/000016），但用户仍应事先知道会失去什么能力：
//
//   - 绑定任务会失去该算法 —— 这些相机在这条算法上的分析会停止；
//   - 历史记录会失去算法引用，只剩写入时快照的名称/版本，因此**无法再按算法筛选**；
//   - 磁盘上的算法包与模型会被删除，需要重新安装才能恢复。
//
// ⚠️ 刻意不统计抓拍图片的字节数：那需要对 snapshots/ 做目录遍历，几千个文件在
// 低配 RK3568 上会明显拖慢接口，而用户真正关心的是「会丢多少条记录 / 多少路任务」。
// 图片不会因此消失，字节数不影响决策。
type DeletionImpact struct {
	AlgorithmID   string `json:"algorithmId"`
	AlgorithmName string `json:"algorithmName"`
	Version       string `json:"version"`
	Status        string `json:"status"`

	// BoundTaskCount 是当前绑定了该算法的通道任务数。卸载后这些任务的该算法条目
	// 会被删除；任务本身保留（其他算法不受影响），但若某任务因此变成空任务，
	// 那台相机就完全停止分析了。
	BoundTaskCount int64 `json:"boundTaskCount"`
	// AffectedCameraIDs 列出受影响相机，供前端直接展示「这 3 台相机会停」。
	AffectedCameraIDs []string `json:"affectedCameraIds"`

	// RecognitionCount / AlarmCount / CaptureCount 是历史证据条数。
	// 卸载**不会删除**它们，只会让 algorithm_id 变成 NULL。
	// 前端文案必须准确：是「失去归属」而非「删除」。
	RecognitionCount int64 `json:"recognitionCount"`
	AlarmCount       int64 `json:"alarmCount"`
	CaptureCount     int64 `json:"captureCount"`

	// RequiresStop 表示算法当前正在运行，卸载会先强制停止它。
	RequiresStop bool `json:"requiresStop"`
}

// HasEvidence 报告是否存在会失去归属的历史证据。
func (i DeletionImpact) HasEvidence() bool {
	return i.RecognitionCount > 0 || i.AlarmCount > 0 || i.CaptureCount > 0
}

// GetDeletionImpact 计算卸载该算法包的影响面，不产生任何副作用。
//
// 这是一个纯查询接口：前端应在弹出卸载确认框之前调用它，把结果展示给用户，
// 用户确认后再发 DELETE。
func (s *Service) GetDeletionImpact(ctx context.Context, id string) (*DeletionImpact, error) {
	algo, err := s.store.GetAlgorithm(ctx, id)
	if err != nil {
		return nil, s.wrapLoadError(err)
	}

	impact := &DeletionImpact{
		AlgorithmID:   algo.ID,
		AlgorithmName: algo.Name,
		Version:       algo.Version,
		Status:        algo.Status,
	}

	// 任务绑定与受影响的相机
	cameraIDs, err := s.store.ListCameraIDsByAlgorithm(ctx, id)
	if err != nil {
		return nil, apperr.Internal("IMPACT_QUERY_FAILED", "Failed to count bound tasks", err)
	}
	impact.AffectedCameraIDs = cameraIDs
	impact.BoundTaskCount = int64(len(cameraIDs))

	// 历史证据条数
	counts, err := s.store.CountEvidenceByAlgorithm(ctx, id)
	if err != nil {
		return nil, apperr.Internal("IMPACT_QUERY_FAILED", "Failed to count evidence", err)
	}
	impact.RecognitionCount = counts.Recognitions
	impact.AlarmCount = counts.Alarms
	impact.CaptureCount = counts.Captures

	// 运行状态决定是否需要先停进程。GetStatus 返回 DISABLED 表示没有 watcher。
	status := s.supervisor.GetStatus(id)
	switch status {
	case StatusRunning, StatusStarting, StatusRollingBack:
		impact.RequiresStop = true
	}

	return impact, nil
}

// wrapLoadError 把底层错误统一成 API 错误，避免 GetAlgorithm 的三种失败在
// 两个调用点（DELETE 与 impact 查询）出现不一致的文案。
func (s *Service) wrapLoadError(err error) error {
	if err == ErrNotFound {
		return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
	}
	return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("get algorithm: %v", err), err)
}
