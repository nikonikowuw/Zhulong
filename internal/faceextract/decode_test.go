package faceextract

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

// checkerImage 生成一张每个像素都唯一的 2x3 测试图，便于逐点校验方向映射。
func checkerImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	palette := []color.RGBA{
		{R: 10, G: 20, B: 30, A: 255},
		{R: 40, G: 50, B: 60, A: 255},
		{R: 70, G: 80, B: 90, A: 255},
		{R: 100, G: 110, B: 120, A: 255},
		{R: 130, G: 140, B: 150, A: 255},
		{R: 160, G: 170, B: 180, A: 255},
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, palette[y*2+x])
		}
	}
	return img
}

func encodeTestJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// jpegWithOrientation 生成带 EXIF Orientation 的 JPEG。
// 标准库不能写 EXIF，因此在 SOI 之后手工拼一个 APP1 段插进去。
func jpegWithOrientation(t *testing.T, img image.Image, orientation int) []byte {
	t.Helper()
	raw := encodeTestJPEG(t, img)
	if len(raw) < 2 || raw[0] != 0xff || raw[1] != 0xd8 {
		t.Fatalf("unexpected jpeg header")
	}

	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0} // 小端 TIFF 头 + IFD0 偏移 8 + 条目数 1
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry[0:2], 0x0112) // Orientation
	binary.LittleEndian.PutUint16(entry[2:4], 3)      // 类型 SHORT
	binary.LittleEndian.PutUint32(entry[4:8], 1)      // 数量 1
	binary.LittleEndian.PutUint16(entry[8:10], uint16(orientation))
	tiff = append(tiff, entry...)
	tiff = append(tiff, 0, 0, 0, 0) // 下一个 IFD 偏移 = 0

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	segment = append(segment, payload...)

	out := make([]byte, 0, len(raw)+len(segment))
	out = append(out, raw[:2]...)
	out = append(out, segment...)
	return append(out, raw[2:]...)
}

// pngHeader 拼一个只有签名与 IHDR 的 PNG（无 IDAT）。
// DecodeConfig 对非调色板颜色类型只需读到 IHDR，因此它能证明超限图在完整解码之前就被拒绝。
func pngHeader(w, h int) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8] = 8 // 位深
	ihdr[9] = 2 // 颜色类型：truecolor

	// chunk = 长度(4) + 类型(4) + 数据(13) + CRC(4)，CRC 覆盖类型与数据
	chunk := make([]byte, 0, 21)
	chunk = append(chunk, 0, 0, 0, 13)
	chunk = append(chunk, "IHDR"...)
	chunk = append(chunk, ihdr...)

	sum := crc32.ChecksumIEEE(chunk[4:])
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk...)
	return append(out, byte(sum>>24), byte(sum>>16), byte(sum>>8), byte(sum))
}

