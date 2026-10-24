package task_test

import (
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/task"
)

// boolPtr 返回指向 v 的指针，用于构造 *bool 形式的显式开关。
func boolPtr(v bool) *bool {
	return &v
}

func TestCreateTaskRequestToTask(t *testing.T) {
	tests := []struct {
		name     string
		req      task.CreateTaskRequest
		wantCam  string
		wantTask string
		check    func(t *testing.T, got *task.Task)
	}{
		{
			name:    "camelCase fields",
			req:     task.CreateTaskRequest{CameraID: "cam_camel", Enabled: boolPtr(true)},
			wantCam: "cam_camel",
			check: func(t *testing.T, got *task.Task) {
				if !got.Enabled {
					t.Error("expected Enabled=true")
				}
			},
		},
		{
			name:    "snake_case camera alias",
			req:     task.CreateTaskRequest{CameraIDAlt: "cam_snake"},
			wantCam: "cam_snake",
			check: func(t *testing.T, got *task.Task) {
				// 未显式给出 enabled 时默认开播。
				if !got.Enabled {
					t.Error("expected Enabled to default to true")
				}
			},
		},
		{
			// 这条盯的是 GORM 零值回弹那个坑的入口：显式 false 必须原样穿过 *bool。
			name:    "explicit enabled=false",
			req:     task.CreateTaskRequest{CameraID: "cam_off", Enabled: boolPtr(false)},
			wantCam: "cam_off",
			check: func(t *testing.T, got *task.Task) {
				if got.Enabled {
					t.Error("expected Enabled=false to survive the *bool deref")
				}
			},
		},
		{
			name: "legacy flat single algorithm",
			req: task.CreateTaskRequest{
				CameraID:    "cam_legacy_flat",
				AlgorithmID: "algo_flat",
				SampleFPS:   12,
				ConfigJSON:  `{"threshold": 0.7}`,
			},
			wantCam: "cam_legacy_flat",
			check: func(t *testing.T, got *task.Task) {
				if len(got.Algorithms) != 1 {
					t.Fatalf("expected 1 algorithm, got %d", len(got.Algorithms))
				}
				sub := got.Algorithms[0]
				if sub.AlgorithmID != "algo_flat" || sub.SampleFPS != 12 {
					t.Errorf("unexpected algorithm item: %+v", sub)
				}
				if sub.ConfigJSON != `{"threshold": 0.7}` {
					t.Errorf("unexpected config json: %s", sub.ConfigJSON)
				}
			},
		},
		{
			name: "multi algorithm items with snake_case aliases",
			req: task.CreateTaskRequest{
				CameraID: "cam_multi",
				Algorithms: []task.TaskAlgorithmItem{
					{
						AlgorithmIDAlt:      "algo_a",
						Enabled:             boolPtr(false),
						SampleFPSAlt:        7,
						ConfigJSONAlt:       `{"a": 1}`,
						RegionConfigJSONAlt: `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`,
					},
					{AlgorithmID: "algo_b"},
				},
			},
			wantCam: "cam_multi",
			check: func(t *testing.T, got *task.Task) {
				if len(got.Algorithms) != 2 {
					t.Fatalf("expected 2 algorithms, got %d", len(got.Algorithms))
				}
				first := got.Algorithms[0]
				if first.AlgorithmID != "algo_a" || first.SampleFPS != 7 {
					t.Errorf("unexpected first algorithm: %+v", first)
				}
				if first.Enabled {
					t.Error("expected first algorithm Enabled=false")
				}
				if first.ConfigJSON != `{"a": 1}` {
					t.Errorf("unexpected first config json: %s", first.ConfigJSON)
				}
				if first.RegionConfigJSON != `{"roi": [[0.1, 0.1, 0.5, 0.5]]}` {
					t.Errorf("unexpected first region config: %s", first.RegionConfigJSON)
				}
				// 缺省值必须收敛到 DefaultSampleFPS / "{}"。
				second := got.Algorithms[1]
				if second.SampleFPS != task.DefaultSampleFPS {
					t.Errorf("expected default fps %d, got %d", task.DefaultSampleFPS, second.SampleFPS)
				}
				if second.ConfigJSON != "{}" || second.RegionConfigJSON != "{}" {
					t.Errorf("expected empty json docs, got %q / %q", second.ConfigJSON, second.RegionConfigJSON)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.req.ToTask()
			if got.CameraID != test.wantCam {
				t.Fatalf("expected camera %q, got %q", test.wantCam, got.CameraID)
			}
			test.check(t, got)
		})
	}
}

func TestUpdateTaskRequestApplyTo(t *testing.T) {
	baseTask := func() *task.Task {
		return &task.Task{
			ID:       "task_apply",
			CameraID: "cam_apply",
			Enabled:  true,
			Name:     "original",
			Algorithms: []task.TaskAlgorithm{
				{
					AlgorithmID:      "algo_a",
					Enabled:          true,
					SampleFPS:        5,
					ConfigJSON:       "{}",
					RegionConfigJSON: "{}",
				},
			},
		}
	}

	t.Run("name only leaves everything else intact", func(t *testing.T) {
		tsk := baseTask()
		req := &task.UpdateTaskRequest{Name: "renamed"}
		req.ApplyTo(tsk)

		if tsk.Name != "renamed" {
			t.Errorf("expected name renamed, got %q", tsk.Name)
		}
		if !tsk.Enabled {
			t.Error("expected Enabled to stay true")
		}
		if len(tsk.Algorithms) != 1 || tsk.Algorithms[0].SampleFPS != 5 {
			t.Errorf("algorithms must be untouched, got %+v", tsk.Algorithms)
		}
	})

	t.Run("explicit enabled=false", func(t *testing.T) {
		tsk := baseTask()
		(&task.UpdateTaskRequest{Enabled: boolPtr(false)}).ApplyTo(tsk)

		if tsk.Enabled {
			t.Error("expected Enabled=false")
		}
		if tsk.Name != "original" {
			t.Errorf("expected name to stay original, got %q", tsk.Name)
		}
	})

	t.Run("algorithms replace the set", func(t *testing.T) {
		tsk := baseTask()
		items := []task.TaskAlgorithmItem{{AlgorithmID: "algo_z", SampleFPS: 9}}
		(&task.UpdateTaskRequest{Algorithms: &items}).ApplyTo(tsk)

		if len(tsk.Algorithms) != 1 || tsk.Algorithms[0].AlgorithmID != "algo_z" {
			t.Fatalf("expected algorithm set to be replaced, got %+v", tsk.Algorithms)
		}
		if tsk.Algorithms[0].SampleFPS != 9 {
			t.Errorf("expected fps 9, got %d", tsk.Algorithms[0].SampleFPS)
		}
	})

	t.Run("legacy flat fields patch the single algorithm", func(t *testing.T) {
		tsk := baseTask()
		req := &task.UpdateTaskRequest{
			SampleFPS:        11,
			RegionConfigJSON: `{"roi": [[0.2, 0.2, 0.6, 0.6]]}`,
		}
		req.ApplyTo(tsk)

		sub := tsk.Algorithms[0]
		if sub.SampleFPS != 11 {
			t.Errorf("expected fps 11, got %d", sub.SampleFPS)
		}
		if sub.RegionConfigJSON != `{"roi": [[0.2, 0.2, 0.6, 0.6]]}` {
			t.Errorf("unexpected region config: %s", sub.RegionConfigJSON)
		}
		if sub.ConfigJSON != "{}" {
			t.Errorf("expected config json untouched, got %s", sub.ConfigJSON)
		}
	})

	t.Run("legacy flat fields skip multi algorithm tasks", func(t *testing.T) {
		tsk := baseTask()
		tsk.Algorithms = append(tsk.Algorithms, task.TaskAlgorithm{AlgorithmID: "algo_b", SampleFPS: 5})
		(&task.UpdateTaskRequest{SampleFPS: 30}).ApplyTo(tsk)

		for _, sub := range tsk.Algorithms {
			if sub.SampleFPS != 5 {
				t.Errorf("flat fps must not leak into multi-algorithm task, got %+v", sub)
			}
		}
	})

	t.Run("nil task is ignored", func(t *testing.T) {
		(&task.UpdateTaskRequest{Name: "x"}).ApplyTo(nil)
	})
}
