package app

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/camera"
)

type stubAlertCameraReader struct {
	names map[string]string
}

func (s stubAlertCameraReader) Get(_ context.Context, id string) (*camera.CameraResponse, error) {
	name, ok := s.names[id]
	if !ok {
		return nil, errors.New("camera not found")
	}
	return &camera.CameraResponse{ID: id, Name: name}, nil
}

type stubAlertAlgorithmReader struct {
	algos map[string]*algorithm.Algorithm
}

func (s stubAlertAlgorithmReader) GetAlgorithm(_ context.Context, id string) (*algorithm.Algorithm, error) {
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
