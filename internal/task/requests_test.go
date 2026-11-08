package task

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/region"
)

func boolPtr(v bool) *bool {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

func mustCrop(t *testing.T, raw string) *region.Rect {
	t.Helper()
	crop, err := region.ParseCrop(raw)
	if err != nil {
		t.Fatalf("parse crop %q: %v", raw, err)
	}
	return crop
}

func TestCreateTaskRequestToTask(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateTaskRequest
		wantCam string
		check   func(t *testing.T, got *Task)
	}{
		{
			name:    "camelCase fields",
			req:     CreateTaskRequest{CameraID: "cam_camel", Enabled: boolPtr(true)},
			wantCam: "cam_camel",
			check: func(t *testing.T, got *Task) {
				if !got.Enabled {
					t.Error("expected Enabled=true")
				}
			},
		},
		{
			name:    "snake_case camera alias",
			req:     CreateTaskRequest{CameraIDAlt: "cam_snake"},
			wantCam: "cam_snake",
			check: func(t *testing.T, got *Task) {
				// 未显式给出 enabled 时默认开播。
				if !got.Enabled {
					t.Error("expected Enabled to default to true")
				}
			},
		},
		{
			// 这条盯的是 GORM 零值回弹那个坑的入口：显式 false 必须原样穿过 *bool。
			name:    "explicit enabled=false",
			req:     CreateTaskRequest{CameraID: "cam_off", Enabled: boolPtr(false)},
			wantCam: "cam_off",
			check: func(t *testing.T, got *Task) {
				if got.Enabled {
					t.Error("expected Enabled=false to survive the *bool deref")
				}
			},
		},
		{
			name: "legacy flat single algorithm",
			req: CreateTaskRequest{
				CameraID:    "cam_legacy_flat",
				AlgorithmID: "algo_flat",
				SampleFPS:   12,
				ConfigJSON:  `{"threshold": 0.7}`,
			},
			wantCam: "cam_legacy_flat",
			check: func(t *testing.T, got *Task) {
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
			req: CreateTaskRequest{
				CameraID: "cam_multi",
				Algorithms: []TaskAlgorithmItem{
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
			check: func(t *testing.T, got *Task) {
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
				if second.SampleFPS != DefaultSampleFPS {
					t.Errorf("expected default fps %d, got %d", DefaultSampleFPS, second.SampleFPS)
				}
				if second.ConfigJSON != "{}" || second.RegionConfigJSON != "{}" {
					t.Errorf("expected empty json docs, got %q / %q", second.ConfigJSON, second.RegionConfigJSON)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.req.toTask()
			if err != nil {
				t.Fatalf("toTask failed: %v", err)
			}
			if got.CameraID != test.wantCam {
				t.Fatalf("expected camera %q, got %q", test.wantCam, got.CameraID)
			}
			test.check(t, got)
		})
	}
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
			req := &CreateTaskRequest{CameraID: "cam", Crop: tt.crop}
			got, err := req.toTask()
			if err != nil {
				t.Fatalf("toTask failed: %v", err)
			}
			if got.CropJSON != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got.CropJSON)
			}
		})
	}
}

