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

func (e *Engine) ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)
```

`Start` 在持锁下创建 C-owned handle 并启动；启动失败时销毁 handle。`Stop` 不销毁句柄；幂等 `Close` 停止并销毁。Go 只保存不透明句柄值，不向 C 侧暴露可逃逸的 Go 指针。C++ 静态库由 `make native-build` 构建到 `build/native/host/libZhulongEngine.a`。Go 命令必须通过 `native/scripts/build.py go ...` 或 Make：脚本传递 Engine 内容摘要路径、FFmpeg 三个静态归档及目标系统库，避免 Go 外部归档缓存过期。交叉产物使用独立目录，不覆盖 host。

句柄生命周期防护与并发控制：
- **句柄操作读写锁隔离 (`handleMu sync.RWMutex`)**：所有跨 CGO 访问底层 `handle` 的调用均在读锁保护下执行（`handleMu.RLock()` / `handleMu.RUnlock()`）；`Close()` 销毁 `handle` 前必须获取写锁（`handleMu.Lock()`）彻底排空在途调用，消除野指针与 Use-After-Free 隐患。
- **停机注销延迟排空**：若退订发生在底层引擎停机期间（返回 `Zhulong_ERR_NOT_RUNNING`），严禁立即调用 `deleteToken()`，必须暂存并在底层工作线程全部 `join()` 汇合完成后（`stopInternal` 之后）统一释放，防止残余回调在注销后访问非法 `cgo.Handle`。

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

---

## 6. 单次人脸特征提取（离线录入通道）

- **C ABI**：`Zhulong_engine_extract_face_feature` 接收紧凑图像像素内存（RGB24/BGR24），输出 512 维 `float32` 归一化特征向量。
- **内存规则**：Go 侧在调用期间将连续图像切片首地址转为 `*C.uint8_t`，通过 `runtime.KeepAlive` 保证调用期间不被 GC 回收；输出由 Go 分配 512 维切片，C 侧原地写入，禁止跨调用缓存 Go 指针。
- **业务解耦**：上层业务包（如 `internal/person`）禁止 `import "C"`，必须通过 `FaceFeatureExtractor` 接口解耦，由 Uber Fx 注入 `*engine.Engine`。

### 6.1 能力来源：算法插件动态加载（dlopen）而非静态链接

真实推理（SCRFD 检测 → Umeyama 5 点对齐 → 512 维特征抽取）全部由算法插件 `libFaceRecognitionPlugin.so` 通过专用 C ABI 提供：

```c
zhulong_status_t zhulong_face_extract_feature(const uint8_t*, int32_t, int32_t, int32_t, int32_t, float*, uint32_t*);
void             zhulong_face_extract_release(void);
```

- 宿主 `native/src/abi/engine.cpp` 的 `FacePluginLoader` 单例懒加载该插件：`libZhulongEngine.a` 与 `zhulong` 可执行文件**绝不**产生 `libonnxruntime.so` 的 `DT_NEEDED` 依赖，宿主在缺失 ORT 的机器上仍可启动并服务 Web/流媒体；
- 寻址优先级：`ZHULONG_FACE_PLUGIN_PATH`（**显式覆盖，设置后即为唯一候选，不再静默回退**）→ `ZHULONG_PLUGIN_DIR` → 构建产物目录 → `dladdr` 同目录；
- 底库录入与视频流识别必须使用同一份模型权重（`det_500m.onnx` + `facelivtv2-l.onnx`），保证两侧特征处于同一度量空间。

### 6.2 Fail-Closed 闭环报错（严禁静默退化）

| 场景 | 插件状态码 | Engine 状态码 | Go 侧错误 | HTTP |
| :--- | :--- | :--- | :--- | :--- |
| 成功 | `ZHULONG_PLUGIN_OK` | `Zhulong_OK` | `nil` | 200 |
| 插件缺失 / 符号缺失 / 模型不可用 | *(dlopen 失败)* / `ERR_INTERNAL` | `Zhulong_ERR_UNSUPPORTED` / `ERR_INTERNAL` | `ErrUnsupported` / `ErrInternal` | 500 |
| 检出结果来自 CPU 降级桩（`is_real_detection == false`） | `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED` | `Zhulong_ERR_UNSUPPORTED` | `ErrUnsupported` | 500 |
| 真实推理但未检出人脸（含全黑图） | `ZHULONG_PLUGIN_ERR_NOT_FOUND` | `Zhulong_ERR_NOT_FOUND` | `ErrNotFound` | 400 `NO_FACE_DETECTED` |
| 参数非法（空指针 / 尺寸 / stride / 容量 < 512） | `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT` | `Zhulong_ERR_INVALID_ARGUMENT` | `ErrInvalidArgument` | 400 |

⚠️ **严禁回退到数学模拟特征**：任何一条失败路径都不允许写出向量，否则会向人员底库写入无效特征并让后续视频流识别永久失灵。检测器在真实 9 头 SCRFD 路径上**不得**再用 CPU 桩伪造检出框（`FaceDetector::detect` 的桩回退只允许出现在非 9 头输出，即 CPU/桩后端）。

### 6.3 TTL 空闲回收与生命周期汇合

- 首次请求懒加载并进入热态（单张推理 10~25ms）；空闲超过 `ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC`（默认 60 秒）后，后台 Reaper 线程调用 `zhulong_face_extract_release` 再 `dlclose`，把约 80MB 模型常驻内存交还操作系统；下次请求自动重新唤醒加载。
- **并发契约**：`FacePluginLoader::extract()` 全程持有互斥锁，Reaper 与 `shutdown()` 不可能在推理执行途中卸载代码段（Use-After-Unload）。
- `Zhulong_engine_destroy` 在**最后一个**引擎句柄销毁时同步停止 Reaper 并卸载；`Zhulong_engine_destroy(NULL)` 是无操作，不得递减引擎引用计数（否则会误卸载仍在使用的插件）。

### 6.4 验证边界

- `ZhulongFaceFeatureExtract` 在 ONNX 后端断言真实人脸样本产出 512 维单位特征，在 CPU 桩后端断言 Fail-Closed（`UNSUPPORTED`）；两个分支由 CMake 宏 `ZHULONG_TEST_ONNX_INFERENCE` 区分。
- `ZhulongFacePluginTtl` 使用最小插件替身（`native/tests/mock_face_plugin.cpp`）黑盒验证「TTL 卸载 → 抽走插件文件后报 `UNSUPPORTED` → 恢复文件后重新唤醒」。


