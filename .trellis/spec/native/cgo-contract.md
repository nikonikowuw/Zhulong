# CGO 跨语言交互与内存安全契约

> Go 与 C++ 原生引擎之间的 CGO 边界、不透明句柄、指针规则与内存生命周期。

---

## 1. 核心设计原则

1. **热路径批处理 (Batching)**：跨栈切换有固定开销，**严禁逐像素、逐框调用 CGO**，必须以整帧、批处理或流控制粒度调用。
2. **纯 C ABI 隔离**：C++ 必须通过 `extern "C"` 导出 C 兼容符号，禁止向 Go 暴露 C++ 类或模板。
3. **内存归属谁分配谁释放**：
   - Go 侧内存由 GC 管理；C 侧内存（`malloc`/`new`）由 C 显式释放。
   - `C.CString` **必须紧跟 `defer C.free(unsafe.Pointer(cStr))`**。

---

## 2. 严格的 Go-C 指针规则 (cgocheck 强制)

1. **禁止传含 Go 指针的内存**：传给 C 的连续内存中，**严禁包含任何指向 Go 堆对象的指针**。
2. **禁止 C 侧跨调用保留 Go 指针**：C 函数返回后**严禁长期缓存 Go 内存指针**；如需异步必须在 C 侧深拷贝，或由 Go 使用 `runtime.Pinner`。
3. **防止提前 GC**：将 Go 切片传给 C 同步调用时，调用后紧跟 `runtime.KeepAlive(slice)`。

---

## 3. 不透明句柄模式与当前桥接契约

### C 侧公开头文件 (`native/include/Zhulong/engine.h`)

```c
#ifndef ZHULONG_ENGINE_H
#define ZHULONG_ENGINE_H
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Zhulong_engine_t *Zhulong_engine_h;
typedef int32_t Zhulong_status_t;

enum {
    Zhulong_OK = 0,
    Zhulong_ERR_INVALID_ARGUMENT = -1,
    Zhulong_ERR_OUT_OF_MEMORY = -2,
    Zhulong_ERR_INTERNAL = -99
};

Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine);
Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine);
void Zhulong_engine_destroy(Zhulong_engine_h engine);

#ifdef __cplusplus
}
#endif
#endif
```

所有 ABI 函数捕获 C++ 异常；`start` / `stop` 返回状态码，`destroy` 无返回值且空句柄可安全销毁。公开头文件保持纯 C，不暴露 C++ 类型。以上仅示范保留的生命周期子集；新增 probe、stream、subscription 的完整签名以 `native/include/Zhulong/engine.h` 为准，执行合同见 [ingestion-contract.md](./ingestion-contract.md)。

### Go 侧桥接 (`internal/engine/engine.go`)

```go
/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
// 完整静态库与系统依赖由 native/scripts/build.py 的 CGO_LDFLAGS 提供。
#include <stdint.h>
#include <Zhulong/engine.h>

void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet);
*/
import "C"

type Engine struct { ... }
func New() *Engine
func (e *Engine) Start() error
func (e *Engine) Stop() error
func (e *Engine) Ready() bool
func (e *Engine) Close() error

func (e *Engine) Probe(ctx context.Context, uri string, options StreamOptions) (VideoInfo, error)
func (e *Engine) Acquire(ctx context.Context, uri string, consumerID uint64, kind ConsumerKind, options StreamOptions) (*Stream, error)
func (s *Stream) Status(ctx context.Context) (StreamStatus, error)
func (s *Stream) Subscribe(ctx context.Context, options SubscriptionOptions) (*Subscription, error)
func (s *Stream) Close() error
func (s *Subscription) Next(ctx context.Context) (Packet, error)
func (s *Subscription) Done() <-chan struct{}
func (s *Subscription) Err() error
func (s *Subscription) Close() error
```

`Start` 在持锁下创建 C-owned handle 并启动；启动失败时销毁 handle。`Stop` 不销毁句柄；幂等 `Close` 停止并销毁。Go 只保存不透明句柄值，不向 C 侧暴露可逃逸的 Go 指针。C++ 静态库由 `make native-build` 构建到 `build/native/host/libZhulongEngine.a`。Go 命令必须通过 `native/scripts/build.py go ...` 或 Make：脚本传递 Engine 内容摘要路径、FFmpeg 三个静态归档及目标系统库，避免 Go 外部归档缓存过期。交叉产物使用独立目录，不覆盖 host。

---

## 4. 线程绑定与异步回调守则

1. **线程绑定 API (`runtime.LockOSThread`)**：若底层涉及对线程局部存储（TLS）敏感的 NPU/GPU 上下文，调用该 CGO 接口的 goroutine 必须在调用前执行 `runtime.LockOSThread()`，退出时 `defer runtime.UnlockOSThread()`。
2. **异步回调使用 `cgo.Handle`**：若 C 侧需要异步上报事件，在 Go 侧通过 `cgo.NewHandle(obj)` 创建安全令牌并传给 C，回调时通过 `//export` 函数传回并由 Go 解析，完成后显式 `handle.Delete()`，严禁在 C 侧缓存 Go 原生指针。

---

## 5. 当前接入实现的验证边界

- `make native-test` 运行 C++17 生命周期、纯 C ABI、真实本地 RTSP/RTP 和构建合同测试；`make go-check` 包含 Go vet/race 与真实 loopback RTSP 桥接门禁（`run_go_bridge_tests.py`）。
- Go Engine wrapper 已完整实现 Probe 探测（独立私有 Engine 隔离取消）、Stream 物理流复用/宽限期管理、有界深拷贝包订阅（Subscription）、同步排空后释放 `cgo.Handle`。
- 包只在同步回调期间借用，入队前深拷贝至 Go 内存；probe-result view 仅在 result destroy 前有效。注销须 drain 在途回调后才允许 Go 侧 `cgo.Handle.Delete()`。
- stop 取消输入和 probe、join worker/reaper，之后才释放资源。回调内控制 API 返回 CALLBACK_CONTEXT，destroy 由外部所有者串行调用。
- Linux host 与 Native sanitizer 检查不证明真实摄像机、解码、板端或旧系统兼容；具体命令/断言/残余限制见 [接入合同](./ingestion-contract.md)。
