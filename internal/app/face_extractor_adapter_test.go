package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/person"
)

// blackPNG 生成一张纯黑图片：必然检测不到人脸。
func blackPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.Black)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestFaceFeatureExtractorAdapter_MapsInputFailuresToDomainSentinels 锁定适配器的错误翻译契约：
// 上传输入本身的问题（解不开 / 没有脸）必须变成 person 包的哨兵，让 HTTP 层回答 4xx；
// 其余错误一律透传，避免把服务端故障伪装成「照片有问题」。
//
// 这是 internal/person 与 internal/engine 之间唯一的翻译点，它一旦漏掉某个哨兵，
// 客户端就会收到 500。
func TestFaceFeatureExtractorAdapter_MapsInputFailuresToDomainSentinels(t *testing.T) {
	adapter := &faceFeatureExtractorAdapter{eng: engine.New()}
	ctx := context.Background()

	t.Run("undecodable bytes map to ErrUnsupportedImageFormat", func(t *testing.T) {
		_, err := adapter.ExtractFaceFeature(ctx, []byte("not-an-image"))
		if !errors.Is(err, person.ErrUnsupportedImageFormat) {
			t.Fatalf("want person.ErrUnsupportedImageFormat, got %v", err)
		}
	})

	t.Run("no detectable face maps to ErrNoFaceDetected", func(t *testing.T) {
		_, err := adapter.ExtractFaceFeature(ctx, blackPNG(t))
		if errors.Is(err, engine.ErrUnsupported) {
			t.Skipf("native face inference unavailable in this build (stub backend or missing plugin): %v", err)
		}
		if !errors.Is(err, person.ErrNoFaceDetected) {
			t.Fatalf("want person.ErrNoFaceDetected, got %v", err)
		}
	})

	t.Run("webp passes through as a real extraction", func(t *testing.T) {
		data, err := os.ReadFile("../engine/testdata/face_sample.webp")
		if err != nil {
			t.Fatalf("read webp fixture: %v", err)
		}

		features, err := adapter.ExtractFaceFeature(ctx, data)
		if errors.Is(err, person.ErrUnsupportedImageFormat) {
			t.Fatalf("WebP must be decodable through the adapter, got %v", err)
		}
		if errors.Is(err, engine.ErrUnsupported) {
			t.Skipf("native face inference unavailable in this build (stub backend or missing plugin): %v", err)
		}
		if err != nil {
			t.Fatalf("ExtractFaceFeature failed: %v", err)
		}
		if len(features) != 512 {
			t.Fatalf("want 512 features, got %d", len(features))
		}
	})
}
