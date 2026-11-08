package task

import (
	"context"
	"errors"

	"github.com/nikonikowuw/Zhulong/internal/region"
)

// CropFor 实现 camera.TaskCropProvider：返回该相机启用任务的裁剪窗口。
//
// 语义边界：
//   - 「没有任务 / 任务停用 / crop 为空或整帧」一律返回 (nil, nil)，表示不裁剪；
//   - 读库或解析失败返回 error —— 调用方必须保留当前视图，
//     绝不把「读不到」当成「清空」，否则一次数据库抖动就会让
//     整路 AI 从裁剪窗口跳回全幅，检测坐标静默错位一整批结果。
func (s *Store) CropFor(ctx context.Context, cameraID string) (*region.Rect, error) {
	tsk, err := s.GetTaskByCamera(ctx, cameraID)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	case tsk == nil || !tsk.Enabled:
		return nil, nil
	}

	crop, err := region.ParseCrop(tsk.CropJSON)
	if err != nil {
		return nil, err
	}
	if crop == nil || crop.IsIdentity() {
		return nil, nil
	}
	return crop, nil
}
