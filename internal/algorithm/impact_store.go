package algorithm

import (
	"context"

	"gorm.io/gorm"
)

// EvidenceCounts 汇总某算法产生的历史证据条数。
type EvidenceCounts struct {
	Recognitions int64
	Alarms       int64
	Captures     int64
}

// ListCameraIDsByAlgorithm 返回当前绑定了该算法的相机 ID（按字典序）。
//
// 走 task_algorithms JOIN analysis_tasks，只取 camera_id：不加载整行实体，
// 避免把任务配置 JSON 这类大字段拉进内存。
func (s *Store) ListCameraIDsByAlgorithm(ctx context.Context, algorithmID string) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var ids []string
	err = db.Table("task_algorithms").
		Select("DISTINCT analysis_tasks.camera_id").
		Joins("JOIN analysis_tasks ON analysis_tasks.id = task_algorithms.task_id").
		Where("task_algorithms.algorithm_id = ?", algorithmID).
		Order("analysis_tasks.camera_id").
		Pluck("analysis_tasks.camera_id", &ids).Error
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = make([]string, 0)
	}
	return ids, nil
}

// CountEvidenceByAlgorithm 统计该算法产生的感知记录条数。
//
// ⚠️ 这是**只读统计**，不代表卸载会删除这些记录。卸载时外键是 SET NULL，
// 记录保留且算法名称/版本快照仍在（迁移 000015/000016），失去的只是
// 「按 algorithm_id 筛选」的能力。
//
// 三条 COUNT 在同一个事务里执行，保证得到的是一致快照 —— 否则并发落库会让
// 「识别 100 条 / 告警 99 条」这种数字自相矛盾。
func (s *Store) CountEvidenceByAlgorithm(ctx context.Context, algorithmID string) (EvidenceCounts, error) {
	var counts EvidenceCounts
	db, err := s.db(ctx)
	if err != nil {
		return counts, err
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("recognitions").
			Where("algorithm_id = ?", algorithmID).
			Count(&counts.Recognitions).Error; err != nil {
			return err
		}
		if err := tx.Table("alarms").
			Where("algorithm_id = ?", algorithmID).
			Count(&counts.Alarms).Error; err != nil {
			return err
		}
		return tx.Table("captures").
			Where("algorithm_id = ?", algorithmID).
			Count(&counts.Captures).Error
	})
	return counts, err
}
