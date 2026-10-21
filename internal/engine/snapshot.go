package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <stdlib.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"errors"
	"runtime"
	"unsafe"
)

// MaxSnapshotBoxes 是单次抓拍允许携带的最大裁剪框数量，与 C 侧
// ZHULONG_SNAPSHOT_MAX_BOXES 严格一致。超出会被原生层拒绝。
const MaxSnapshotBoxes = 8

// DefaultSnapshotQuality 是 JPEG 质量的默认值（0 表示「使用默认」）。
const DefaultSnapshotQuality = 85

// ErrSnapshotUnavailable 表示原生侧不具备抓拍能力（静态 FFmpeg 未编译 mjpeg
// 编码器，或目标平台不支持）。它与「本次抓拍失败」不同：重试永远不会成功。
var ErrSnapshotUnavailable = &NativeError{
	Op:      "snapshot",
	Code:    StatusUnsupported,
	Message: "jpeg encoder is not available in this build",
}

// SnapshotBox 是一个归一化裁剪框，坐标语义与 aitypes.DetectionItem.Box 完全一致：
// [x1, y1, x2, y2] ∈ [0.0, 1.0]，x1<=x2，y1<=y2。
type SnapshotBox [4]float64

// SnapshotImage 是一张抓拍图。
//
// BoxIndex 为 -1 表示全幅图；>=0 表示对应 SnapshotBoxes[i] 的裁剪图。
// 某个框因越界或退化被原生层跳过时，该索引不会出现在结果里 — 调用方必须按
// BoxIndex 索引，不得假设「第 i 张裁剪图对应 boxes[i]」。
type SnapshotImage struct {
	BoxIndex int
	Width    int
	Height   int
	JPEG     []byte
}

// SnapshotOptions 控制一次抓拍的编码参数。
type SnapshotOptions struct {
	// Quality 为 JPEG 质量 1..100。0 表示使用 DefaultSnapshotQuality。
	Quality int
	// Boxes 为需要额外输出的裁剪框；为空表示只出全幅图。
	// 上限为 MaxSnapshotBoxes。
	Boxes []SnapshotBox
}

// Snapshot 对当前流的最新一帧执行抓拍（全幅 + 可选裁剪），返回完整 JPEG 字节流。
//
// ⚠️ 语义与陷阱（与 C ABI 文档保持一致）：
//   - 抓到的是**当前最新一帧**，不是产生某个推理结果的那一帧。二者存在固有延迟
//     （实测约 1~4 帧），调用方应把它当作已知语义而非缺陷。
//   - 该流必须已有活跃的 AI 解码会话（即已有 AI 消费者），否则返回 StatusNotFound。
//   - 本调用同步执行 JPEG 编码（RK3568 上 1080p 约 35~60ms），不可在热路径或
//     Native 回调中调用。
//
// 返回的 JPEG 切片已深拷贝进 Go 堆，与原生句柄生命周期无关，调用方可以自由持有。
func (s *Stream) Snapshot(ctx context.Context, options SnapshotOptions) ([]SnapshotImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(options.Boxes) > MaxSnapshotBoxes {
		return nil, &NativeError{
			Op:      "snapshot",
			Code:    StatusInvalidArgument,
			Message: "too many snapshot boxes",
		}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	generation := s.generation
	streamID := s.id
	s.mu.Unlock()

	// 构造请求。使用 C.malloc 分配的数组，而不是 Go 切片：
	// C 侧会在调用期间读它，用 Go 内存虽然合法（不含指针）但需要额外 Pin，
	// 而这里的分配量极小（最多 32 个 float32），malloc 更直接且无需担心移动。
	var req C.Zhulong_snapshot_request
	var boxesPtr *C.float
	if len(options.Boxes) > 0 {
		size := C.size_t(len(options.Boxes) * 4 * int(unsafe.Sizeof(C.float(0))))
		raw := C.malloc(size)
		if raw == nil {
			return nil, &NativeError{
				Op:      "snapshot",
				Code:    StatusOutOfMemory,
				Message: "failed to allocate box array",
			}
		}
		defer C.free(raw)
		boxesPtr = (*C.float)(raw)
		flat := unsafe.Slice(boxesPtr, len(options.Boxes)*4)
		for i, box := range options.Boxes {
			for c := 0; c < 4; c++ {
				flat[i*4+c] = C.float(box[c])
			}
		}
		req.box_count = C.int32_t(len(options.Boxes))
		req.boxes = boxesPtr
	}
	req.quality = C.int32_t(clampQuality(options.Quality))

	return s.engine.snapshot(generation, streamID, &req)
}

// clampQuality 把调用方给的质量收敛进 [1,100]，0 视为默认值。
func clampQuality(quality int) int {
	if quality == 0 {
		return DefaultSnapshotQuality
	}
	if quality < 1 {
		return 1
	}
	if quality > 100 {
		return 100
	}
	return quality
}

// snapshot 执行一次原生抓拍调用，并把结果深拷贝到 Go 堆。
//
// ⚠️ 内存所有权：结果句柄由 C 侧分配，必须由 Zhulong_snapshot_destroy 释放。
// 每张图的 JPEG 字节在 destroy 之前用 C.GoBytes 深拷贝出来，绝不把原生指针
// 交给 Go 长期持有。
func (e *Engine) snapshot(gen uint64, streamID C.Zhulong_stream_id, req *C.Zhulong_snapshot_request) ([]SnapshotImage, error) {
	e.handleMu.RLock()
	defer e.handleMu.RUnlock()

	e.mu.Lock()
	if e.handle == nil || !e.running || e.stopping || e.generation != gen {
		e.mu.Unlock()
		return nil, ErrNotRunning
	}
	handle := e.handle
	e.mu.Unlock()

	var result C.Zhulong_snapshot_h
	st := C.Zhulong_stream_snapshot(handle, streamID, req, &result)
	runtime.KeepAlive(req)
	if st != C.Zhulong_OK {
		// 把「构建不支持抓拍」与「本次抓拍失败」区分开：前者重试无意义。
		if StatusCode(st) == StatusUnsupported {
			return nil, ErrSnapshotUnavailable
		}
		return nil, mapNativeStatus("snapshot", int32(st))
	}
	if result == nil {
		return nil, errors.New("native snapshot returned a nil handle")
	}
	defer C.Zhulong_snapshot_destroy(result)

	count := int(C.Zhulong_snapshot_count(result))
	if count <= 0 {
		return nil, errors.New("native snapshot returned no images")
	}

	images := make([]SnapshotImage, 0, count)
	for i := 0; i < count; i++ {
		view := C.Zhulong_snapshot_at(result, C.int32_t(i))
		if view == nil {
			// 索引区间由 count 保证，取不到说明原生侧违反了契约，跳过而不是崩。
			continue
		}
		if view.data == nil || view.size == 0 {
			continue
		}
		images = append(images, SnapshotImage{
			BoxIndex: int(view.box_index),
			Width:    int(view.width),
			Height:   int(view.height),
			JPEG:     C.GoBytes(unsafe.Pointer(view.data), C.int(view.size)),
		})
	}
	if len(images) == 0 {
		return nil, errors.New("native snapshot returned only empty images")
	}
	return images, nil
}
