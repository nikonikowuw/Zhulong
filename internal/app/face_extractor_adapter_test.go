package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/faceextract"
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

// stubProvider writes a fake package entry that answers every frame with the given
// JSON body. The adapter test therefore exercises the *translation* layer (decode →
// frame → error mapping) without needing models, ONNX Runtime, or a real package.
func stubProvider(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stub_provider")
	script := `#!/usr/bin/env python3
import json, struct, sys

template = ` + strconv.Quote(body) + `

while True:
    header = sys.stdin.buffer.read(28)
    if len(header) < 28:
        break
    magic, request_id, w, h, stride, fmt, payload_len = struct.unpack("<7I", header)
    sys.stdin.buffer.read(payload_len)
    reply = json.loads(template)
    reply["request_id"] = request_id
    sys.stdout.write(json.dumps(reply) + "\n")
    sys.stdout.flush()
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub provider: %v", err)
	}
	return path
}

func newStubAdapter(t *testing.T, body string) *faceFeatureExtractorAdapter {
	t.Helper()
	entry := stubProvider(t, body)
	client := faceextract.New(faceextract.Options{
		Entry: func(context.Context) (string, error) { return entry, nil },
	})
	t.Cleanup(func() { _ = client.Close() })
	return &faceFeatureExtractorAdapter{client: client}
}

// TestFaceFeatureExtractorAdapter_MapsInputFailuresToDomainSentinels 锁定适配器的错误翻译契约：
// 上传输入本身的问题（解不开）必须变成 person 包的哨兵，让 HTTP 层回答 4xx；
// 「照片里没人脸」同理；其余错误一律透传，避免把服务端故障伪装成「照片有问题」。
//
// 这是 internal/person 与 internal/faceextract 之间唯一的翻译点，它一旦漏掉某个哨兵，
// 客户端就会收到 500。
func TestFaceFeatureExtractorAdapter_MapsInputFailuresToDomainSentinels(t *testing.T) {
	ctx := context.Background()

	t.Run("undecodable bytes map to ErrUnsupportedImageFormat", func(t *testing.T) {
		// 解码在 Go 侧完成，坏字节永远到不了 provider —— 不需要进程就能断言。
		adapter := newStubAdapter(t, `{"status":"passed","feature_dim":512,"features":[]}`)
		_, err := adapter.ExtractFaceFeature(ctx, []byte("not-an-image"))
		if !errors.Is(err, person.ErrUnsupportedImageFormat) {
			t.Fatalf("want person.ErrUnsupportedImageFormat, got %v", err)
		}
	})

	t.Run("empty payload maps to ErrUnsupportedImageFormat", func(t *testing.T) {
		adapter := newStubAdapter(t, `{"status":"passed","feature_dim":512,"features":[]}`)
		_, err := adapter.ExtractFaceFeature(ctx, nil)
		if !errors.Is(err, person.ErrUnsupportedImageFormat) {
			t.Fatalf("want person.ErrUnsupportedImageFormat, got %v", err)
		}
	})

	t.Run("no detectable face maps to ErrNoFaceDetected", func(t *testing.T) {
		adapter := newStubAdapter(t, `{"status":"failed","code":"NO_FACE_DETECTED"}`)
		_, err := adapter.ExtractFaceFeature(ctx, blackPNG(t))
		if !errors.Is(err, person.ErrNoFaceDetected) {
			t.Fatalf("want person.ErrNoFaceDetected, got %v", err)
		}
	})

	t.Run("multiple faces map to ErrMultipleFaces", func(t *testing.T) {
		// 合影不是「照片坏了」也不是「服务坏了」：它是必须退回人工复核的输入裁决，
		// 所以既不能落到 ErrInternal（500），也不能落到 ErrUnsupportedImageFormat。
		adapter := newStubAdapter(t, `{"status":"failed","code":"MULTIPLE_FACES"}`)
		_, err := adapter.ExtractFaceFeature(ctx, blackPNG(t))
		if !errors.Is(err, person.ErrMultipleFaces) {
			t.Fatalf("want person.ErrMultipleFaces, got %v", err)
		}
		if errors.Is(err, person.ErrNoFaceDetected) || errors.Is(err, person.ErrUnsupportedImageFormat) {
			t.Fatalf("a group photo must not be reported as an input-format problem, got %v", err)
		}
	})

	t.Run("provider incapable of extraction stays a server error", func(t *testing.T) {
		adapter := newStubAdapter(t, `{"status":"failed","code":"UNSUPPORTED"}`)
		_, err := adapter.ExtractFaceFeature(ctx, blackPNG(t))
		if err == nil {
			t.Fatal("expected an error for an unsupported provider")
		}
		if errors.Is(err, person.ErrNoFaceDetected) || errors.Is(err, person.ErrUnsupportedImageFormat) {
			t.Fatalf("a provider capability problem must not be reported as an input problem, got %v", err)
		}
	})

	t.Run("webp is decodable end to end", func(t *testing.T) {
		data, err := os.ReadFile("../faceextract/testdata/face_sample.webp")
		if err != nil {
			t.Fatalf("read webp fixture: %v", err)
		}
		features := make([]string, 512)
		for i := range features {
			features[i] = "0.01"
		}
		adapter := newStubAdapter(t, `{"status":"passed","feature_dim":512,"features":[`+joinComma(features)+`]}`)

		got, err := adapter.ExtractFaceFeature(ctx, data)
		if err != nil {
			t.Fatalf("WebP must be decodable through the adapter, got %v", err)
		}
		if len(got) != 512 {
			t.Fatalf("want 512 features, got %d", len(got))
		}
	})
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i != 0 {
			out += ","
		}
		out += item
	}
	return out
}
