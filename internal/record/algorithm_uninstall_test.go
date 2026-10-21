package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// TestEvidenceSurvivesAlgorithmUninstall 是「卸载算法不得删除证据」这条业务
// 规则的端到端回归。
//
// 场景：算法产生过告警 → 运维卸载该算法 → 历史告警必须仍在，并保留算法名与
// 版本快照，只是不再指向任何在册算法。
//
// 这条链路曾经是错的：迁移 000014 给 algorithm_id 加了 ON DELETE CASCADE，
// 卸载算法会把三个月的历史证据一次性清空，且没有任何提示。
func TestEvidenceSurvivesAlgorithmUninstall(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 1. 让算法产生一条告警，并落库（此时算法名/版本应被快照下来）
	svc := record.NewService(store, zap.NewNop())
	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1234,
		Timestamp:   time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.1, 0.2, 0.5, 0.6}, Label: "smoke", Score: 0.88},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult: %v", err)
	}

	var before record.Alarm
	if err := dbStore.DB().Where("algorithm_id = ?", "algo_01").First(&before).Error; err != nil {
		t.Fatalf("load alarm before uninstall: %v", err)
	}
	if before.AlgorithmName != "FaceReID" {
		t.Fatalf("expected algorithm name snapshot %q, got %q", "FaceReID", before.AlgorithmName)
	}
	if before.AlgorithmVersion != "1.0.0" {
		t.Fatalf("expected algorithm version snapshot '1.0.0', got %q", before.AlgorithmVersion)
	}

	// 2. 卸载算法（等价于 algorithm.Service.DeleteAlgorithm 的最后一步）
	if err := dbStore.DB().Exec(`DELETE FROM algorithms WHERE id = 'algo_01'`).Error; err != nil {
		t.Fatalf("uninstall algorithm: %v", err)
	}

	// 3. 证据必须还在
	var after record.Alarm
	if err := dbStore.DB().Where("id = ?", before.ID).First(&after).Error; err != nil {
		t.Fatalf("alarm disappeared after algorithm uninstall: %v", err)
	}

	// 4. 引用被置空，但快照仍能回答「谁产生的」
	if after.AlgorithmID != nil {
		t.Errorf("expected algorithm_id to be NULL after uninstall, got %q", *after.AlgorithmID)
	}
	if after.AlgorithmName != "FaceReID" || after.AlgorithmVersion != "1.0.0" {
		t.Errorf("algorithm snapshot lost: name=%q version=%q", after.AlgorithmName, after.AlgorithmVersion)
	}

	// 5. 通过公开 API 读出来，快照字段必须可见（前端靠它展示来源）
	resp, err := svc.GetAlarm(ctx, after.ID)
	if err != nil {
		t.Fatalf("GetAlarm: %v", err)
	}
	if resp.AlgorithmID != "" {
		t.Errorf("expected empty algorithmId in API response, got %q", resp.AlgorithmID)
	}
	if resp.AlgorithmName != "FaceReID" {
		t.Errorf("API must expose the algorithm name snapshot, got %q", resp.AlgorithmName)
	}

	// 6. 证据图不能被连带删除（文件是独立资源，DB 级联不该碰它）
	if after.ImagePath == "" {
		t.Error("evidence image path must survive algorithm uninstall")
	}
}
