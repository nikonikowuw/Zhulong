package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

func createTestPNG(w, h int, fill color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// loadSampleFacePNG 载入原生测试夹具中的真实人脸样本并转码为 PNG 字节流。
func loadSampleFacePNG(t *testing.T) []byte {
	t.Helper()

	candidates := []string{
		"../../native/tests/fixtures/onnx/face_detect_sample.ppm",
		"../native/tests/fixtures/onnx/face_detect_sample.ppm",
		"native/tests/fixtures/onnx/face_detect_sample.ppm",
	}
	var data []byte
	for _, path := range candidates {
		if d, err := os.ReadFile(path); err == nil {
			data = d
			break
		}
	}
	if data == nil {
		t.Skip("face_detect_sample.ppm fixture not found")
	}

	lines := bytes.SplitN(data, []byte("\n"), 4)
	if len(lines) < 4 || string(lines[0]) != "P6" {
		t.Fatalf("invalid ppm header")
	}
	var w, h, maxVal int
	if _, err := fmt.Sscanf(string(lines[1]), "%d %d", &w, &h); err != nil {
		t.Fatalf("parse ppm dim: %v", err)
	}
	if _, err := fmt.Sscanf(string(lines[2]), "%d", &maxVal); err != nil {
		t.Fatalf("parse ppm max: %v", err)
	}
	pixels := lines[3]
	if len(pixels) < w*h*3 {
		t.Fatalf("truncated ppm payload: got %d bytes, want %d", len(pixels), w*h*3)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (y*w + x) * 3
			img.SetRGBA(x, y, color.RGBA{R: pixels[idx], G: pixels[idx+1], B: pixels[idx+2], A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestExtractFaceFeature_ValidationAndSuccess(t *testing.T) {
	eng := engine.New()
	ctx := context.Background()

	// 1. 空数据应返回 ErrInvalidArgument
	if _, err := eng.ExtractFaceFeature(ctx, nil); !errors.Is(err, engine.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument on nil image data, got %v", err)
	}

	// 2. 损坏数据应返回解图错误
	if _, err := eng.ExtractFaceFeature(ctx, []byte("not-an-image")); !errors.Is(err, engine.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument on invalid image bytes, got %v", err)
	}

	// 3. 超出最大允许尺寸 (单边 > 4096) 应返回 ErrInvalidArgument（Go 侧分辨率防护门限）
	oversizedPNG := createTestPNG(4097, 1, color.White)
	if _, err := eng.ExtractFaceFeature(ctx, oversizedPNG); !errors.Is(err, engine.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument on oversized image dimension, got %v", err)
	}

	// 4. 真实人脸样本：真实推理能力由原生构建的推理后端决定。
	//    仅 CPU 桩后端（ZHULONG_INFERENCE_BACKEND=cpu）或插件缺失时返回 ErrUnsupported，
	//    此时显式跳过真实数值断言，而不是把「能力缺失」误判为测试失败。
	validPNG := loadSampleFacePNG(t)
	features, err := eng.ExtractFaceFeature(ctx, validPNG)
	if errors.Is(err, engine.ErrUnsupported) {
		t.Skipf("native face inference unavailable in this build (stub backend or missing plugin): %v", err)
	}
	if err != nil {
		t.Fatalf("ExtractFaceFeature failed: %v", err)
	}

	if len(features) != 512 {
		t.Fatalf("expected 512 features, got %d", len(features))
	}

	// 校验 L2 归一化 (||v|| ≈ 1.0)
	var normSq float64
	for _, v := range features {
		normSq += float64(v * v)
	}
	if norm := math.Sqrt(normSq); math.Abs(norm-1.0) > 1e-3 {
		t.Errorf("expected unit L2 norm, got %f", norm)
	}

	// 5. 全黑图片应返回 ErrNotFound (未检出人脸)
	blackPNG := createTestPNG(64, 64, color.Black)
	if _, err := eng.ExtractFaceFeature(ctx, blackPNG); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on black image, got %v", err)
	}
}