func TestExifOrientation(t *testing.T) {
	img := checkerImage()

	if got := exifOrientation(encodeTestJPEG(t, img)); got != 1 {
		t.Errorf("JPEG without EXIF: want 1, got %d", got)
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if got := exifOrientation(pngBuf.Bytes()); got != 1 {
		t.Errorf("PNG input: want 1, got %d", got)
	}
	if got := exifOrientation([]byte("not-an-image")); got != 1 {
		t.Errorf("garbage input: want 1, got %d", got)
	}
	if got := exifOrientation(nil); got != 1 {
		t.Errorf("empty input: want 1, got %d", got)
	}

	for want := 1; want <= 8; want++ {
		if got := exifOrientation(jpegWithOrientation(t, img, want)); got != want {
			t.Errorf("orientation %d: got %d", want, got)
		}
	}

	// 越界取值必须被安全忽略，而不是原样透传
	if got := exifOrientation(jpegWithOrientation(t, img, 99)); got != 1 {
		t.Errorf("out-of-range orientation: want 1, got %d", got)
	}
}

func TestPackRGB24_AppliesExifOrientation(t *testing.T) {
	img := checkerImage() // 2x3，像素唯一

	cases := []struct {
		orientation int
		w, h        int
		position    func(x, y int) (int, int) // 源 (x,y) 在输出中的落点
	}{
		{1, 2, 3, func(x, y int) (int, int) { return x, y }},
		{2, 2, 3, func(x, y int) (int, int) { return 1 - x, y }},
		{3, 2, 3, func(x, y int) (int, int) { return 1 - x, 2 - y }},
		{4, 2, 3, func(x, y int) (int, int) { return x, 2 - y }},
		{5, 3, 2, func(x, y int) (int, int) { return y, x }},
		{6, 3, 2, func(x, y int) (int, int) { return 2 - y, x }},
		{7, 3, 2, func(x, y int) (int, int) { return 2 - y, 1 - x }},
		{8, 3, 2, func(x, y int) (int, int) { return y, 1 - x }},
	}

	for _, tc := range cases {
		dst := make([]byte, tc.w*tc.h*3)
		packRGB24(img, tc.orientation, dst)

		for y := 0; y < 3; y++ {
			for x := 0; x < 2; x++ {
				ox, oy := tc.position(x, y)
				src := img.RGBAAt(x, y)
				idx := (oy*tc.w + ox) * 3
				got := [3]byte{dst[idx], dst[idx+1], dst[idx+2]}
				want := [3]byte{src.R, src.G, src.B}
				if got != want {
					t.Errorf("orientation %d: source (%d,%d) -> (%d,%d): got %v, want %v",
						tc.orientation, x, y, ox, oy, got, want)
				}
			}
		}
	}
}

func readWebPFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/face_sample.webp")
	if err != nil {
		t.Fatalf("read webp fixture: %v", err)
	}
	return data
}

func TestDecodeToRGB24_SupportsWebP(t *testing.T) {
	pixels, w, h, err := DecodeToRGB24(readWebPFixture(t))
	if err != nil {
		t.Fatalf("WebP must decode (regression: 曾因缺少解码器报 unknown format): %v", err)
	}
	if w != 320 || h != 222 {
		t.Fatalf("want 320x222, got %dx%d", w, h)
	}
	if len(pixels) != w*h*3 {
		t.Fatalf("want %d pixel bytes, got %d", w*h*3, len(pixels))
	}

	var nonZero int
	for _, b := range pixels {
		if b != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("decoded WebP is entirely black")
	}
}

func TestDecodeToRGB24_RejectsUnknownAndCorruptedFormats(t *testing.T) {
	cases := map[string][]byte{
		"unknown format": []byte("not-an-image"),
		"truncated jpeg": {0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00},
		"empty":          {},
	}

	for name, data := range cases {
		if _, _, _, err := DecodeToRGB24(data); !errors.Is(err, ErrUnsupportedImageFormat) {
			t.Errorf("%s: want ErrUnsupportedImageFormat, got %v", name, err)
		}
	}
}

// TestDecodeToRGB24_AnimatedWebPFailsClosed 钉住动图 WebP 的行为：
// 它的 DecodeConfig 能读出画布尺寸，但 Decode 不支持动画容器。此时必须是
// 「可分类的输入错误」而不是 panic、空图或 5xx。
// 若将来 x/image 支持了动画（改为返回首帧），本用例应改为断言首帧解码成功。
func TestDecodeToRGB24_AnimatedWebPFailsClosed(t *testing.T) {
	data, err := os.ReadFile("testdata/animated.webp")
	if err != nil {
		t.Fatalf("read animated fixture: %v", err)
	}

	if _, _, _, err := DecodeToRGB24(data); !errors.Is(err, ErrUnsupportedImageFormat) {
		t.Fatalf("want ErrUnsupportedImageFormat for animated WebP, got %v", err)
	}
}

// TestDecodeToRGB24_RejectsOversizedBeforeFullDecode 用「只有 IHDR 的 PNG」证明
// 超限图在完整解码之前就被拒绝：否则 20000x20000 会先吃掉约 1.2GB 才被拦下。
func TestDecodeToRGB24_RejectsOversizedBeforeFullDecode(t *testing.T) {
	header := pngHeader(20000, 20000)
	if _, _, _, err := DecodeToRGB24(header); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument on oversized image header, got %v", err)
	}

	// 边界：恰好等于上限必须放行（尺寸检查不能多拒一个像素）
	atLimit := pngHeader(MaxImageDimension, MaxImageDimension)
	_, w, h, err := DecodeToRGB24(atLimit)
	if err == nil {
		t.Fatalf("expected decode failure for header-only PNG, got %dx%d", w, h)
	}
	if errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("image exactly at MaxImageDimension must pass the size guard, got %v", err)
	}
}

