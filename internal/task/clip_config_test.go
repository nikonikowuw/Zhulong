package task_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

func TestParseClipConfig(t *testing.T) {
	defaults := task.DefaultClipConfig()
	tests := []struct {
		name    string
		raw     string
		want    task.ClipConfig
		wantErr bool
	}{
		{name: "empty string", raw: "", want: defaults},
		{name: "blank", raw: "   ", want: defaults},
		{name: "empty object", raw: "{}", want: defaults},
		{name: "null", raw: "null", want: defaults},
		{
			name: "enabled with defaults",
			raw:  `{"enabled":true}`,
			want: task.ClipConfig{Enabled: true, PreSeconds: 5, PostSeconds: 10, RecognitionFilter: "all"},
		},
		{
			name: "full",
			raw:  `{"enabled":true,"preSeconds":15,"postSeconds":60,"recognitionFilter":"stranger"}`,
			want: task.ClipConfig{Enabled: true, PreSeconds: 15, PostSeconds: 60, RecognitionFilter: "stranger"},
		},
		{
			name: "min bounds known",
			raw:  `{"enabled":true,"preSeconds":1,"postSeconds":1,"recognitionFilter":"known"}`,
			want: task.ClipConfig{Enabled: true, PreSeconds: 1, PostSeconds: 1, RecognitionFilter: "known"},
		},
		{
			name: "disabled keeps values",
			raw:  `{"enabled":false,"preSeconds":3}`,
			want: task.ClipConfig{PreSeconds: 3, PostSeconds: 10, RecognitionFilter: "all"},
		},
		{
			name: "empty filter means default",
			raw:  `{"enabled":true,"recognitionFilter":""}`,
			want: task.ClipConfig{Enabled: true, PreSeconds: 5, PostSeconds: 10, RecognitionFilter: "all"},
		},
		{name: "pre zero", raw: `{"preSeconds":0}`, wantErr: true},
		{name: "pre too large", raw: `{"preSeconds":16}`, wantErr: true},
		{name: "post zero", raw: `{"postSeconds":0}`, wantErr: true},
		{name: "post too large", raw: `{"postSeconds":61}`, wantErr: true},
		{name: "bad filter", raw: `{"recognitionFilter":"vip"}`, wantErr: true},
		{name: "unknown key", raw: `{"enable":true}`, wantErr: true},
		{name: "wrong type", raw: `{"enabled":"yes"}`, wantErr: true},
		{name: "not object", raw: `[1]`, wantErr: true},
		{name: "malformed", raw: `{`, wantErr: true},
		{name: "trailing data", raw: `{} {}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := task.ParseClipConfig(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestClipConfigAllowsRecognition(t *testing.T) {
	tests := []struct {
		filter  string
		matched bool
		want    bool
	}{
		{filter: "all", matched: true, want: true},
		{filter: "all", matched: false, want: true},
		{filter: "", matched: false, want: true},
		{filter: "stranger", matched: false, want: true},
		{filter: "stranger", matched: true, want: false},
		{filter: "known", matched: true, want: true},
		{filter: "known", matched: false, want: false},
	}
	for _, tt := range tests {
		cfg := task.ClipConfig{RecognitionFilter: tt.filter}
		if got := cfg.AllowsRecognition(tt.matched); got != tt.want {
			t.Errorf("filter=%q matched=%v: got %v want %v", tt.filter, tt.matched, got, tt.want)
		}
	}
}

func TestServiceRejectsInvalidClipConfig(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.CreateTask(ctx, &task.Task{
		CameraID: "cam_clip",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{{
			AlgorithmID:    "algo_vest",
			Enabled:        true,
			ClipConfigJSON: `{"enabled":true,"preSeconds":99}`,
		}},
	})
	assertAppErrCode(t, err, "INVALID_CLIP_CONFIG")

	ok := &task.Task{
		CameraID: "cam_clip",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{{
			AlgorithmID: "algo_vest",
			Enabled:     true,
		}},
	}
	if err := svc.CreateTask(ctx, ok); err != nil {
		t.Fatalf("create task: %v", err)
	}
	got, err := svc.GetTask(ctx, ok.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Algorithms[0].ClipConfigJSON != "{}" {
		t.Fatalf("empty clip config should normalize to {}, got %q", got.Algorithms[0].ClipConfigJSON)
	}

	// 更新路径同样校验，且合法配置被持久化。
	bad := `{"enabled":true,"recognitionFilter":"nope"}`
	_, err = svc.UpdateTask(ctx, ok.ID, &task.UpdateTaskRequest{
		Algorithms: []task.TaskAlgorithmItem{{AlgorithmID: "algo_vest", ClipConfigJSON: bad}},
	})
	assertAppErrCode(t, err, "INVALID_CLIP_CONFIG")

	_, err = svc.UpdateTask(ctx, ok.ID, &task.UpdateTaskRequest{
		Algorithms: []task.TaskAlgorithmItem{{
			AlgorithmID:       "algo_vest",
			ClipConfigJSONAlt: `{"enabled":true,"postSeconds":20}`,
		}},
	})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	reloaded, err := svc.GetTask(ctx, ok.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if reloaded.Algorithms[0].ClipConfigJSON != `{"enabled":true,"postSeconds":20}` {
		t.Fatalf("clip config not persisted: %q", reloaded.Algorithms[0].ClipConfigJSON)
	}
}

func TestClipPolicyCache(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	insertMockAlgorithm(t, db, "algo_1", "det_1", 10)
	insertMockAlgorithm(t, db, "algo_2", "det_2", 10)
	insertMockAlgorithm(t, db, "algo_3", "det_3", 10)

	cache := task.NewClipPolicyCache(store, zap.NewNop())
	if _, ok := cache.PolicyFor("cam_1", "algo_1"); ok {
		t.Fatalf("expected empty cache initially")
	}
	if _, ok := cache.RecorderFor("cam_1"); ok {
		t.Fatalf("expected no recorder initially")
	}

	var hookCalls atomic.Int32
	cache.SetOnReload(func(context.Context) { hookCalls.Add(1) })

	mustCreate := func(tsk *task.Task) {
		t.Helper()
		if err := store.CreateTask(ctx, tsk); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}
	mustCreate(&task.Task{
		ID: "task_1", CameraID: "cam_1", Enabled: true,
		Algorithms: []task.TaskAlgorithm{
			{ID: "s1", AlgorithmID: "algo_1", Enabled: true, SampleFPS: 5,
				ClipConfigJSON: `{"enabled":true,"preSeconds":3,"postSeconds":7}`},
			{ID: "s2", AlgorithmID: "algo_2", Enabled: true, SampleFPS: 5,
				ClipConfigJSON: `{"enabled":true,"preSeconds":8,"recognitionFilter":"known"}`},
			// 算法停用：不进缓存，也不参与前段最大值。
			{ID: "s3", AlgorithmID: "algo_3", Enabled: false, SampleFPS: 5,
				ClipConfigJSON: `{"enabled":true,"preSeconds":15}`},
		},
	})
	mustCreate(&task.Task{
		ID: "task_2", CameraID: "cam_2", Enabled: true,
		Algorithms: []task.TaskAlgorithm{
			// 录像关闭。
			{ID: "s4", AlgorithmID: "algo_1", Enabled: true, SampleFPS: 5, ClipConfigJSON: `{}`},
			// 非法配置：跳过不影响其余项。
			{ID: "s5", AlgorithmID: "algo_2", Enabled: true, SampleFPS: 5, ClipConfigJSON: `{"preSeconds":99}`},
		},
	})
	mustCreate(&task.Task{
		ID: "task_3", CameraID: "cam_3", Enabled: false,
		Algorithms: []task.TaskAlgorithm{
			{ID: "s6", AlgorithmID: "algo_1", Enabled: true, SampleFPS: 5, ClipConfigJSON: `{"enabled":true}`},
		},
	})

	cache.OnTaskChanged(ctx, "algo_1")
	if hookCalls.Load() != 1 {
		t.Fatalf("reload hook should fire once, got %d", hookCalls.Load())
	}

	p1, ok := cache.PolicyFor("cam_1", "algo_1")
	if !ok || p1.PreSeconds != 3 || p1.PostSeconds != 7 || p1.RecognitionFilter != "all" {
		t.Fatalf("unexpected cam_1/algo_1 policy %+v ok=%v", p1, ok)
	}
	p2, ok := cache.PolicyFor("cam_1", "algo_2")
	if !ok || p2.RecognitionFilter != "known" || p2.PostSeconds != 10 {
		t.Fatalf("unexpected cam_1/algo_2 policy %+v ok=%v", p2, ok)
	}
	if _, ok := cache.PolicyFor("cam_1", "algo_3"); ok {
		t.Fatalf("disabled algorithm must not be cached")
	}
	if pre, ok := cache.RecorderFor("cam_1"); !ok || pre != 8000 {
		t.Fatalf("cam_1 recorder pre = %d ok=%v, want 8000 true", pre, ok)
	}
	for _, cam := range []string{"cam_2", "cam_3", "cam_none"} {
		if pre, ok := cache.RecorderFor(cam); ok {
			t.Fatalf("%s must not record, got pre=%d", cam, pre)
		}
	}
	if _, ok := cache.PolicyFor("cam_2", "algo_1"); ok {
		t.Fatalf("clip-disabled policy must not be cached")
	}

	// 删除任务后全量重建：cam_1 不再录像。
	if err := store.DeleteTask(ctx, "task_1"); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cache.RecorderFor("cam_1"); ok {
		t.Fatalf("cam_1 recorder should be released after task deletion")
	}
	if hookCalls.Load() != 2 {
		t.Fatalf("hook should fire on every reload")
	}

	cache.SetOnReload(nil)
	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if hookCalls.Load() != 2 {
		t.Fatalf("cleared hook must not fire")
	}
}

func TestClipPolicyCacheNilStore(t *testing.T) {
	cache := task.NewClipPolicyCache(nil, nil)
	if err := cache.Reload(context.Background()); err != nil {
		t.Fatalf("nil store reload should be a no-op: %v", err)
	}
	if _, ok := cache.RecorderFor("cam"); ok {
		t.Fatalf("expected no recorder")
	}
}
