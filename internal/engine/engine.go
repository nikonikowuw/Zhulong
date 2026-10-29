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

// probeContext 持有一次独立探测的私有引擎实例。
//
// ⚠️ 生命周期：stop 与 destroy 必须互斥（mu）。stopInternal 会拿着从 activeProbes
// 取出的快照逐个 stop，而探测 goroutine 自己也会 stop 并在返回时 destroy ——
// 没有这把锁，两者交错就会对已释放的句柄调用 stop（Use-After-Free）。
type probeContext struct {
	mu        sync.Mutex
	engine    C.Zhulong_engine_h
	destroyed bool
}

// stop 请求私有引擎停止；已销毁时为空操作。
func (p *probeContext) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.destroyed || p.engine == nil {
		return
	}
	C.Zhulong_engine_stop(p.engine)
}

// destroy 是私有引擎的终点：先排空 stop 语义，再释放句柄。
func (p *probeContext) destroy() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.destroyed {
		return
	}
	p.destroyed = true
	if p.engine == nil {
		return
	}
	C.Zhulong_engine_stop(p.engine)
	C.Zhulong_engine_destroy(p.engine)
	p.engine = nil
}

// handle 返回底层句柄，仅供调用方在自身持有生命周期保证时使用。
func (p *probeContext) handle() C.Zhulong_engine_h {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.engine
}

// probeConcurrency 是同时在途的独立探测上限。
const probeConcurrency = 4

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

	logHandle cgo.Handle
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

// handleLease 是一次跨 CGO 访问对底层句柄的租约。
//
// 调用方释放返回值之前，所有使用 handle 的 CGO 调用都落在 handleMu 读锁保护区内，
// 因此必然排在 Engine.Close 取写锁之前、Zhulong_engine_destroy 之前。
type handleLease struct {
	handle     C.Zhulong_engine_h
	generation uint64
}

// handleRequirement 描述一次跨 CGO 调用对引擎状态的前置要求。
//
// 三个条件的不同组合覆盖全部调用点，差异必须显式写出来，borrowHandle 不做任何缺省推断；
// deniedError 是必填项（nil 会被当作“拒绝时返回 nil 错误”的悖论）。
//
// 尤其 requireRunning：停机排空路径（releaseStream / unsubscribeWith）**故意**不要求
// 运行态 —— 无条件加上会让关流退化成 ErrNotRunning，token 便会堆到下次排空。
type handleRequirement struct {
	requireRunning    bool
	requireGeneration *uint64 // nil 表示不校验代次
	deniedError       error   // 必填：前置条件不满足时返回的哨兵
}

// borrowHandle 取出句柄租约与解锁函数；成功时调用方必须 defer release()。
func (e *Engine) borrowHandle(req handleRequirement) (handleLease, func(), error) {
	e.handleMu.RLock()

	e.mu.Lock()
	lease := handleLease{handle: e.handle, generation: e.generation}
	running := e.running && !e.stopping
	e.mu.Unlock()

	handleMissing := lease.handle == nil
	notRunning := req.requireRunning && !running
	generationChanged := req.requireGeneration != nil && lease.generation != *req.requireGeneration
	if handleMissing || notRunning || generationChanged {
		e.handleMu.RUnlock()
		return handleLease{}, nil, req.deniedError
	}
	return lease, e.handleMu.RUnlock, nil
}

// runningGeneration 在运行态下返回当前代次；未运行或停机中返回 ErrNotRunning。
func (e *Engine) runningGeneration() (uint64, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning: true,
		deniedError:    ErrNotRunning,
	})
	if err != nil {
		return 0, err
	}
	release()
	return lease.generation, nil
}

