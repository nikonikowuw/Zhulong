package engine_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
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

func TestExtractFaceFeature_ValidationAndSuccess(t *testing.T) {
	eng := engine.New()
	ctx := context.Background()

	// 1. 空数据应返回 ErrInvalidArgument
	_, err := eng.ExtractFaceFeature(ctx, nil)
	if err == nil {
		t.Fatal("expected error on nil image data")
	}

	// 2. 损坏数据应返回解图错误
	_, err = eng.ExtractFaceFeature(ctx, []byte("not-an-image"))
	if err == nil {
		t.Fatal("expected error on invalid image bytes")
	}

	// 3. 全黑图片应返回 ErrNotFound (未检出人脸)
	blackPNG := createTestPNG(64, 64, color.Black)
	_, err = eng.ExtractFaceFeature(ctx, blackPNG)
	if err == nil {
		t.Fatal("expected ErrNotFound on black image")
	}

	// 4. 有效图像应返回 512 维归一化向量
	validPNG := createTestPNG(64, 64, color.RGBA{R: 200, G: 150, B: 120, A: 255})
	features, err := eng.ExtractFaceFeature(ctx, validPNG)
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
	norm := math.Sqrt(normSq)
	if math.Abs(norm-1.0) > 1e-3 {
		t.Errorf("expected unit L2 norm, got %f", norm)
	}
}
