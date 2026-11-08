package task_test

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/task"
)

func TestStoreCropJSONRoundTrip(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_crop",
		"crop_detector",
		10,
	)

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
