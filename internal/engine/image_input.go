package engine

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"

	// 图片解码器注册表。这三个导入定义了本引擎接受的图片格式，
	// 必须与 internal/person 的上传白名单（扩展名 + magic bytes）保持一致：
	// WebP 由 golang.org/x/image 提供，Go 标准库没有内置解码器。
	_ "golang.org/x/image/webp"
	_ "image/jpeg"
	_ "image/png"
)

// decodeToRGB24 解码图片字节流，按 EXIF 方向归一化后输出打包连续的 RGB24 像素。
//
// 返回的错误区分两类失败，上层据此给出不同的 HTTP 语义：
//   - 无法解码（格式不在注册表内，或文件损坏）→ ErrUnsupportedImageFormat，属于调用方输入问题；
//   - 尺寸非法或单边超过 maxDimension → ErrInvalidArgument，属于调用方输入问题。
//
// 尺寸先用 DecodeConfig 只读文件头探测，避免超大图在完整解码时才被拒绝、先吃掉一次巨额分配。
//
// ⚠️ 仅支持静态图：动图 WebP 的 DecodeConfig 能读出画布尺寸，但 Decode 会报
// "webp: invalid format"（x/image/webp 不支持动画容器），同样归入 ErrUnsupportedImageFormat。
func decodeToRGB24(data []byte, maxDimension int) ([]byte, int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: read image header: %v", ErrUnsupportedImageFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, 0, 0, fmt.Errorf("%w: invalid image dimensions (%dx%d)", ErrInvalidArgument, cfg.Width, cfg.Height)
	}
	if cfg.Width > maxDimension || cfg.Height > maxDimension {
		return nil, 0, 0, fmt.Errorf("%w: image dimensions %dx%d exceed maximum allowed %d", ErrInvalidArgument, cfg.Width, cfg.Height, maxDimension)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: decode image: %v", ErrUnsupportedImageFormat, err)
	}

	orientation := exifOrientation(data)
	width, height := orientedDimensions(img, orientation)

	pixels := make([]byte, width*height*3)
	packRGB24(img, orientation, pixels)
	return pixels, width, height, nil
}

// orientedDimensions 返回按 EXIF 方向归一化之后的像素尺寸。
// 方向 5..8 是四种「行列互换」的变换，宽高需要互换。
func orientedDimensions(img image.Image, orientation int) (int, int) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if orientation >= 5 && orientation <= 8 {
		return h, w
	}
	return w, h
}

// packRGB24 按 EXIF 方向把 img 映射为打包连续的 RGB24 像素（行优先，stride = 输出宽度*3）。
//
// EXIF Orientation 语义（0th row / 0th column 的视觉位置）：
//
//	1 正立   2 水平镜像   3 旋转 180   4 垂直镜像
//	5 转置   6 顺时针 90   7 反转置     8 逆时针 90
//
// ⚠️ 手机竖拍照片绝大多数是 6：像素在文件里是「躺着」的，而 image.Decode 不解释 EXIF，
// 直接送检测器会导致人脸漏检。方向必须在进入推理前抹平。
func packRGB24(img image.Image, orientation int, dst []byte) {
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

// exifOrientation 从 JPEG 字节流中解析 EXIF 的 Orientation 标签 (0x0112)。
//
// 返回 1..8；非 JPEG、没有 EXIF、标签缺失或解析失败时一律返回 1（视为已正立）。
// 本函数只读，不做任何分配，解析失败绝不报错——方向是「尽力而为」的增强，
// 不能因为一张图缺 EXIF 就拒绝它。
//
// ⚠️ 仅处理 JPEG 的 APP1/Exif 容器。WebP 的 EXIF 存在 RIFF 的 "EXIF" chunk 里，
// 而 WebP 几乎总是由工具转换而来（转换时方向已烘进像素），因此暂不处理。
func exifOrientation(data []byte) int {
	// JPEG 以 SOI(0xFFD8) 开头，之后是一串 marker 段；EXIF 位于 APP1(0xFFE1)。
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}

	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 1
		}
		marker := data[i+1]
		switch {
		case marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			i += 2 // 无载荷的独立 marker
			continue
		case marker == 0xda || marker == 0xd9:
			return 1 // 已进入扫描数据，后面不会再有 APP1
		}

		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		payload := data[i+4 : i+2+segLen]
		if marker == 0xe1 && len(payload) > 6 && string(payload[:6]) == "Exif\x00\x00" {
			return parseTIFFOrientation(payload[6:])
		}
		i += 2 + segLen
	}
	return 1
}

// parseTIFFOrientation 在 EXIF 的 TIFF 头之后查找 IFD0 里的 Orientation 标签。
func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}

	var order binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		order = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}

	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
		return 1
	}
	entryCount := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))

	for i := 0; i < entryCount; i++ {
		entry := ifdOffset + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:entry+2]) != 0x0112 {
			continue
		}
		if order.Uint16(tiff[entry+2:entry+4]) != 3 { // 类型必须是 SHORT
			return 1
		}
		if value := int(order.Uint16(tiff[entry+8 : entry+10])); value >= 1 && value <= 8 {
			return value
		}
		return 1
	}
	return 1
}