// activeProbeContexts 快照当前在途的私有探测实例。
func (e *Engine) activeProbeContexts() []*probeContext {
	e.probesMu.Lock()
	defer e.probesMu.Unlock()
	probes := make([]*probeContext, 0, len(e.activeProbes))
	for pCtx := range e.activeProbes {
		probes = append(probes, pCtx)
	}
	return probes
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

	e.running = true
	e.stopping = false
	e.nativeStopped = false
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

// Probe 对指定 RTSP 流执行独立的视频元数据探测。
//
// 每个探测使用隔离的私有 Native Engine 实例，并发上限为 probeConcurrency。
// 上限已满时**阻塞等待**（或被 ctx 取消），不做投机性排队。
// 调用方应始终传入带 deadline 的 ctx，否则可能长期等待。
func (e *Engine) Probe(ctx context.Context, uri string, options StreamOptions) (VideoInfo, error) {
	if err := validateRTSPURI(uri); err != nil {
		return VideoInfo{}, err
	}
	// Probe 是异步可取消的（runProbe 内 select ctx.Done()），因此**不**把 ctx deadline
	// 收敛进 native open timeout：收敛会被毫秒截断，让 native 超时先于 ctx 到期，
	// 使取消语义退化为调度时序竞态（返回 TIMEOUT 而非 DeadlineExceeded）。
	cOptions, err := convertStreamOptions(options, ctx, false)
	if err != nil {
		return VideoInfo{}, err
	}

	gen, err := e.runningGeneration()
	if err != nil {
		return VideoInfo{}, err
	}

	select {
	case e.probeTokens <- struct{}{}:
	case <-ctx.Done():
		return VideoInfo{}, ctx.Err()
	}
	defer func() { <-e.probeTokens }()

	if err := ctx.Err(); err != nil {
		return VideoInfo{}, err
	}

	// 令牌排队期间引擎可能已经重建，必须重新核对代次。
	current, err := e.runningGeneration()
	if err != nil {
		return VideoInfo{}, err
	}
	if current != gen {
		return VideoInfo{}, ErrNotRunning
	}

	pCtx, err := newProbeContext()
	if err != nil {
		return VideoInfo{}, err
	}
	defer pCtx.destroy()

	if err := e.registerProbe(pCtx); err != nil {
		return VideoInfo{}, err
	}
	defer e.unregisterProbe(pCtx)

	return runProbe(ctx, pCtx, uri, cOptions)
}

// newProbeContext 创建并启动一个隔离的私有引擎实例。
func newProbeContext() (*probeContext, error) {
	var handle C.Zhulong_engine_h
	if status := C.Zhulong_engine_create(&handle); status != C.Zhulong_OK {
		return nil, mapNativeStatus("probe_create", int32(status))
	}

	pCtx := &probeContext{engine: handle}
	if status := C.Zhulong_engine_start(handle); status != C.Zhulong_OK {
		pCtx.destroy()
		return nil, mapNativeStatus("probe_start", int32(status))
	}
	return pCtx, nil
}

// registerProbe 把私有引擎登记到停机排空集合；停机中则拒绝。
func (e *Engine) registerProbe(pCtx *probeContext) error {
	e.probesMu.Lock()
	defer e.probesMu.Unlock()
	if e.probesClosed {
		return ErrNotRunning
	}
	if e.activeProbes == nil {
		e.activeProbes = make(map[*probeContext]struct{})
	}
	e.activeProbes[pCtx] = struct{}{}
	return nil
}

func (e *Engine) unregisterProbe(pCtx *probeContext) {
	e.probesMu.Lock()
	delete(e.activeProbes, pCtx)
	e.probesMu.Unlock()
}

// runProbe 在一个隔离的私有引擎上执行探测，并保证 ctx 取消优先于原生超时。
//
// ⚠️ 返回前一定会 join 探测 goroutine：pCtx.destroy() 由调用方的 defer 执行，
// 此时绝不可能还有 CGO 调用在途。
func runProbe(ctx context.Context, pCtx *probeContext, uri string, options C.Zhulong_stream_options) (VideoInfo, error) {
	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))

	type probeResult struct {
		result C.Zhulong_probe_result_h
		status C.Zhulong_status_t
	}
	resChan := make(chan probeResult, 1)
	handle := pCtx.handle()

	go func() {
		var out C.Zhulong_probe_result_h
		st := C.Zhulong_engine_probe(handle, cURI, &options, &out)
		resChan <- probeResult{result: out, status: st}
	}()

	var res probeResult
	select {
	case res = <-resChan:
	case <-ctx.Done():
		// 停掉私有引擎让阻塞中的探测尽快返回，再回收结果；取消语义以 ctx 为准。
		pCtx.stop()
		res = <-resChan
	}

	if res.result != nil {
		defer C.Zhulong_probe_result_destroy(res.result)
	}
	if err := ctx.Err(); err != nil {
		return VideoInfo{}, err
	}
	if res.status != C.Zhulong_OK {
		return VideoInfo{}, mapNativeStatus("probe", int32(res.status))
	}
	return extractVideoInfo(res.result)
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
	cOptions, err := convertStreamOptions(options, ctx, true)
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
	st := C.Zhulong_stream_acquire(lease.handle, cURI, &cOptions, C.uint64_t(consumer.ID), C.int32_t(consumer.Kind), &streamID)
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
		C.Zhulong_stream_release(lease.handle, stream.id, C.uint64_t(stream.consumerID))
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
func (e *Engine) subscribe(gen uint64, streamID C.Zhulong_stream_id, consumerID uint64, sub *Subscription) (C.Zhulong_subscription_id, uintptr, error) {
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
func (e *Engine) subscribeAIResults(gen uint64, streamID C.Zhulong_stream_id, sub *AIResultSubscription) (C.Zhulong_subscription_id, uintptr, error) {
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
func (e *Engine) unsubscribe(gen uint64, streamID C.Zhulong_stream_id, subID C.Zhulong_subscription_id, releaseToken func()) error {
	return e.unsubscribeWith(gen, "unsubscribe",
		func(handle C.Zhulong_engine_h) C.Zhulong_status_t {
			return C.Zhulong_stream_unsubscribe(handle, streamID, subID)
		},
		releaseToken,
	)
}

// unsubscribeAIResults 注销 AI 推理结果订阅。
func (e *Engine) unsubscribeAIResults(gen uint64, streamID C.Zhulong_stream_id, subID C.Zhulong_subscription_id, releaseToken func()) error {
	return e.unsubscribeWith(gen, "unsubscribe_ai_results",
		func(handle C.Zhulong_engine_h) C.Zhulong_status_t {
			return C.Zhulong_stream_unsubscribe_ai_results(handle, streamID, subID)
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

// brokerPathBufferBytes 是 FrameBroker socket 路径的接收缓冲。
// Unix domain socket 路径长度受 sockaddr_un.sun_path 限制（Linux 108 字节），
// 256 留出足够余量；超出时原生侧会返回 ERR_BUFFER_TOO_SMALL。
const brokerPathBufferBytes = 256

func (e *Engine) getBrokerPath(gen uint64, streamID C.Zhulong_stream_id) (string, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return "", err
	}
	defer release()

	var buf [brokerPathBufferBytes]C.char
	inoutLen := C.size_t(len(buf))
	st := C.Zhulong_stream_get_broker_path(lease.handle, streamID, &buf[0], &inoutLen)
	if st != C.Zhulong_OK {
		return "", mapNativeStatus("get_broker_path", int32(st))
	}
	return C.GoString(&buf[0]), nil
}

func (e *Engine) setPublishRate(gen uint64, streamID C.Zhulong_stream_id, fps uint32) error {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return err
	}
	defer release()

	st := C.Zhulong_stream_set_publish_rate(lease.handle, streamID, C.uint32_t(fps))
	if st != C.Zhulong_OK {
		return mapNativeStatus("set_publish_rate", int32(st))
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
func convertStreamOptions(options StreamOptions, ctx context.Context, clampToContext bool) (C.Zhulong_stream_options, error) {
	if options.Transport != TransportTCP && options.Transport != TransportUDP {
		return C.Zhulong_stream_options{}, fmt.Errorf("%w: invalid transport %d", ErrInvalidArgument, options.Transport)
	}

	openTimeoutMs, err := normalizeTimeoutMs(options.OpenTimeout)
	if err != nil {
		return C.Zhulong_stream_options{}, err
	}
	if clampToContext {
		openTimeoutMs, err = clampToDeadline(openTimeoutMs, ctx)
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

// 人脸特征提取不在这里：它从「engine 里 dlopen 人脸插件」改成了「跟包可执行文件
// 说一个小协议」，实现在 internal/faceextract。留在这里的只会是两套模型加载路径，
// 而且宿主根本不该认识算法二进制。

// BroadcastAIMessage 向指定流的 FrameBroker 投递一条带类型的控制载荷
// （如 MSG_GALLERY_*），以触达已连接的算法 runner；streamID 为 0 时广播给全部活跃流。
func (e *Engine) BroadcastAIMessage(ctx context.Context, streamID uint64, msgType uint16, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(payload) > algoipc.MaxIpcPayloadSize {
		return fmt.Errorf("%w: payload exceeds %d limit (%d bytes)", ErrInvalidArgument, algoipc.MaxIpcPayloadSize, len(payload))
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
