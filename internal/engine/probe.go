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
	"sync"
	"unsafe"
)

// probeContext 持有一次独立探测的私有引擎实例。
//
// ⚠️ 生命周期：stop 与 destroy 必须互斥（mu）。stopLocked 会拿着从 activeProbes
// 取出的快照逐个 stop，而探测 goroutine 自己也会 stop 并在返回时 destroy ——
// 没有这把锁，两者交错就会对已释放的句柄调用 stop（Use-After-Free）。
type probeContext struct {
	mu        sync.Mutex
	engine    C.Zhulong_engine_h
	destroyed bool
}

// stop 请求私有引擎停止；已销毁时为空操作。
//
// ⚠️ 契约（runProbe 依赖它）：stop 必须让在途的 Zhulong_engine_probe 尽快返回。
// 做不到时不会报错，只会表现为 Probe 晚于 ctx 返回 —— 见 runProbe 里的取舍说明。
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

// Probe 对指定 RTSP 流执行独立的视频元数据探测。
//
// 每个探测使用隔离的私有 Native Engine 实例，并发上限为 probeConcurrency。
// 上限已满时**阻塞等待**（或被 ctx 取消），不做投机性排队。
// 调用方应始终传入带 deadline 的 ctx，否则可能长期等待。
//
// 取消语义：ctx 取消后 Probe 会尽力回到调用方，但**不保证**立即返回 ——
// 若原生侧在 stop 之后仍不返回，这里会选择继续等（理由见 runProbe）。
func (e *Engine) Probe(ctx context.Context, uri string, options StreamOptions) (VideoInfo, error) {
	if err := validateRTSPURI(uri); err != nil {
		return VideoInfo{}, err
	}
	// Probe 是异步可取消的（runProbe 内 select ctx.Done()），因此**不**把 ctx deadline
	// 收敛进 native open timeout：收敛会被毫秒截断，让 native 超时先于 ctx 到期，
	// 使取消语义退化为调度时序竞态（返回 TIMEOUT 而非 DeadlineExceeded）。
	cOptions, err := convertStreamOptions(ctx, options, false)
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
func runProbe(
	ctx context.Context,
	pCtx *probeContext,
	uri string,
	options C.Zhulong_stream_options,
) (VideoInfo, error) {
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
		//
		// ⚠️ 第二次等待刻意不加上限，哪怕它可能越过 ctx 的截止时间：一旦超时放弃，
		// 就必须把 pCtx 的所有权移交给 goroutine（由它返回后 destroy），而 Probe 的
		// defer 会立即 destroy —— 两条路径稍一交错就是对在跑的原生调用 destroy 句柄。
		// 用「可能晚返回」换「不会 Use-After-Free」；该等待能被解开的前提见 probeContext.stop 的契约。
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
