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

## 3. 不透明句柄模式与桥接契约

### C 侧公开头文件 (`include/Zhulong/engine.h`)

```c
#ifndef Zhulong_ENGINE_H
#define Zhulong_ENGINE_H
#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Zhulong_engine_t* Zhulong_engine_h;

typedef enum {
    Zhulong_OK = 0,
    Zhulong_ERR_INVALID_PARAM = -1,
    Zhulong_ERR_OUT_OF_MEMORY = -2,
    Zhulong_ERR_PIPELINE_INIT = -3,
    Zhulong_ERR_INTERNAL      = -99
} Zhulong_status_t;

Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h* out_engine);
void           Zhulong_engine_destroy(Zhulong_engine_h engine);
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine, const char* rtsp_url);
void           Zhulong_engine_stop(Zhulong_engine_h engine);

#ifdef __cplusplus
}
#endif
#endif
```

### Go 侧桥接包装 (`internal/engine/engine.go`)

```go
package engine

/*
#cgo CFLAGS: -I../../native/include
#cgo LDFLAGS: -L../../native/build -lZhulong_engine
#include <stdlib.h>
#include "Zhulong/engine.h"
*/
import "C"
import (
 "fmt"
 "unsafe"
)

type Engine struct { handle C.Zhulong_engine_h }

func NewEngine() (*Engine, error) {
 var h C.Zhulong_engine_h
 if rc := C.Zhulong_engine_create(&h); rc != C.Zhulong_OK {
  return nil, fmt.Errorf("engine_create failed: %d", int(rc))
 }
 return &Engine{handle: h}, nil
}

func (e *Engine) Start(url string) error {
 cURL := C.CString(url)
 defer C.free(unsafe.Pointer(cURL))
 if rc := C.Zhulong_engine_start(e.handle, cURL); rc != C.Zhulong_OK {
  return fmt.Errorf("engine_start failed: %d", int(rc))
 }
 return nil
}

func (e *Engine) Close() {
 if e.handle != nil {
  C.Zhulong_engine_destroy(e.handle)
  e.handle = nil
 }
}
```

---

## 4. 线程绑定与异步回调守则

1. **线程绑定 API (`runtime.LockOSThread`)**：若底层涉及对线程局部存储（TLS）敏感的 NPU/GPU 上下文，调用该 CGO 接口的 goroutine 必须在调用前执行 `runtime.LockOSThread()`，退出时 `defer runtime.UnlockOSThread()`。
2. **异步回调使用 `cgo.Handle`**：若 C 侧需要异步上报事件，在 Go 侧通过 `cgo.NewHandle(obj)` 创建安全令牌并传给 C，回调时通过 `//export` 函数传回并由 Go 解析，完成后显式 `handle.Delete()`，严禁在 C 侧缓存 Go 原生指针。
