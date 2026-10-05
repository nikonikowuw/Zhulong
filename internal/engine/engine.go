// Package engine 封装了基于 CGO 调用的 Native C++ 媒体处理引擎。
//
// 架构分层与职责：
//   - 上层（Go）：负责业务逻辑编排、生命周期管理与并发控制。
//   - 边界层（CGO）：负责 Go 与 C ABI 之间的数据传递与状态映射。
//   - 下层（C++）：负责高性能 RTSP 拉流、静态 FFmpeg 封装、多路流复用与数据包派发。
//
// 内存所有权与指针规则（⚠️ 关键约定）：
//   - 句柄所有权：Go 通过 C.Zhulong_engine_h 引用 C++ 内部堆分配的 Zhulong_engine_t 结构体。
//   - 谁分配谁释放：Engine.Start 调用 C.Zhulong_engine_create 分配底层对象；
//     Engine.Close 必须调用 C.Zhulong_engine_destroy 显式释放，禁止依赖 Go 垃圾回收器释放 C 内存。
//   - 指针边界：Go 传给 C 的指针不得指向包含 Go 指针的内存；在生命周期内不向 C 侧暴露可逃逸的 Go 指针。
package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <stdlib.h>
#include <Zhulong/engine.h>

void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet);
*/
import "C"

import (
	"context"
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"
)

type probeContext struct {
	engine C.Zhulong_engine_h
}

// Engine 封装 Native C++ 引擎的不透明指针句柄。
type Engine struct {
	lifecycleMu sync.Mutex // 串行化 Start, Stop, Close 等长时生命周期切换

	mu         sync.Mutex // 保护运行态、句柄及代次状态
	handle     C.Zhulong_engine_h
	running    bool
	stopping   bool
	generation uint64

	probeTokens  chan struct{}
	probesMu     sync.Mutex
	probesClosed bool
	activeProbes map[*probeContext]struct{}

	streamsMu     sync.Mutex
	activeStreams map[*Stream]struct{}
}

// New 创建一个未初始化的 Native 引擎包装实例。
func New() *Engine {
	return &Engine{
		probeTokens:   make(chan struct{}, 4),
		activeProbes:  make(map[*probeContext]struct{}),
		activeStreams: make(map[*Stream]struct{}),
	}
}

// Start 启动 Native 引擎。
func (e *Engine) Start() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil
	}

	if e.probeTokens == nil {
		e.probeTokens = make(chan struct{}, 4)
	}
	if e.activeProbes == nil {
		e.activeProbes = make(map[*probeContext]struct{})
	}
	if e.activeStreams == nil {
		e.activeStreams = make(map[*Stream]struct{})
	}

	e.generation++

	e.probesMu.Lock()
	e.probesClosed = false
	e.probesMu.Unlock()

	if e.handle == nil {
		var handle C.Zhulong_engine_h
		if status := C.Zhulong_engine_create(&handle); status != C.Zhulong_OK {
			e.mu.Unlock()
			return mapNativeStatus("create", int32(status))
		}
		e.handle = handle
	}

	if status := C.Zhulong_engine_start(e.handle); status != C.Zhulong_OK {
		C.Zhulong_engine_destroy(e.handle)
		e.handle = nil
		e.running = false
		e.mu.Unlock()
		return mapNativeStatus("start", int32(status))
	}

	e.running = true
	e.stopping = false
	e.mu.Unlock()
	return nil
}

// Stop 停止 Native 引擎。
func (e *Engine) Stop() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	return e.stopInternal()
}

