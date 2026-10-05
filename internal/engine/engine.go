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
// 静态库与链接参数由 native/scripts/build.py 动态注入（隔离 Host/交叉编译环境，
// 并基于 Engine 静态库的内容哈希提供缓存隔离，防止 Go 的 CGO 缓存误用陈旧二进制）。
#include <Zhulong/engine.h>
*/
import "C"

import (
	"fmt"
	"sync"
)

// Engine 封装 Native C++ 引擎的不透明指针句柄。
//
// 并发安全性：
//   - 内部通过 sync.Mutex 对所有生命周期操作（Start, Stop, Close, Ready）进行互斥串行化，
//     确保 Close() 不会与正在进行的生命周期切换发生数据竞争。
//   - 内部状态 running 用于快速反映当前引擎工作状态。
type Engine struct {
	mu      sync.Mutex         // 保证生命周期操作线程安全的互斥锁
	handle  C.Zhulong_engine_h // 底层 C++ 引擎实例指针句柄（C 侧分配与管理）
	running bool               // 标记引擎是否处于运行状态
}

// New 创建一个未初始化的 Native 引擎包装实例。
//
// 此时底层 C++ 句柄尚未分配，仅在首次调用 Start() 时按需延迟创建。
func New() *Engine {
	return &Engine{}
}

// Start 启动 Native 引擎。
//
// 行为与约定：
//  1. 若底层句柄不存在，则调用 C.Zhulong_engine_create 分配 Native 实例。
//  2. 调用 C.Zhulong_engine_start 启动后台处理线程与流管理调度器。
//  3. 具备幂等性：如果引擎已启动，重复调用立即返回 nil。
//  4. 若启动失败，会自动清理已创建的底层句柄并重置状态，保证状态一致性。
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 延迟初始化：如果尚未分配 C 句柄，先创建底层引擎
	if e.handle == nil {
		var handle C.Zhulong_engine_h
		if status := C.Zhulong_engine_create(&handle); status != C.Zhulong_OK {
			return nativeError("create", status)
		}
		e.handle = handle
	}

	// 启动底层流管理和工作调度线程
	if status := C.Zhulong_engine_start(e.handle); status != C.Zhulong_OK {
		// 启动失败时及时销毁已创建的底层句柄，避免内存泄漏
		C.Zhulong_engine_destroy(e.handle)
		e.handle = nil
		e.running = false
		return nativeError("start", status)
	}
	e.running = true
	return nil
}

// Stop 停止 Native 引擎。
//
// 行为与约定：
//  1. 若引擎未启动或句柄为空，直接返回 nil。
//  2. 调用 C.Zhulong_engine_stop 取消全部活跃拉流与探测任务，等待后台线程退出。
//  3. 保留底层的 handle，后续仍可再次通过 Start() 重新启动。
//  4. 具备幂等性，可重复调用。
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == nil {
		return nil
	}
	status := C.Zhulong_engine_stop(e.handle)
	e.running = false
	if status != C.Zhulong_OK {
		return nativeError("stop", status)
	}
	return nil
}

// Ready 查询当前 Native 引擎是否处于正常运行状态。
//
// 并发安全：通过互斥锁保护，可随时在外部 goroutine 中安全调用。
func (e *Engine) Ready() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Close 完全停止并销毁底层 C++ 引擎实例。
//
// 内存与生命周期说明（⚠️ 必须显式释放）：
//  1. 内部先调用 C.Zhulong_engine_stop 停止所有工作流并回收线程。
//  2. 随后调用 C.Zhulong_engine_destroy 销毁 C 堆内存分配的对象并置空句柄。
//  3. 允许多次重复调用，多次调用是安全的（幂等操作）。
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == nil {
		return nil
	}

	status := C.Zhulong_engine_stop(e.handle)
	C.Zhulong_engine_destroy(e.handle)
	e.handle = nil
	e.running = false
	if status != C.Zhulong_OK {
		return nativeError("stop", status)
	}
	return nil
}

// nativeError 统一将 Native C ABI 返回的状态码格式化为 Go 错误。
func nativeError(operation string, status C.Zhulong_status_t) error {
	return fmt.Errorf("native engine %s failed with status %d", operation, int(status))
}
