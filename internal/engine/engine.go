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
//
// 并发与 Context 约定：
//   - 所有接受 context.Context 的导出方法都要求传入非 nil 值（与标准库惯例一致）；本包不提供 nil 语义。
//   - Engine 上多把锁的获取顺序写在 Engine 类型注释里，新增调用点必须遵守。
package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <stdlib.h>
#include <Zhulong/engine.h>

void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet);
void zhulongAIResultCallbackBridge(uintptr_t token, Zhulong_stream_id stream, const char *json_payload, size_t payload_len);
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"unsafe"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
)

// Engine 封装 Native C++ 引擎的不透明指针句柄。
//
// 锁获取顺序（严格单向，禁止反向；违反即死锁）：
//
//	lifecycleMu → handleMu → mu → {probesMu, streamsMu}
//
// 两层职责分工：
//   - lifecycleMu 只串行化 Start / Stop / Close 这类长时状态切换，不在其下做 CGO 调用之外的等待。
//   - handleMu 是「CGO 调用 vs destroy」的隔离层：所有访问底层 handle 的调用持读锁，
//     Close 必须取写锁排空在途调用之后才能 destroy。
//   - mu 只保护状态字段（running/stopping/generation/pendingTokens…），**绝不在其下做 CGO 调用**。
//
// handle 同时受 handleMu（生命周期）与 mu（可见性）保护：前者保证指针不悬空，后者保证状态自洽。
type Engine struct {
	lifecycleMu sync.Mutex // 串行化 Start, Stop, Close 等长时生命周期切换

	handleMu sync.RWMutex // 读写保护底层 handle 的 CGO 调用与销毁

	mu            sync.Mutex // 保护运行态、句柄及代次状态
	handle        C.Zhulong_engine_h
	running       bool
	stopping      bool
	nativeStopped bool
	generation    uint64
	pendingTokens []func()

	probeTokens  chan struct{}
	probesMu     sync.Mutex
	probesClosed bool
	activeProbes map[*probeContext]struct{}

	streamsMu     sync.Mutex
	activeStreams map[*Stream]struct{}

	decodePolicy *DecodePolicy // 缓存配置的全局解码策略

	logHandle cgo.Handle
}

// DecodeBackend 定义原生解码后端类型。
type DecodeBackend int32

const (
	DecodeBackendAuto   DecodeBackend = 0
	DecodeBackendMPP    DecodeBackend = 1
	DecodeBackendFFmpeg DecodeBackend = 2
)

// DecodePolicy 定义全局硬件解码策略。
type DecodePolicy struct {
	Backend               DecodeBackend
	AllowSoftwareFallback bool
	MaxSessions           int
}

// DecodeStats 定义原生解码器运行态统计。
type DecodeStats struct {
	ActiveSessions    int
	MaxSessions       int
	RejectedRequests  int
	HardwareAvailable bool
}

// New 创建一个未初始化的 Native 引擎包装实例。
// Engine 的零值不可直接使用：队列与映射由本构造函数建立。
func New() *Engine {
	return &Engine{
		probeTokens:   make(chan struct{}, probeConcurrency),
		activeProbes:  make(map[*probeContext]struct{}),
		activeStreams: make(map[*Stream]struct{}),
	}
}

// activeStreamSet 快照当前在途的物理流持有者。
func (e *Engine) activeStreamSet() []*Stream {
	e.streamsMu.Lock()
	defer e.streamsMu.Unlock()
	streams := make([]*Stream, 0, len(e.activeStreams))
	for s := range e.activeStreams {
		streams = append(streams, s)
	}
	return streams
}

// deferTokenDrain 暂存 token 释放函数，等底层工作线程全部 join 之后再执行。
//
// ⚠️ 若在原生回调仍可能触发的窗口内释放 cgo.Handle，残余回调会访问非法句柄。
func (e *Engine) deferTokenDrain(release func()) {
	e.mu.Lock()
	e.pendingTokens = append(e.pendingTokens, release)
	e.mu.Unlock()
}

// drainPendingTokens 执行暂存的 token 释放函数（原生工作线程已全部汇合）。
func (e *Engine) drainPendingTokens() {
	e.mu.Lock()
	tokens := e.pendingTokens
	e.pendingTokens = nil
	e.mu.Unlock()
	for _, release := range tokens {
		release()
	}
}