func (e *Engine) stopInternal() error {
	e.mu.Lock()
	if !e.running && e.handle == nil {
		e.mu.Unlock()
		return nil
	}
	e.stopping = true
	e.running = false
	handle := e.handle
	e.mu.Unlock()

	// 1. 中止所有私有探测任务
	e.probesMu.Lock()
	e.probesClosed = true
	probes := make([]*probeContext, 0, len(e.activeProbes))
	for pCtx := range e.activeProbes {
		probes = append(probes, pCtx)
	}
	e.probesMu.Unlock()

	for _, pCtx := range probes {
		C.Zhulong_engine_stop(pCtx.engine)
	}

	// 2. 关闭并退订所有物理流持有着
	e.streamsMu.Lock()
	streams := make([]*Stream, 0, len(e.activeStreams))
	for s := range e.activeStreams {
		streams = append(streams, s)
	}
	e.streamsMu.Unlock()

	for _, s := range streams {
		_ = s.Close()
	}

	// 3. 停止常驻引擎实例（回收底层工作线程、排空回调）
	var status C.Zhulong_status_t = C.Zhulong_OK
	if handle != nil {
		status = C.Zhulong_engine_stop(handle)
	}

	e.mu.Lock()
	e.stopping = false
	e.mu.Unlock()

	if status != C.Zhulong_OK {
		return mapNativeStatus("stop", int32(status))
	}
	return nil
}

// Ready 查询当前 Native 引擎是否处于正常运行状态。
func (e *Engine) Ready() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running && !e.stopping
}

// Close 完全停止并销毁底层 C++ 引擎实例。
func (e *Engine) Close() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	e.mu.Lock()
	if e.handle == nil {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	stopErr := e.stopInternal()

	e.mu.Lock()
	if e.handle != nil {
		C.Zhulong_engine_destroy(e.handle)
		e.handle = nil
	}
	e.running = false
	e.stopping = false
	e.mu.Unlock()

	return stopErr
}

// Probe 对指定 RTSP 流执行独立的视频元数据探测。
// 每个探测使用隔离的私有 Native Engine 实例，最大并发数为 4。
func (e *Engine) Probe(ctx context.Context, uri string, options StreamOptions) (VideoInfo, error) {
	if err := validateRTSPURI(uri); err != nil {
		return VideoInfo{}, err
	}
	cOptions, err := convertStreamOptions(options, ctx)
	if err != nil {
		return VideoInfo{}, err
	}

	e.mu.Lock()
	if !e.running || e.stopping || e.handle == nil {
		e.mu.Unlock()
		return VideoInfo{}, ErrNotRunning
	}
	gen := e.generation
	e.mu.Unlock()

	// 获取并发探测令牌（最多 4 个）
	select {
	case e.probeTokens <- struct{}{}:
	case <-ctx.Done():
		return VideoInfo{}, ctx.Err()
	}
	defer func() { <-e.probeTokens }()

	if err := ctx.Err(); err != nil {
		return VideoInfo{}, err
	}

	// 重新核对引擎代次
	e.mu.Lock()
	if !e.running || e.stopping || e.generation != gen {
		e.mu.Unlock()
		return VideoInfo{}, ErrNotRunning
	}
	e.mu.Unlock()

	// 创建并启动独立探测用私有 Native 引擎
	var probeEngine C.Zhulong_engine_h
	if st := C.Zhulong_engine_create(&probeEngine); st != C.Zhulong_OK {
		return VideoInfo{}, mapNativeStatus("probe_create", int32(st))
	}
	defer C.Zhulong_engine_destroy(probeEngine)

	if st := C.Zhulong_engine_start(probeEngine); st != C.Zhulong_OK {
		return VideoInfo{}, mapNativeStatus("probe_start", int32(st))
	}

	pCtx := &probeContext{engine: probeEngine}
	e.probesMu.Lock()
	if e.probesClosed {
		e.probesMu.Unlock()
		C.Zhulong_engine_stop(probeEngine)
		return VideoInfo{}, ErrNotRunning
	}
	e.activeProbes[pCtx] = struct{}{}
	e.probesMu.Unlock()

	defer func() {
		e.probesMu.Lock()
		delete(e.activeProbes, pCtx)
		e.probesMu.Unlock()
		C.Zhulong_engine_stop(probeEngine)
	}()

	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))

	type probeResult struct {
		result C.Zhulong_probe_result_h
		status C.Zhulong_status_t
	}
	resChan := make(chan probeResult, 1)

	go func() {
		var outResult C.Zhulong_probe_result_h
		st := C.Zhulong_engine_probe(probeEngine, cURI, &cOptions, &outResult)
		resChan <- probeResult{result: outResult, status: st}
	}()

	select {
	case res := <-resChan:
		if res.result != nil {
			defer C.Zhulong_probe_result_destroy(res.result)
		}
		if ctx.Err() != nil {
			return VideoInfo{}, ctx.Err()
		}
		if res.status != C.Zhulong_OK {
			return VideoInfo{}, mapNativeStatus("probe", int32(res.status))
		}
		return extractVideoInfo(res.result)

	case <-ctx.Done():
		C.Zhulong_engine_stop(probeEngine)
		res := <-resChan
		if res.result != nil {
			C.Zhulong_probe_result_destroy(res.result)
		}
		return VideoInfo{}, ctx.Err()
	}
}

