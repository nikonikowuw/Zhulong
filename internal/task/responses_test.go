package task_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/task"
)

func TestTaskToResponse(t *testing.T) {
	t.Run("nil task converts to empty response", func(t *testing.T) {
		var nilTask *task.Task
		resp := nilTask.ToResponse()
		if resp.ID != "" || len(resp.Algorithms) != 0 {
			t.Fatalf("expected empty response for nil task, got %+v", resp)
		}
	})

	t.Run("converts fields and algorithms correctly", func(t *testing.T) {
		now := time.Now().Add(-time.Hour)
		tsk := &task.Task{
			ID:        "task_123",
			CameraID:  "cam_456",
			Enabled:   true,
			Name:      "Helmet Check",
			CreatedAt: now,
			UpdatedAt: now,
			Algorithms: []task.TaskAlgorithm{
				{
					ID:               "sub_789",
					TaskID:           "task_123",
					AlgorithmID:      "algo_helmet",
					Enabled:          true,
					SampleFPS:        10,
					ConfigJSON:       `{"threshold":0.8}`,
					RegionConfigJSON: `{"roi":[]}`,
					CreatedAt:        now,
					UpdatedAt:        now,
				},
			},
		}

		resp := tsk.ToResponse()
		headerMatch := resp.ID == "task_123" &&
			resp.CameraID == "cam_456" &&
			resp.Enabled &&
			resp.Name == "Helmet Check"
		if !headerMatch {
			t.Fatalf("unexpected task header: %+v", resp)
		}
		if len(resp.Algorithms) != 1 {
			t.Fatalf("expected 1 algorithm info, got %d", len(resp.Algorithms))
		}
		algo := resp.Algorithms[0]
		algoMatch := algo.ID == "sub_789" &&
			algo.AlgorithmID == "algo_helmet" &&
			algo.SampleFPS == 10
		if !algoMatch {
			t.Fatalf("unexpected algorithm info: %+v", algo)
		}
		if algo.CreatedAt.Location() != time.UTC || algo.UpdatedAt.Location() != time.UTC {
			t.Fatalf("expected UTC timestamps, got %v / %v", algo.CreatedAt.Location(), algo.UpdatedAt.Location())
		}
	})

	t.Run("empty algorithms serialize to empty json array not null", func(t *testing.T) {
		tsk := &task.Task{
			ID:         "task_empty_algo",
			CameraID:   "cam_1",
			Algorithms: nil,
		}
		resp := tsk.ToResponse()
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("json marshal failed: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("json unmarshal failed: %v", err)
		}
		algos, ok := parsed["algorithms"].([]any)
		if !ok || algos == nil {
			t.Fatalf("expected empty array [] for algorithms, got %v", parsed["algorithms"])
		}
	})
}

func TestToTaskResponses(t *testing.T) {
	t.Run("empty slice returns initialized non-nil slice", func(t *testing.T) {
		responses := task.ToTaskResponses([]task.Task{})
		if responses == nil {
			t.Fatal("expected non-nil responses slice")
		}
		raw, err := json.Marshal(responses)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if string(raw) != "[]" {
			t.Fatalf("expected '[]', got %s", string(raw))
		}
	})

	t.Run("multiple tasks mapping", func(t *testing.T) {
		tasks := []task.Task{
			{ID: "t1", CameraID: "c1"},
			{ID: "t2", CameraID: "c2"},
		}
		responses := task.ToTaskResponses(tasks)
		if len(responses) != 2 {
			t.Fatalf("expected 2 responses, got %d", len(responses))
		}
		if responses[0].ID != "t1" || responses[1].ID != "t2" {
			t.Fatalf("unexpected responses: %+v", responses)
		}
	})
}
