package record_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

func TestProcessVehicleStructureCapture(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	// 推理结果：包含 1 辆机动车和 1 块挂载车牌
	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1000,
		Timestamp:   ts,
		TaskType:    aitypes.TaskTypeVehicleStructure,
		Vehicles: []aitypes.VehicleItem{
			{
				TrackID:      108,
				VehicleBox:   [4]float64{0.1, 0.2, 0.6, 0.8},
				VehicleScore: 0.94,
				VehicleType:  "car",
				VehicleColor: "white",
				Plates: []aitypes.PlateSubItem{
					{
						Box:         [4]float64{0.25, 0.55, 0.45, 0.65},
						Score:       0.98,
						PlateNumber: "粤B88888",
						PlateColor:  "蓝色",
						PlateType:   0,
					},
				},
				Attributes: map[string]any{
					"brand": "BYD",
				},
			},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult vehicle_structure failed: %v", err)
	}

	var captures []record.Capture
	if err := dbStore.DB().Where("target_type = ?", "vehicle").Find(&captures).Error; err != nil {
		t.Fatalf("query captures failed: %v", err)
	}

	if len(captures) != 1 {
		t.Fatalf("expected 1 vehicle capture, got %d", len(captures))
	}

	cap := captures[0]
	if cap.CameraID != "cam_01" {
		t.Errorf("expected CameraID 'cam_01', got %q", cap.CameraID)
	}
	if cap.TargetType != "vehicle" {
		t.Errorf("expected TargetType 'vehicle', got %q", cap.TargetType)
	}
	if cap.TrackID != 108 {
		t.Errorf("expected TrackID 108, got %d", cap.TrackID)
	}
	if cap.Score != 0.94 {
		t.Errorf("expected Score 0.94, got %f", cap.Score)
	}

	var attrs map[string]any
	if err := json.Unmarshal([]byte(cap.AttributesJSON), &attrs); err != nil {
		t.Fatalf("parse AttributesJSON failed: %v", err)
	}

	if attrs["plate_number"] != "粤B88888" {
		t.Errorf("expected plate_number '粤B88888', got %v", attrs["plate_number"])
	}
	if attrs["plate_color"] != "蓝色" {
		t.Errorf("expected plate_color '蓝色', got %v", attrs["plate_color"])
	}
	if attrs["vehicle_type"] != "car" {
		t.Errorf("expected vehicle_type 'car', got %v", attrs["vehicle_type"])
	}
	if attrs["brand"] != "BYD" {
		t.Errorf("expected brand 'BYD', got %v", attrs["brand"])
	}
}