// expectedABIVersion 直接取自本次编译所用的头文件宏，避免手工同步 Go 常量与
// ZHULONG_ABI_VERSION 漂移：这里校验的是「头文件」与「静态库归档」是否同源，
// 而不是 Go 源码与头文件是否同源。
const expectedABIVersion = uint32(C.ZHULONG_ABI_VERSION)

// verifyABI 校验头文件与静态库是否来自同一次构建（幂等，失败后不缓存结果）。
func verifyABI() error {
	var version C.uint32_t
	if status := C.Zhulong_engine_get_abi_version(&version); status != C.Zhulong_OK {
		return mapNativeStatus("abi_version", int32(status))
	}
	if uint32(version) != expectedABIVersion {
		return fmt.Errorf("%w: native=v%d, expected=v%d; run `make native-build`",
			ErrABIMismatch, uint32(version), expectedABIVersion)
	}
	return nil
}

// Start 幂等地启动 Native 引擎：已运行时为空操作，失败时不会留下半初始化的句柄。
// 每次成功启动都会推进 generation，旧代次的 Stream / Subscription 随之失效。
func (e *Engine) Start() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	if err := verifyABI(); err != nil {
		return err
	}

	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil
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

	if e.decodePolicy != nil {
		var allowFallback int32
		if e.decodePolicy.AllowSoftwareFallback {
			allowFallback = 1
		}
		cPolicy := C.Zhulong_decode_policy_t{
			backend:                 C.int32_t(e.decodePolicy.Backend),
			allow_software_fallback: C.int32_t(allowFallback),
			max_sessions:            C.int32_t(e.decodePolicy.MaxSessions),
		}
		if status := C.Zhulong_engine_configure_decode(e.handle, &cPolicy); status != C.Zhulong_OK {
			C.Zhulong_engine_stop(e.handle)
			C.Zhulong_engine_destroy(e.handle)
			e.handle = nil
			e.running = false
			e.mu.Unlock()
			return mapNativeStatus("configure_decode", int32(status))
		}
	}

	e.running = true
	e.stopping = false
	e.nativeStopped = false
	e.mu.Unlock()
	return nil
}

// Stop 停止 Native 引擎：中止在途探测、关闭全部物理流，并等待原生工作线程汇合。
// 只有汇合成功后才执行暂存的 cgo.Handle 释放；已停机时为空操作。
func (e *Engine) Stop() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	return e.stopLocked()
}

// stopLocked 执行一次完整的停机排空；调用方必须已持有 lifecycleMu。
func (e *Engine) stopLocked() error {
	e.mu.Lock()
	alreadyStopped := !e.running && (e.handle == nil || e.nativeStopped)
	if alreadyStopped {
		e.mu.Unlock()
		return nil
	}
	e.stopping = true
	e.running = false
	handle := e.handle
	e.mu.Unlock()

	// 1. 中止所有私有探测任务
	for _, pCtx := range e.activeProbeContexts() {
		pCtx.stop()
	}

	// 2. 关闭并退订所有物理流持有者
	for _, s := range e.activeStreamSet() {
		// 流的注销错误在这里无可上报之处：物理流即将随引擎一起消失，
		// 终态由各自的订阅通道保证可见。
		_ = s.Close()
	}

	// 3. 停止常驻引擎实例（回收底层工作线程、排空回调）
	status := C.Zhulong_status_t(C.Zhulong_OK)
	if handle != nil {
		status = C.Zhulong_engine_stop(handle)
	}

	e.mu.Lock()
	e.stopping = false
	e.nativeStopped = status == C.Zhulong_OK
	e.mu.Unlock()

	// 只有原生工作线程全部 join 之后，才允许释放暂存的 cgo.Handle。
	if status == C.Zhulong_OK {
		e.drainPendingTokens()
	}

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

// ConfigureDecode 配置全局解码策略。
func (e *Engine) ConfigureDecode(policy DecodePolicy) error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	e.mu.Lock()
	e.decodePolicy = &policy
	handle := e.handle
	running := e.running
	e.mu.Unlock()

	if !running || handle == nil {
		return nil
	}

	var allowFallback int32
	if policy.AllowSoftwareFallback {
		allowFallback = 1
	}

	cPolicy := C.Zhulong_decode_policy_t{
		backend:                 C.int32_t(policy.Backend),
		allow_software_fallback: C.int32_t(allowFallback),
		max_sessions:            C.int32_t(policy.MaxSessions),
	}

	if status := C.Zhulong_engine_configure_decode(handle, &cPolicy); status != C.Zhulong_OK {
		return mapNativeStatus("configure_decode", int32(status))
	}
	return nil
}