func TestCreateTaskRequestToTaskRejectsNonFiniteCrop(t *testing.T) {
	crop := region.Rect{math.NaN(), 0.2, 0.5, 0.6}
	_, err := (&CreateTaskRequest{CameraID: "cam", Crop: &crop}).toTask()
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

func TestUpdateTaskRequestApply(t *testing.T) {
	baseTask := func() *Task {
		return &Task{
			ID:       "task_apply",
			CameraID: "cam_apply",
			Enabled:  true,
			Name:     "original",
			CropJSON: `{"crop":[0.1,0.2,0.5,0.6]}`,
			Algorithms: []TaskAlgorithm{
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
		req := &UpdateTaskRequest{Name: stringPtr("renamed")}
		if err := req.apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

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

	t.Run("explicit empty name clears task name", func(t *testing.T) {
		tsk := baseTask()
		req := &UpdateTaskRequest{Name: stringPtr("")}
		if err := req.apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

		if tsk.Name != "" {
			t.Errorf("expected name to be cleared to empty string, got %q", tsk.Name)
		}
	})

	t.Run("nil name leaves task name untouched", func(t *testing.T) {
		tsk := baseTask()
		req := &UpdateTaskRequest{Name: nil}
		if err := req.apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

		if tsk.Name != "original" {
			t.Errorf("expected name to stay original, got %q", tsk.Name)
		}
	})

	t.Run("explicit enabled=false", func(t *testing.T) {
		tsk := baseTask()
		if err := (&UpdateTaskRequest{Enabled: boolPtr(false)}).apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

		if tsk.Enabled {
			t.Error("expected Enabled=false")
		}
		if tsk.Name != "original" {
			t.Errorf("expected name to stay original, got %q", tsk.Name)
		}
	})

	t.Run("algorithms replace the set", func(t *testing.T) {
		tsk := baseTask()
		items := []TaskAlgorithmItem{{AlgorithmID: "algo_z", SampleFPS: 9}}
		if err := (&UpdateTaskRequest{Algorithms: items}).apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

		if len(tsk.Algorithms) != 1 || tsk.Algorithms[0].AlgorithmID != "algo_z" {
			t.Fatalf("expected algorithm set to be replaced, got %+v", tsk.Algorithms)
		}
		if tsk.Algorithms[0].SampleFPS != 9 {
			t.Errorf("expected fps 9, got %d", tsk.Algorithms[0].SampleFPS)
		}
	})

	t.Run("legacy flat fields patch the single algorithm", func(t *testing.T) {
		tsk := baseTask()
		req := &UpdateTaskRequest{
			SampleFPS:        11,
			RegionConfigJSON: `{"roi": [[0.2, 0.2, 0.6, 0.6]]}`,
		}
		if err := req.apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

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
		tsk.Algorithms = append(tsk.Algorithms, TaskAlgorithm{AlgorithmID: "algo_b", SampleFPS: 5})
		if err := (&UpdateTaskRequest{SampleFPS: 30}).apply(tsk); err != nil {
			t.Fatalf("apply failed: %v", err)
		}

		for _, sub := range tsk.Algorithms {
			if sub.SampleFPS != 5 {
				t.Errorf("flat fps must not leak into multi-algorithm task, got %+v", sub)
			}
		}
	})

	t.Run("invalid crop leaves target task untouched", func(t *testing.T) {
		tsk := baseTask()
		req := &UpdateTaskRequest{
			Name: stringPtr("mutated"),
			Crop: json.RawMessage(`[0.5,0.2,0.5,0.8]`),
		}
		if err := req.apply(tsk); err == nil {
			t.Fatal("expected invalid crop error")
		}
		if tsk.Name != "original" {
			t.Fatalf("expected Name to stay original on rejected crop, got %q", tsk.Name)
		}
	})

	t.Run("nil task is ignored", func(t *testing.T) {
		if err := (&UpdateTaskRequest{Name: stringPtr("x")}).apply(nil); err != nil {
			t.Fatalf("expected nil error for nil task, got %v", err)
		}
	})
}

func TestUpdateTaskRequestCropMerge(t *testing.T) {
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
		{
			name: "storage shape accepted",
			raw:  `{"crop":[0.2,0.2,0.8,0.8]}`,
			want: `{"crop":[0.2,0.2,0.8,0.8]}`,
		},
		{name: "wrong length rejected", raw: `[0.2,0.2,0.8]`, wantErr: true},
		{name: "degenerate rejected", raw: `[0.5,0.2,0.5,0.8]`, wantErr: true},
		{name: "too small rejected", raw: `[0.1,0.1,0.105,0.2]`, wantErr: true},
		{name: "scalar rejected", raw: `42`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entity := &Task{CropJSON: `{"crop":[0.1,0.2,0.5,0.6]}`}
			req := &UpdateTaskRequest{}
			if tt.raw != "" {
				req.Crop = json.RawMessage(tt.raw)
			}
			err := req.apply(entity)
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