// Acquire 获取或复用物理 RTSP 流。
func (e *Engine) Acquire(ctx context.Context, uri string, consumerID uint64, kind ConsumerKind, options StreamOptions) (*Stream, error) {
	if err := validateRTSPURI(uri); err != nil {
		return nil, err
	}
	if err := validateConsumer(consumerID, kind); err != nil {
		return nil, err
	}
	cOptions, err := convertStreamOptions(options, ctx)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	if !e.running || e.stopping || e.handle == nil {
		e.mu.Unlock()
		return nil, ErrNotRunning
	}
	gen := e.generation
	handle := e.handle
	e.mu.Unlock()

	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))

	var streamID C.Zhulong_stream_id
	st := C.Zhulong_stream_acquire(handle, cURI, &cOptions, C.uint64_t(consumerID), C.int32_t(kind), &streamID)
	if st != C.Zhulong_OK {
		return nil, mapNativeStatus("acquire", int32(st))
	}

	stream := &Stream{
		engine:     e,
		id:         streamID,
		consumerID: consumerID,
		kind:       kind,
		url:        uri,
		generation: gen,
	}

	e.mu.Lock()
	if !e.running || e.stopping || e.generation != gen || e.handle == nil {
		e.mu.Unlock()
		C.Zhulong_stream_release(handle, streamID, C.uint64_t(consumerID))
		return nil, ErrNotRunning
	}
	e.streamsMu.Lock()
	e.activeStreams[stream] = struct{}{}
	e.streamsMu.Unlock()
	e.mu.Unlock()

	return stream, nil
}

func (e *Engine) getStreamStatus(gen uint64, streamID C.Zhulong_stream_id) (StreamStatus, error) {
	e.mu.Lock()
	if e.handle == nil || e.generation != gen {
		e.mu.Unlock()
		return StreamStatus{}, ErrStaleResource
	}
	handle := e.handle
	e.mu.Unlock()

	var cStatus C.Zhulong_stream_status
	st := C.Zhulong_stream_get_status(handle, streamID, &cStatus)
	if st != C.Zhulong_OK {
		return StreamStatus{}, mapNativeStatus("get_status", int32(st))
	}

	var streamErr error
	if cStatus.error != C.Zhulong_OK {
		streamErr = mapNativeStatus("stream", int32(cStatus.error))
	}

	return StreamStatus{
		State: StreamState(cStatus.state),
		Error: streamErr,
	}, nil
}

func (e *Engine) subscribe(gen uint64, streamID C.Zhulong_stream_id, consumerID uint64, sub *Subscription) (C.Zhulong_subscription_id, uintptr, error) {
	e.mu.Lock()
	if e.handle == nil || !e.running || e.stopping || e.generation != gen {
		e.mu.Unlock()
		return 0, 0, ErrNotRunning
	}
	handle := e.handle
	e.mu.Unlock()

	h := cgo.NewHandle(sub)
	token := uintptr(h)

	var subID C.Zhulong_subscription_id
	st := C.Zhulong_stream_subscribe(
		handle,
		streamID,
		C.uint64_t(consumerID),
		C.Zhulong_packet_callback(C.zhulongPacketCallbackBridge),
		C.uintptr_t(token),
		&subID,
	)

	if st != C.Zhulong_OK {
		h.Delete()
		return 0, 0, mapNativeStatus("subscribe", int32(st))
	}

	return subID, token, nil
}