// GetDecodeStats 查询原生解码器运行态统计。
func (e *Engine) GetDecodeStats() (DecodeStats, error) {
	e.handleMu.RLock()
	defer e.handleMu.RUnlock()

	e.mu.Lock()
	handle := e.handle
	running := e.running
	e.mu.Unlock()

	if !running || handle == nil {
		return DecodeStats{}, ErrNotRunning
	}

	var cStats C.Zhulong_decode_stats_t
	if status := C.Zhulong_engine_get_decode_stats(handle, &cStats); status != C.Zhulong_OK {
		return DecodeStats{}, mapNativeStatus("get_decode_stats", int32(status))
	}

	return DecodeStats{
		ActiveSessions:    int(cStats.active_sessions),
		MaxSessions:       int(cStats.max_sessions),
		RejectedRequests:  int(cStats.rejected_requests),
		HardwareAvailable: cStats.hardware_available == 1,
	}, nil
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

	stopErr := e.stopLocked()
	if stopErr != nil {
		return stopErr
	}

	e.handleMu.Lock()
	e.mu.Lock()
	handle := e.handle
	e.handle = nil
	e.running = false
	e.stopping = false
	e.mu.Unlock()
	if handle != nil {
		C.Zhulong_engine_destroy(handle)
	}
	e.drainPendingTokens()
	e.handleMu.Unlock()

	return nil
}

// Acquire 获取或复用物理 RTSP 流。
func (e *Engine) Acquire(ctx context.Context, uri string, consumer Consumer, options StreamOptions) (*Stream, error) {
	if err := validateRTSPURI(uri); err != nil {
		return nil, err
	}
	if err := validateConsumer(consumer); err != nil {
		return nil, err
	}
	// Acquire 是同步、不可异步取消的 CGO 调用，必须把 ctx deadline 收敛进
	// native open timeout，否则底层阻塞会超出调用方给定的 ctx 预算。
	cOptions, err := convertStreamOptions(ctx, options, true)
	if err != nil {
		return nil, err
	}

	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning: true,
		deniedError:    ErrNotRunning,
	})
	if err != nil {
		return nil, err
	}
	defer release()

	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))

	var streamID C.Zhulong_stream_id
	st := C.Zhulong_stream_acquire(
		lease.handle,
		cURI,
		&cOptions,
		C.uint64_t(consumer.ID),
		C.int32_t(consumer.Kind),
		&streamID,
	)
	if st != C.Zhulong_OK {
		return nil, mapNativeStatus("acquire", int32(st))
	}

	stream := &Stream{
		engine:     e,
		id:         streamID,
		consumerID: consumer.ID,
		kind:       consumer.Kind,
		url:        uri,
		generation: lease.generation,
	}

	if err := e.adoptStream(lease, stream); err != nil {
		return nil, err
	}
	return stream, nil
}

// adoptStream 把刚创建好的流登记进停机排空集合。
//
// ⚠️ 原生 acquire 与登记之间引擎可能已经停机或重建代次：此时必须回滚原生侧的
// 消费者引用，否则该流的生命周期就脱离了 Go 侧管理（停机排空列表里根本没有它）。
func (e *Engine) adoptStream(lease handleLease, stream *Stream) error {
	e.mu.Lock()
	handleMissing := e.handle == nil
	notRunning := !e.running || e.stopping
	generationChanged := e.generation != lease.generation
	if handleMissing || notRunning || generationChanged {
		e.mu.Unlock()
		// 回滚失败无可上报之处：引擎已停机，该引用会随 Zhulong_engine_destroy 一并释放。
		// 用显式 `_ =` 而不是裸调用，让「已检查并有意丢弃」与「忘了接返回值」在代码上可区分。
		_ = C.Zhulong_stream_release(lease.handle, stream.id, C.uint64_t(stream.consumerID))
		return ErrNotRunning
	}
	e.streamsMu.Lock()
	e.activeStreams[stream] = struct{}{}
	e.streamsMu.Unlock()
	e.mu.Unlock()
	return nil
}

