package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/region"
	"github.com/nikonikowuw/Zhulong/internal/task"
)

func mustCrop(t *testing.T, raw string) *region.Rect {
	t.Helper()
	crop, err := region.ParseCrop(raw)
	if err != nil {
		t.Fatalf("parse crop %q: %v", raw, err)
	}
	return crop
}

func TestCreateTaskRequestToTaskCrop(t *testing.T) {
	rect := mustCrop(t, `{"crop":[0.1,0.2,0.5,0.6]}`)
	identity := region.Identity()

	tests := []struct {
		name string
		crop *region.Rect
		want string
	}{
		{name: "nil keeps empty object", crop: nil, want: "{}"},
		{name: "identity normalizes to empty object", crop: &identity, want: "{}"},
		{name: "rect serializes", crop: rect, want: `{"crop":[0.1,0.2,0.5,0.6]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &task.CreateTaskRequest{CameraID: "cam", Crop: tt.crop}
			got, err := req.ToTask()
			if err != nil {
				t.Fatalf("ToTask failed: %v", err)
			}
			if got.CropJSON != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got.CropJSON)
			}
		})
	}
}

func TestCreateTaskRequestToTaskRejectsNonFiniteCrop(t *testing.T) {
	crop := region.Rect{math.NaN(), 0.2, 0.5, 0.6}
	_, err := (&task.CreateTaskRequest{CameraID: "cam", Crop: &crop}).ToTask()
	if err == nil {
		t.Fatal("expected non-finite crop to be rejected")
	}

	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected apperr.Error, got %T", err)
	}
	if appErr.Code != "INVALID_CROP_CONFIG" {
		t.Fatalf("expected INVALID_CROP_CONFIG, got %s", appErr.Code)
	}
}

func TestUpdateTaskRequestApplyCrop(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "omitted keeps existing", raw: "", want: `{"crop":[0.1,0.2,0.5,0.6]}`},
		{name: "null clears", raw: "null", want: "{}"},
		{name: "empty array clears", raw: "[]", want: "{}"},
		{name: "empty object clears", raw: "{}", want: "{}"},
		{name: "array sets", raw: `[0.2,0.2,0.8,0.8]`, want: `{"crop":[0.2,0.2,0.8,0.8]}`},
		{name: "identity normalizes", raw: `[0,0,1,1]`, want: "{}"},
		{name: "storage shape accepted", raw: `{"crop":[0.2,0.2,0.8,0.8]}`, want: `{"crop":[0.2,0.2,0.8,0.8]}`},
		{name: "wrong length rejected", raw: `[0.2,0.2,0.8]`, wantErr: true},
		{name: "degenerate rejected", raw: `[0.5,0.2,0.5,0.8]`, wantErr: true},
		{name: "too small rejected", raw: `[0.1,0.1,0.105,0.2]`, wantErr: true},
		{name: "scalar rejected", raw: `42`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entity := &task.Task{CropJSON: `{"crop":[0.1,0.2,0.5,0.6]}`}
			req := &task.UpdateTaskRequest{}
			if tt.raw != "" {
				req.Crop = json.RawMessage(tt.raw)
			}
			err := req.ApplyCrop(entity)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got crop %q", entity.CropJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if entity.CropJSON != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, entity.CropJSON)
			}
		})
	}
}

func TestStoreCropJSONRoundTrip(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_crop", "crop_detector", 10)

	entity := &task.Task{
		CameraID: "cam_crop",
		Enabled:  true,
		Name:     "before",
		CropJSON: `{"crop":[0.1,0.2,0.5,0.6]}`,
		Algorithms: []task.TaskAlgorithm{{
			AlgorithmID:      "algo_crop",
			Enabled:          true,
			SampleFPS:        5,
			ConfigJSON:       "{}",
			RegionConfigJSON: "{}",
			ScheduleJSON:     "{}",
		}},
	}
	if err := store.CreateTask(ctx, entity); err != nil {
		t.Fatalf("create task: %v", err)
	}

	loaded, err := store.GetTask(ctx, entity.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if loaded.CropJSON != `{"crop":[0.1,0.2,0.5,0.6]}` {
		t.Fatalf("crop not persisted on insert: %q", loaded.CropJSON)
	}

	// 只改 name：更新走 writeTaskRow 的列 map，crop_json 必须原样保留而不是被零值覆盖。
	loaded.Name = "after"
	if err := store.SaveTaskWithinBudget(ctx, loaded, task.MaxComputePoints); err != nil {
		t.Fatalf("save task: %v", err)
	}
	again, err := store.GetTask(ctx, entity.ID)
	if err != nil {
		t.Fatalf("get task after update: %v", err)
	}
	if again.CropJSON != `{"crop":[0.1,0.2,0.5,0.6]}` {
		t.Fatalf("crop lost on update: %q", again.CropJSON)
	}

	again.CropJSON = "{}"
	if err := store.SaveTaskWithinBudget(ctx, again, task.MaxComputePoints); err != nil {
		t.Fatalf("clear crop: %v", err)
	}
	cleared, err := store.GetTask(ctx, entity.ID)
	if err != nil {
		t.Fatalf("get cleared task: %v", err)
	}
	if cleared.CropJSON != "{}" {
		t.Fatalf("expected cleared crop, got %q", cleared.CropJSON)
	}
}
