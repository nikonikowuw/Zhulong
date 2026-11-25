package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/camera"
)

type stubAlertCameraReader struct {
	names map[string]string
	calls *atomic.Int32
}

func (s stubAlertCameraReader) Get(_ context.Context, id string) (*camera.CameraResponse, error) {
	if s.calls != nil {
		s.calls.Add(1)
	}
	name, ok := s.names[id]
	if !ok {
		return nil, errors.New("camera not found")
	}
	return &camera.CameraResponse{ID: id, Name: name}, nil
}

type stubAlertAlgorithmReader struct {
	algos map[string]*algorithm.Algorithm
	calls *atomic.Int32
}

func (s stubAlertAlgorithmReader) GetAlgorithm(_ context.Context, id string) (*algorithm.Algorithm, error) {
	if s.calls != nil {
		s.calls.Add(1)
	}
	algo, ok := s.algos[id]
	if !ok {
		return nil, errors.New("algorithm not found")
	}
	return algo, nil
}

func TestAlertSourceResolver(t *testing.T) {
	resolver := &alertSourceResolver{
		cameras: stubAlertCameraReader{names: map[string]string{"cam_1": "东门入口"}},
		algorithms: stubAlertAlgorithmReader{algos: map[string]*algorithm.Algorithm{
			"ppe_helmet": {ID: "ppe_helmet", Name: "安全帽检测", Version: "1.1.0"},
		}},
		logger: zap.NewNop(),
	}

	tests := []struct {
		name        string
		cameraID    string
		algorithmID string
		wantCamera  string
		wantAlgo    bool
		wantVersion string
	}{
		{name: "both found", cameraID: "cam_1", algorithmID: "ppe_helmet", wantCamera: "东门入口", wantAlgo: true, wantVersion: "1.1.0"},
		{name: "camera missing, algorithm missing", cameraID: "cam_x", algorithmID: "unknown", wantAlgo: true},
		{name: "empty algorithm id", cameraID: "cam_1", algorithmID: "", wantCamera: "东门入口"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := resolver.ResolveSource(context.Background(), tt.cameraID, tt.algorithmID)
			if src.Camera == nil || src.Camera.ID != tt.cameraID {
				t.Fatalf("camera = %+v", src.Camera)
			}
			if src.Camera.Name != tt.wantCamera {
				t.Fatalf("camera name = %q, want %q", src.Camera.Name, tt.wantCamera)
			}
			if (src.Algorithm != nil) != tt.wantAlgo {
				t.Fatalf("algorithm = %+v, want present=%v", src.Algorithm, tt.wantAlgo)
			}
			if !tt.wantAlgo {
				return
			}
			if src.Algorithm.ID != tt.algorithmID {
				t.Fatalf("algorithm id = %q", src.Algorithm.ID)
			}
			if src.Algorithm.Version != tt.wantVersion {
				t.Fatalf("algorithm version = %q, want %q", src.Algorithm.Version, tt.wantVersion)
			}
		})
	}
}

func TestAlertSourceResolver_NilDependencies(t *testing.T) {
	resolver := newAlertSourceResolver(nil, nil, nil)
	src := resolver.ResolveSource(context.Background(), "cam_1", "algo_1")
	if src.Camera == nil || src.Camera.ID != "cam_1" || src.Camera.Name != "" {
		t.Fatalf("camera = %+v", src.Camera)
	}
	if src.Algorithm == nil || src.Algorithm.ID != "algo_1" || src.Algorithm.Name != "" || src.Algorithm.Version != "" {
		t.Fatalf("algorithm = %+v", src.Algorithm)
	}
}

func TestAlertSourceResolver_CachesWithTTLAndSkipsErrors(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	var camCalls, algoCalls atomic.Int32

	camReader := stubAlertCameraReader{
		names: map[string]string{"cam_1": "东门入口"},
		calls: &camCalls,
	}
	algoReader := stubAlertAlgorithmReader{
		algos: map[string]*algorithm.Algorithm{
			"ppe_helmet": {ID: "ppe_helmet", Name: "安全帽检测", Version: "1.1.0"},
		},
		calls: &algoCalls,
	}
	resolver := &alertSourceResolver{
		cameras:    camReader,
		algorithms: algoReader,
		logger:     zap.NewNop(),
		cacheTTL:   10 * time.Second,
		now:        func() time.Time { return now },
	}

	// First lookup populates cache; repeated lookups within TTL must hit cache.
	for range 4 {
		src := resolver.ResolveSource(context.Background(), "cam_1", "ppe_helmet")
		if src.Camera.Name != "东门入口" || src.Algorithm.Version != "1.1.0" {
			t.Fatalf("unexpected source: %+v", src)
		}
	}
	if camCalls.Load() != 1 || algoCalls.Load() != 1 {
		t.Fatalf("expected 1 lookup each within TTL, got cam=%d algo=%d", camCalls.Load(), algoCalls.Load())
	}

	// Missing entries are not cached so newly added records resolve immediately.
	_ = resolver.ResolveSource(context.Background(), "cam_missing", "algo_missing")
	_ = resolver.ResolveSource(context.Background(), "cam_missing", "algo_missing")
	if camCalls.Load() != 3 || algoCalls.Load() != 3 {
		t.Fatalf("expected failed lookups not cached, got cam=%d algo=%d", camCalls.Load(), algoCalls.Load())
	}

	// After TTL expires, updated metadata is refreshed.
	camReader.names["cam_1"] = "西门入口"
	algoReader.algos["ppe_helmet"] = &algorithm.Algorithm{ID: "ppe_helmet", Name: "安全帽检测", Version: "1.2.0"}
	now = now.Add(11 * time.Second)

	refreshed := resolver.ResolveSource(context.Background(), "cam_1", "ppe_helmet")
	if refreshed.Camera.Name != "西门入口" || refreshed.Algorithm.Version != "1.2.0" {
		t.Fatalf("expected refreshed source after TTL, got %+v", refreshed)
	}
}
