package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// TestCaptureCarriesAlgorithmAttribution 验证两类抓拍都写入算法归属快照。
//
// 背景：captures 曾经连 algorithm_id 列都没有（迁移 000016 才补），导致
// 「某算法误报率高」的排查无法按算法归因 —— 你只能看到一堆抓拍。
func TestCaptureCarriesAlgorithmAttribution(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

	// 1. person_structure：人体抓拍
	if err := svc.ProcessInferenceResult(ctx, aitypes.InferenceResult{
		CameraID: "cam_01", AlgorithmID: "algo_01", PTS: 1, Timestamp: ts,
		TaskType: aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{TrackID: 7, BodyBox: [4]float64{0.1, 0.1, 0.4, 0.9}, BodyScore: 0.9},
		},
	}); err != nil {
		t.Fatalf("process person_structure: %v", err)
	}

	// 2. detection：目标抓拍（非告警标签走 capture 分支）
	if err := svc.ProcessInferenceResult(ctx, aitypes.InferenceResult{
		CameraID: "cam_01", AlgorithmID: "algo_01", PTS: 2, Timestamp: ts,
		TaskType: aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.2, 0.2, 0.5, 0.5}, Label: "car", Score: 0.8},
		},
	}); err != nil {
		t.Fatalf("process detection: %v", err)
	}

	var captures []record.Capture
	if err := dbStore.DB().Order("target_type").Find(&captures).Error; err != nil {
		t.Fatalf("load captures: %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 captures (person + car), got %d", len(captures))
	}

	for _, cap := range captures {
		if cap.AlgorithmID == nil {
			t.Errorf("capture %s (target=%s): algorithm_id must not be NULL", cap.ID, cap.TargetType)
			continue
		}
		if *cap.AlgorithmID != "algo_01" {
			t.Errorf("capture %s: expected algorithm_id 'algo_01', got %q", cap.ID, *cap.AlgorithmID)
		}
		// 快照必须同时写入：只存 id 的话，卸载算法后「谁产生的」就丢了。
		if cap.AlgorithmName != "FaceReID" {
			t.Errorf("capture %s: expected name snapshot 'FaceReID', got %q", cap.ID, cap.AlgorithmName)
		}
		if cap.AlgorithmVersion != "1.0.0" {
			t.Errorf("capture %s: expected version snapshot '1.0.0', got %q", cap.ID, cap.AlgorithmVersion)
		}
	}

	// 3. 公开 API 必须暴露这三个字段
	for _, cap := range captures {
		resp, err := svc.GetCapture(ctx, cap.ID)
		if err != nil {
			t.Fatalf("GetCapture(%s): %v", cap.ID, err)
		}
		if resp.AlgorithmID != "algo_01" {
			t.Errorf("GetCapture(%s).AlgorithmID = %q, want 'algo_01'", cap.ID, resp.AlgorithmID)
		}
		if resp.AlgorithmName != "FaceReID" {
			t.Errorf("GetCapture(%s).AlgorithmName = %q, want 'FaceReID'", cap.ID, resp.AlgorithmName)
		}
	}
}
