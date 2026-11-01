package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

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