func (e *Engine) getStreamStatus(gen uint64, streamID C.Zhulong_stream_id) (StreamStatus, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireGeneration: &gen,
		deniedError:       ErrStaleResource,
	})
	if err != nil {
		return StreamStatus{}, err
	}
	defer release()

	var cStatus C.Zhulong_stream_status
	st := C.Zhulong_stream_get_status(lease.handle, streamID, &cStatus)
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

// subscribe 注册视频包订阅。
func (e *Engine) subscribe(
	gen uint64,
	streamID C.Zhulong_stream_id,
	consumerID uint64,
	sub *Subscription,
) (C.Zhulong_subscription_id, uintptr, error) {
	return e.subscribeWith(gen, "subscribe", sub,
		func(handle C.Zhulong_engine_h, token C.uintptr_t, out *C.Zhulong_subscription_id) C.Zhulong_status_t {
			return C.Zhulong_stream_subscribe(
				handle,
				streamID,
				C.uint64_t(consumerID),
				C.Zhulong_packet_callback(C.zhulongPacketCallbackBridge),
				token,
				out,
			)
		})
}

// subscribeAIResults 注册 AI 推理结果订阅。
func (e *Engine) subscribeAIResults(
	gen uint64,
	streamID C.Zhulong_stream_id,
	sub *AIResultSubscription,
) (C.Zhulong_subscription_id, uintptr, error) {
	return e.subscribeWith(gen, "subscribe_ai_results", sub,
		func(handle C.Zhulong_engine_h, token C.uintptr_t, out *C.Zhulong_subscription_id) C.Zhulong_status_t {
			return C.Zhulong_stream_subscribe_ai_results(
				handle,
				streamID,
				C.Zhulong_ai_result_callback(C.zhulongAIResultCallbackBridge),
				token,
				out,
			)
		})
}

// subscribeWith 在句柄读锁保护下注册一条订阅；失败时立即释放 token。
//
// 注册与注销共用 handleMu 的读侧，因此永远不会与 Close 的 destroy 重叠。
func (e *Engine) subscribeWith(
	gen uint64,
	op string,
	owner any,
	call func(handle C.Zhulong_engine_h, token C.uintptr_t, out *C.Zhulong_subscription_id) C.Zhulong_status_t,
) (C.Zhulong_subscription_id, uintptr, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return 0, 0, err
	}
	defer release()

	h := cgo.NewHandle(owner)
	token := uintptr(h)

	var subID C.Zhulong_subscription_id
	if status := call(lease.handle, C.uintptr_t(token), &subID); status != C.Zhulong_OK {
		h.Delete()
		return 0, 0, mapNativeStatus(op, int32(status))
	}
	return subID, token, nil
}

// unsubscribe 注销视频包订阅。
//
// 生产路径由 Subscription.Close 与 AIResultSubscription.Close 各自内联对应的
// 原生调用（它们需要子资源持有者的 id/streamID）；这里是包级订阅的注销入口，
// 也被 engine_test.go 的 token 释放时机用例用作测试缝。
func (e *Engine) unsubscribe(
	gen uint64,
	streamID C.Zhulong_stream_id,
	subID C.Zhulong_subscription_id,
	releaseToken func(),
) error {
	return e.unsubscribeWith(
		gen,
		"unsubscribe",
		func(handle C.Zhulong_engine_h) C.Zhulong_status_t {
			return C.Zhulong_stream_unsubscribe(handle, streamID, subID)
		},
		releaseToken,
	)
}

// releaseTiming 描述注销返回之后 cgo.Handle 的释放时机。
type releaseTiming int

const (
	// releaseNow：原生已确认同步排空（或本就无此订阅），可以立即释放。
	releaseNow releaseTiming = iota
	// releaseAfterStop：底层工作线程可能仍在跑，必须等停机汇合之后再释放。
	releaseAfterStop
)

