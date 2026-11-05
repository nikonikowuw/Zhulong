package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import "context"

// ViewRect 是严格归一化的窗口矩形 [x1, y1, x2, y2]，对应 C ABI 的 Zhulong_view_rect。
//
// 用 float64 是因为 Go 侧的身份比较与日志都按 float64 处理；跨 ABI 时按 float32 传递，
// 精度差异远小于一个像素（1080p 下 float32 的 ULP ≈ 1e-7，远小于 1/1920）。
type ViewRect [4]float64

// ViewState 是引擎回报的生效窗口：Since 之前发布的帧属于上一代窗口。
//
// Since 是宿主 UTC 毫秒；0 表示尚未发生过窗口切换。
type ViewState struct {
	Rect  ViewRect
	Since int64
}

// SetView 设置（rect != nil）或清除（rect == nil）该 AI 流的分析视图。
//
// 流已存在但还没有解码帧尺寸时返回 ErrStreamNotReady：配置已登记，首个解码帧后生效。
// 本方法绝不 acquire 流：流不存在直接返回 ErrNotFound。
func (s *Stream) SetView(ctx context.Context, rect *ViewRect) (ViewState, error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return ViewState{}, err
	}
	return s.engine.setView(ref.generation, ref.id, rect)
}

// GetView 查询当前生效窗口与切换点（只读，不 acquire）。
func (s *Stream) GetView(ctx context.Context) (ViewState, error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return ViewState{}, err
	}
	return s.engine.getView(ref.generation, ref.id)
}

func (e *Engine) setView(gen uint64, streamID C.Zhulong_stream_id, rect *ViewRect) (ViewState, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return ViewState{}, err
	}
	defer release()

	var cRect C.Zhulong_view_rect
	var rectPtr *C.Zhulong_view_rect
	if rect != nil {
		cRect = C.Zhulong_view_rect{
			x1: C.float(rect[0]),
			y1: C.float(rect[1]),
			x2: C.float(rect[2]),
			y2: C.float(rect[3]),
		}
		rectPtr = &cRect
	}

	var state C.Zhulong_view_state
	// 交给 C 的是 cRect（Go 栈上的 C 结构体），cgo 保证它在调用期间存活且 C 不保留它；
	// 这里不需要 runtime.KeepAlive(rect)——rect 在构造 cRect 时就已经取完值。
	st := C.Zhulong_stream_set_view(lease.handle, streamID, rectPtr, &state)
	if st != C.Zhulong_OK {
		return ViewState{}, mapNativeStatus("set_view", int32(st))
	}
	return viewStateFromC(state), nil
}

func (e *Engine) getView(gen uint64, streamID C.Zhulong_stream_id) (ViewState, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return ViewState{}, err
	}
	defer release()

	var state C.Zhulong_view_state
	st := C.Zhulong_stream_get_view(lease.handle, streamID, &state)
	if st != C.Zhulong_OK {
		return ViewState{}, mapNativeStatus("get_view", int32(st))
	}
	return viewStateFromC(state), nil
}

func viewStateFromC(state C.Zhulong_view_state) ViewState {
	return ViewState{
		Rect: ViewRect{
			float64(state.rect.x1),
			float64(state.rect.y1),
			float64(state.rect.x2),
			float64(state.rect.y2),
		},
		Since: int64(state.effective_since_ms),
	}
}
