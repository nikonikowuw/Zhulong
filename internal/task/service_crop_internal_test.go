package task

import "testing"

// 这些用例盯的是“只改 crop 不重启算法进程”的比较口径。
// 比较函数是内部实现，放在 package task 里直接测，避免为它开导出 API。

func cropSemanticsTask(name string) *Task {
	return &Task{
		CameraID: "cam_1",
		Enabled:  true,
		Name:     name,
		CropJSON: "{}",
		Algorithms: []TaskAlgorithm{
			{
				ID:               "sub_a",
				AlgorithmID:      "algo_a",
				Enabled:          true,
				SampleFPS:        5,
				ConfigJSON:       "{}",
				RegionConfigJSON: "{}",
				ScheduleJSON:     "{}",
			},
			{
				ID:               "sub_b",
				AlgorithmID:      "algo_b",
				Enabled:          false,
				SampleFPS:        2,
				ConfigJSON:       `{"k":1}`,
				RegionConfigJSON: `{"roi":[[0,0,1,1]]}`,
				ScheduleJSON:     "{}",
			},
		},
	}
}

func TestSameTaskSemanticsIgnoresCropAndBookkeeping(t *testing.T) {
	base := cropSemanticsTask("name")
	other := cropSemanticsTask("name")
	other.CropJSON = `{"crop":[0.1,0.2,0.5,0.6]}`
	other.UpdatedAt = base.UpdatedAt.Add(1)
	other.Algorithms[0].ID = "regenerated"
	other.Algorithms[0].CreatedAt = base.CreatedAt
	// Name 是纯展示字段（从不外发给算法包）：改名不该打断正在跑的流。
	other.Name = "renamed"
	// 算法顺序变化不算语义变化（按 AlgorithmID 规范化后比较）。
	other.Algorithms[0], other.Algorithms[1] = other.Algorithms[1], other.Algorithms[0]

	if !sameTaskSemantics(base, other) {
		t.Fatalf("crop-only / bookkeeping / rename changes must not count as semantic changes")
	}
}

func TestSameTaskSemanticsDetectsRealChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{name: "enabled", mutate: func(t *Task) { t.Enabled = false }},
		{name: "camera", mutate: func(t *Task) { t.CameraID = "cam_2" }},
		{
			name: "algorithm added",
			mutate: func(t *Task) {
				t.Algorithms = append(
					t.Algorithms,
					TaskAlgorithm{AlgorithmID: "algo_c", Enabled: true, SampleFPS: 5},
				)
			},
		},
		{name: "algorithm removed", mutate: func(t *Task) { t.Algorithms = t.Algorithms[:1] }},
		{name: "fps", mutate: func(t *Task) { t.Algorithms[0].SampleFPS = 10 }},
		{name: "config", mutate: func(t *Task) { t.Algorithms[0].ConfigJSON = `{"k":2}` }},
		{name: "region", mutate: func(t *Task) { t.Algorithms[0].RegionConfigJSON = `{"roi":[[0,0,1,1]]}` }},
		{name: "schedule", mutate: func(t *Task) { t.Algorithms[0].ScheduleJSON = `{"days":[1]}` }},
		{name: "algorithm disabled", mutate: func(t *Task) { t.Algorithms[0].Enabled = false }},
		{name: "algorithm replaced", mutate: func(t *Task) { t.Algorithms[0].AlgorithmID = "algo_z" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := cropSemanticsTask("name")
			changed := cropSemanticsTask("name")
			tt.mutate(changed)
			if sameTaskSemantics(base, changed) {
				t.Fatalf("expected semantic change to be detected")
			}
		})
	}
}

func TestCloneTaskIsolatesAlgorithms(t *testing.T) {
	original := cropSemanticsTask("name")
	clone := cloneTask(original)
	clone.Algorithms[0].Enabled = false
	clone.Name = "changed"

	if !original.Algorithms[0].Enabled {
		t.Fatalf("clone must not share the algorithms slice")
	}
	if original.Name != "name" {
		t.Fatalf("clone must not share scalar state")
	}
}
