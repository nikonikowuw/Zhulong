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
	"unsafe"
)

// MaxSnapshotBoxes 是单次抓拍允许携带的最大裁剪框数量，与 C 侧
// ZHULONG_SNAPSHOT_MAX_BOXES 严格一致。超出会被原生层拒绝。
const MaxSnapshotBoxes = 8

// DefaultSnapshotQuality 是 JPEG 质量的默认值（0 表示「使用默认」）。
const DefaultSnapshotQuality = 85

// SnapshotBox 是一个归一化裁剪框：坐标语义与 ViewRect 一致
// （[x1, y1, x2, y2] ∈ [0.0, 1.0]，x1<=x2，y1<=y2），只是用途不同：
// ViewRect 是分析窗口，SnapshotBox 是抓拍裁剪框。两者刻意不合并成同一个类型 ——
// 它们可以赋值给同一变量的时候就没人能拦住把分析窗口当裁剪框传进去。
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
	// PTS 为目标帧的时间戳（源流原生时基 ticks）；<= 0 抓当前最新帧，
	// > 0 则优先在历史环形缓存中精确匹配。
	PTS int64
	// Boxes 为需要额外输出的裁剪框；为空表示只出全幅图。
	// 上限为 MaxSnapshotBoxes。
	Boxes []SnapshotBox
}

// Snapshot 对指定流执行抓拍（全幅 + 可选裁剪），返回完整 JPEG 字节流。
//
// ⚠️ 语义与陷阱（与 C ABI 文档保持一致）：
//   - 若 options.PTS > 0，优先在 Native 历史环形缓存中精确匹配对应帧；若历史已淘汰或
//     options.PTS <= 0，退回抓取当前最新的一帧。二者在历史未匹配时存在固有延迟
//     （实测约 1~4 帧）。
//   - 该流必须已有活跃的 AI 解码会话（即已有 AI 消费者），否则返回 StatusNotFound。
//   - 本调用同步执行 JPEG 编码（RK3568 上 1080p 约 35~60ms），不可在热路径或
//     Native 回调中调用。
//
// 返回的 JPEG 切片已深拷贝进 Go 堆，与原生句柄生命周期无关，调用方可以自由持有。
func (s *Stream) Snapshot(ctx context.Context, options SnapshotOptions) ([]SnapshotImage, error) {
	if len(options.Boxes) > MaxSnapshotBoxes {
		return nil, nativeErrorf(
			"snapshot",
			StatusInvalidArgument,
			"too many snapshot boxes: %d > %d",
			len(options.Boxes),
			MaxSnapshotBoxes,
		)
	}

	// lease 同时校验 ctx 与关闭态，因此不需要在入参校验阶段重复 ctx.Err()。
	ref, err := s.lease(ctx)
	if err != nil {
		return nil, err
	}

	// 构造请求。使用 C.malloc 分配的数组，而不是 Go 切片：
	// C 侧会在调用期间读它，用 Go 内存虽然合法（不含指针）但需要额外 Pin，
	// 而这里的分配量极小（最多 32 个 float32），malloc 更直接且无需担心移动。
	var req C.Zhulong_snapshot_request
	if len(options.Boxes) > 0 {
		size := C.size_t(len(options.Boxes) * 4 * int(unsafe.Sizeof(C.float(0))))
		raw := C.malloc(size)
		if raw == nil {
			return nil, nativeErrorf(
				"snapshot",
				StatusOutOfMemory,
				"failed to allocate %d bytes for box array",
				uint64(size),
			)
		}
		defer C.free(raw)

		boxes := unsafe.Slice((*C.float)(raw), len(options.Boxes)*4)
		for i, box := range options.Boxes {
			for c, coordinate := range box {
				boxes[i*4+c] = C.float(coordinate)
			}
		}
		req.box_count = C.int32_t(len(options.Boxes))
		req.boxes = (*C.float)(raw)
	}
	req.quality = C.int32_t(clampQuality(options.Quality))
	req.target_pts = C.int64_t(options.PTS)

	return s.engine.snapshot(ref.generation, ref.id, &req)
}

// clampQuality 把调用方给的质量收敛进 [1,100]，0 视为默认值。
func clampQuality(quality int) int {
	if quality == 0 {
		return DefaultSnapshotQuality
	}
	return min(max(quality, 1), 100)
}

// snapshot 执行一次原生抓拍调用，并把结果深拷贝到 Go 堆。
//
// ⚠️ 内存所有权：结果句柄由 C 侧分配，必须由 Zhulong_snapshot_destroy 释放。
// 每张图的 JPEG 字节在 destroy 之前用 C.GoBytes 深拷贝出来，绝不把原生指针
// 交给 Go 长期持有。
func (e *Engine) snapshot(
	gen uint64,
	streamID C.Zhulong_stream_id,
	req *C.Zhulong_snapshot_request,
) ([]SnapshotImage, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return nil, err
	}
	defer release()

	var result C.Zhulong_snapshot_h
	st := C.Zhulong_stream_snapshot(lease.handle, streamID, req, &result)
	if st != C.Zhulong_OK {
		// 把「构建不支持抓拍」与「本次抓拍失败」区分开：前者重试无意义。
		if StatusCode(st) == StatusUnsupported {
			return nil, ErrSnapshotUnavailable
		}
		return nil, mapNativeStatus("snapshot", int32(st))
	}
	if result == nil {
		return nil, nativeErrorf("snapshot", StatusInternal, "native snapshot returned a nil handle")
	}
	defer C.Zhulong_snapshot_destroy(result)

	count := int(C.Zhulong_snapshot_count(result))
	if count <= 0 {
		return nil, nativeErrorf("snapshot", StatusInternal, "native snapshot returned no images")
	}

	images := make([]SnapshotImage, 0, count)
	for i := range count {
		view := C.Zhulong_snapshot_at(result, C.int32_t(i))
		// 索引区间由 count 保证；取不到或空图说明原生侧违反了契约，跳过而不是崩。
		if view == nil || view.data == nil || view.size == 0 {
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
		return nil, nativeErrorf("snapshot", StatusInternal, "native snapshot returned only empty images")
	}
	return images, nil
}