// packRGB24Reference 是「逐像素 switch」的旧实现，只作为等价性基准保留：
// 仿射系数一旦写错，方向 5..8 会产出「行列对调但顺序错」的静默错图 —— 没有任何报错，
// 只是检测器开始漏检。新实现必须与它在全部 8 个方向上逐字节一致。
func packRGB24Reference(img image.Image, orientation int, dst []byte) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	outWidth, _ := orientedDimensions(img, orientation)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var ox, oy int
			switch orientation {
			case 2:
				ox, oy = w-1-x, y
			case 3:
				ox, oy = w-1-x, h-1-y
			case 4:
				ox, oy = x, h-1-y
			case 5:
				ox, oy = y, x
			case 6:
				ox, oy = h-1-y, x
			case 7:
				ox, oy = h-1-y, w-1-x
			case 8:
				ox, oy = y, w-1-x
			default:
				ox, oy = x, y
			}

			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			idx := (oy*outWidth + ox) * 3
			dst[idx] = byte(r >> 8)
			dst[idx+1] = byte(g >> 8)
			dst[idx+2] = byte(b >> 8)
		}
	}
}

// oddImage 是「没有专门类型」的 image.Image，用来钉住回退路径。
type oddImage struct{ w, h int }

func (o oddImage) ColorModel() color.Model { return color.RGBAModel }

func (o oddImage) Bounds() image.Rectangle { return image.Rect(0, 0, o.w, o.h) }

func (o oddImage) At(x, y int) color.Color {
	return color.RGBA{R: uint8(x * 17), G: uint8(y * 23), B: uint8((x + y) * 7), A: 255}
}

func TestPackRGB24_MatchesReferenceAcrossImageTypes(t *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(0, 0, 3, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 3; x++ {
			// alpha 故意不是 255：预乘语义一旦写错，半透明像素就会偏色。
			nrgba.SetNRGBA(x, y, color.NRGBA{R: uint8(10 + x*40), G: uint8(20 + y*30), B: uint8(90 - x*7), A: uint8(60 + x*30)})
		}
	}

	gray := image.NewGray(image.Rect(0, 0, 3, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 3; x++ {
			gray.SetGray(x, y, color.Gray{Y: uint8(x*30 + y*11)})
		}
	}

	jpegBytes := encodeTestJPEG(t, checkerImage())
	ycbcr, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}

	sources := map[string]image.Image{
		"rgba":   checkerImage(),
		"nrgba":  nrgba,
		"gray":   gray,
		"ycbcr":  ycbcr,
		"custom": oddImage{w: 3, h: 4},
	}

	for name, img := range sources {
		for orientation := 1; orientation <= 8; orientation++ {
			w, h := orientedDimensions(img, orientation)
			got := make([]byte, w*h*3)
			want := make([]byte, w*h*3)
			packRGB24(img, orientation, got)
			packRGB24Reference(img, orientation, want)
			if !bytes.Equal(got, want) {
				t.Errorf("%s orientation %d: output differs from the reference mapping", name, orientation)
			}
		}
	}
}

// BenchmarkPackRGB24 给「登记一张 1920x1080 底照要多久」一个可测的数字，
// 免得围绕 img.At() 的接口调用做没有测量的优化。
func BenchmarkPackRGB24(b *testing.B) {
	img := image.NewYCbCr(image.Rect(0, 0, 1920, 1080), image.YCbCrSubsampleRatio420)
	for i := range img.Y {
		img.Y[i] = uint8(i)
	}
	dst := make([]byte, 1920*1080*3)

	b.SetBytes(int64(len(dst)))
	for b.Loop() {
		packRGB24(img, 6, dst)
	}
}