// planTokenRelease 把原生注销状态码翻译成 token 释放时机。
//
// ⚠️ 默认分支必须保守：停机中（ERR_NOT_RUNNING）与任何未知状态下宁可晚一点释放
// （停机时统一排空），也绝不提前 Delete 让残余回调命中已删除的 cgo.Handle，
// 更不往漏掉不管。
func planTokenRelease(status int32) releaseTiming {
	switch StatusCode(status) {
	case StatusOK, StatusNotFound:
		return releaseNow
	default:
		return releaseAfterStop
	}
}

// unsubscribeWith 在句柄读锁保护下注销订阅，并决定 cgo.Handle 的释放时机。
//
// token 释放规则见 planTokenRelease：目的是既不让残余回调命中已删除的句柄，
// 也不在未知状态下把 token 丢弃泄漏。
func (e *Engine) unsubscribeWith(
	gen uint64,
	op string,
	call func(handle C.Zhulong_engine_h) C.Zhulong_status_t,
	releaseToken func(),
) error {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireGeneration: &gen,
		deniedError:       ErrStaleResource,
	})
	if err != nil {
		// 代次不符说明句柄已换：token 无论如何都得走释放路径，否则 cgo.Handle 泄漏。
		releaseToken()
		return err
	}
	defer release()

	status := call(lease.handle)
	if planTokenRelease(int32(status)) == releaseNow {
		releaseToken()
	} else {
		e.deferTokenDrain(releaseToken)
	}

	if status != C.Zhulong_OK {
		return mapNativeStatus(op, int32(status))
	}
	return nil
}

func (e *Engine) releaseStream(gen uint64, streamID C.Zhulong_stream_id, consumerID uint64) error {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireGeneration: &gen,
		deniedError:       ErrStaleResource,
	})
	if err != nil {
		return err
	}
	defer release()

	st := C.Zhulong_stream_release(lease.handle, streamID, C.uint64_t(consumerID))
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

// convertStreamOptions 把 StreamOptions 转为 C 层选项。
//
// clampToContext 语义见 clampToDeadline：异步可取消的调用（Probe）传 false，
// 同步阻塞的调用（Acquire）传 true。
func convertStreamOptions(
	ctx context.Context,
	options StreamOptions,
	clampToContext bool,
) (C.Zhulong_stream_options, error) {
	if options.Transport != TransportTCP && options.Transport != TransportUDP {
		return C.Zhulong_stream_options{}, fmt.Errorf(
			"%w: invalid transport %d",
			ErrInvalidArgument,
			options.Transport,
		)
	}

	openTimeoutMs, err := normalizeTimeoutMs(options.OpenTimeout)
	if err != nil {
		return C.Zhulong_stream_options{}, err
	}
	if clampToContext {
		openTimeoutMs, err = clampToDeadline(ctx, openTimeoutMs)
		if err != nil {
			return C.Zhulong_stream_options{}, err
		}
	}

	idleTimeoutMs, err := normalizeTimeoutMs(options.IdleTimeout)
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
		return VideoInfo{}, fmt.Errorf(
			"%w: extradata size %d exceeds limit %d",
			ErrPacketTooLarge,
			int(view.extradata_size),
			MaxExtraDataBytes,
		)
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

// BroadcastAIMessage 向指定流的 FrameBroker 投递一条带类型的控制载荷
// （如 MSG_GALLERY_*），以触达已连接的算法 runner；streamID 为 0 时广播给全部活跃流。
func (e *Engine) BroadcastAIMessage(ctx context.Context, streamID uint64, msgType uint16, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(payload) > algoipc.MaxIpcPayloadSize {
		return fmt.Errorf(
			"%w: payload exceeds %d limit (%d bytes)",
			ErrInvalidArgument,
			algoipc.MaxIpcPayloadSize,
			len(payload),
		)
	}

	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning: true,
		deniedError:    ErrNotRunning,
	})
	if err != nil {
		return err
	}
	defer release()

	var ptr unsafe.Pointer
	if len(payload) > 0 {
		ptr = unsafe.Pointer(&payload[0])
	}

	st := C.Zhulong_engine_broadcast_ai_message(
		lease.handle,
		C.Zhulong_stream_id(streamID),
		C.uint16_t(msgType),
		ptr,
		C.size_t(len(payload)),
	)
	runtime.KeepAlive(payload)

	if st != C.Zhulong_OK {
		return mapNativeStatus("broadcast_ai_message", int32(st))
	}
	return nil
}