func (e *Engine) unsubscribe(gen uint64, streamID C.Zhulong_stream_id, subID C.Zhulong_subscription_id, deleteToken func()) error {
	e.mu.Lock()
	if e.handle == nil || e.generation != gen {
		e.mu.Unlock()
		deleteToken()
		return ErrStaleResource
	}
	handle := e.handle
	e.mu.Unlock()

	st := C.Zhulong_stream_unsubscribe(handle, streamID, subID)
	if st == C.Zhulong_OK || st == C.Zhulong_ERR_NOT_RUNNING || st == C.Zhulong_ERR_NOT_FOUND {
		deleteToken()
	}

	if st != C.Zhulong_OK {
		return mapNativeStatus("unsubscribe", int32(st))
	}
	return nil
}

func (e *Engine) releaseStream(gen uint64, streamID C.Zhulong_stream_id, consumerID uint64) error {
	e.mu.Lock()
	if e.handle == nil || e.generation != gen {
		e.mu.Unlock()
		return ErrStaleResource
	}
	handle := e.handle
	e.mu.Unlock()

	st := C.Zhulong_stream_release(handle, streamID, C.uint64_t(consumerID))
	if st != C.Zhulong_OK {
		return mapNativeStatus("release", int32(st))
	}
	return nil
}

func (e *Engine) onStreamClosed(s *Stream) {
	e.streamsMu.Lock()
	delete(e.activeStreams, s)
	e.streamsMu.Unlock()
}

func convertStreamOptions(options StreamOptions, ctx context.Context) (C.Zhulong_stream_options, error) {
	if options.Transport != TransportTCP && options.Transport != TransportUDP {
		return C.Zhulong_stream_options{}, fmt.Errorf("%w: invalid transport %d", ErrInvalidArgument, options.Transport)
	}

	openTimeoutMs, err := resolveTimeoutMs(options.OpenTimeout, ctx)
	if err != nil {
		return C.Zhulong_stream_options{}, err
	}

	idleTimeoutMs, err := resolveTimeoutMs(options.IdleTimeout, nil)
	if err != nil {
		return C.Zhulong_stream_options{}, err
	}

	return C.Zhulong_stream_options{
		transport:       C.int32_t(options.Transport),
		open_timeout_ms: C.uint32_t(openTimeoutMs),
		idle_timeout_ms: C.uint32_t(idleTimeoutMs),
	}, nil
}

func extractVideoInfo(result C.Zhulong_probe_result_h) (VideoInfo, error) {
	var view C.Zhulong_video_view
	if st := C.Zhulong_probe_result_view(result, &view); st != C.Zhulong_OK {
		return VideoInfo{}, mapNativeStatus("probe_result_view", int32(st))
	}

	if view.extradata_size > MaxExtraDataBytes {
		return VideoInfo{}, fmt.Errorf("%w: extradata size %d exceeds limit %d", ErrPacketTooLarge, int(view.extradata_size), MaxExtraDataBytes)
	}

	var extra []byte
	if view.extradata_size > 0 {
		if view.extradata == nil {
			return VideoInfo{}, fmt.Errorf("%w: probe result extradata pointer is nil", ErrInternal)
		}
		extra = make([]byte, int(view.extradata_size))
		copy(extra, unsafe.Slice((*byte)(unsafe.Pointer(view.extradata)), int(view.extradata_size)))
	}

	return VideoInfo{
		Codec:     Codec(view.codec),
		Width:     int(view.width),
		Height:    int(view.height),
		FPS:       Rational{Num: int32(view.fps_num), Den: int32(view.fps_den)},
		TimeBase:  Rational{Num: int32(view.time_base_num), Den: int32(view.time_base_den)},
		ExtraData: extra,
	}, nil
}
