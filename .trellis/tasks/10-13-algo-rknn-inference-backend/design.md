# RKNN 推理后端：技术设计

依据 `prd.md` R1–R9。证据来源：`research/rk3568-board-evidence.md`（真机只读实测）与父任务 `10-10-algorithm-plugin-architecture/research/rockchip-context-sharing.md`（官方头文件声明）。接口名称与参数是实现建议，不是已存在 API。

## 1. 边界与接入点

RKNN 后端**替换** `IInferenceBackend` 的实现，不改动接口本身，也不改动插件 ABI（`zhulong_plugin_api_v1`）。

⚠️ 本任务与兄弟任务 `10-13-algo-rga-vision-backend` 共用同一套后端选择机制（推理侧 / 视觉侧两个正交开关），且都会触碰 `native/CMakeLists.txt` 与 `native/scripts/build.py`。**必须串行执行，本任务在前**。

现有调用链（已确认）：

```text
native/sdk/include/zhulong/algo.hpp:84   → get_default_inference_backend()
native/tests/algo_runtime_test.cpp:60    → get_default_inference_backend()
                                            ↓
native/src/algorithm/runtime/runtime_factory.cpp
   static auto backend = std::make_shared<CpuInferenceBackend>();   ← 当前硬编码
```

`runtime_factory.cpp` 是**唯一**的后端选择点，因此后端分流只需改这一处，插件与 SDK 完全无感。

⚠️ 注意 `algo.hpp` 通过 `#include <algorithm/runtime/runtime_factory.hpp>` 直接依赖 runtime 私有头。这意味着插件编译期就绑定了 `runtime_factory` 的符号——RKNN 后端必须链接进 `libzhulong_algo_runtime.so`，不能作为独立 `.so` 由插件单独加载。

## 2. 目录与依赖隔离

```text
native/src/backends/rknn/          ← 新建；RKNN 专有头文件唯一允许出现处
  rknn_api_loader.hpp/.cpp         ← dlopen/dlsym 或直接链接，见 §5
  rknn_inference_backend.hpp/.cpp  ← IInferenceBackend 实现
  rknn_error_map.hpp/.cpp          ← int 返回码 → zhulong_status_t
  rknn_model_asset.hpp             ← ModelAsset / SessionContext 派生
```

依据 `.trellis/spec/native/directory-structure.md:44,62`：厂商 SDK 专有头文件只能在 `src/backends/` 内引用。CMake 中该目录的 `target_include_directories` 带 `PRIVATE`，确保 `rknn_api.h` 不泄漏到 `runtime/` 或 `sdk/include/`。

验收方式（AC1）：

```bash
grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/   # 只应命中 src/backends/rknn/
```

## 3. 后端选择：编译期 profile

**不引入运行期 dlopen 后端选择**（那会让 CPU 构建也依赖 RKNN 符号解析逻辑）。用 CMake 选项在编译期决定：

⚠️ 后端选择是**两个正交开关**：推理与视觉前处理各自独立。兄弟任务 `10-13-algo-rga-vision-backend` 使用 `ZHULONG_VISION_BACKEND`，本任务使用 `ZHULONG_INFERENCE_BACKEND`。可以「RKNN 推理 + CPU 前处理」，也可以「CPU 推理 + RGA 前处理」。

```cmake
# native/CMakeLists.txt 新增
set(ZHULONG_INFERENCE_BACKEND "cpu" CACHE STRING "算法运行库推理后端: cpu | rknn")
set_property(CACHE ZHULONG_INFERENCE_BACKEND PROPERTY STRINGS cpu rknn)

set(ZHULONG_VISION_BACKEND "cpu" CACHE STRING "算法运行库视觉前处理后端: cpu | rga")
set_property(CACHE ZHULONG_VISION_BACKEND PROPERTY STRINGS cpu rga)

if(ZHULONG_INFERENCE_BACKEND STREQUAL "rknn")
    if(NOT ZHULONG_RKNN_ROOT)
        message(FATAL_ERROR "ZHULONG_INFERENCE_BACKEND=rknn 需要 -DZHULONG_RKNN_ROOT=<rknpu2 前缀>")
    endif()
    target_sources(ZhulongAlgoRuntime PRIVATE
        src/backends/rknn/rknn_inference_backend.cpp
        src/backends/rknn/rknn_error_map.cpp)
    target_include_directories(ZhulongAlgoRuntime PRIVATE
        ${ZHULONG_RKNN_ROOT}/include)
    target_link_libraries(ZhulongAlgoRuntime PRIVATE
        ${ZHULONG_RKNN_ROOT}/lib/librknnrt.so)
    target_compile_definitions(ZhulongAlgoRuntime PRIVATE ZHULONG_INFERENCE_RKNN=1)
endif()
```

`runtime_factory.cpp` 用编译期分支：

```cpp
std::shared_ptr<IInferenceBackend> get_default_inference_backend() {
#ifdef ZHULONG_INFERENCE_RKNN
    static auto backend = std::make_shared<RknnInferenceBackend>();
#else
    static auto backend = std::make_shared<CpuInferenceBackend>();
#endif
    return backend;
}
```

⚠️ 这里**不是** `native/profiles/rk3568.json` 的复用。那个 JSON 是**交叉编译工具链** profile（cc/cxx/sysroot/cflags），由 `build.py profile()` 解析并做 `tree_digest` 缓存身份校验。后端选择是 CMake 层的独立开关。两者正交：可以「交叉编译 + cpu 后端」，也可以「板端原生编译 + rknn 后端」。

⚠️ **执行顺序**：本任务先建立这套机制（含 `ZHULONG_VISION_BACKEND` 的声明位置），RGA 任务在其后加入视觉侧分支。两者都改 `native/CMakeLists.txt` 与 `runtime_factory.cpp`，必须串行。

## 4. 库依赖引入：sysroot 污染问题（关键决策）

`build.py configure()` 生成的 toolchain 含：

```cmake
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)   # 只在 sysroot/prefix 里找库
```

而 `build/sysroots/rk3568` 是**干净的 Debian rootfs，不含 RKNN**（实测确认）。同时 `tree_digest(p["sysroot"])` 让 sysroot 内容参与缓存身份——往 sysroot 里塞库会改变哈希，可能触发全量重建。

三个候选方案：

| 方案 | 做法 | 优点 | 代价 |
| --- | --- | --- | --- |
| **A. 独立 RKNN 前缀** | `-DZHULONG_RKNN_ROOT=build/deps/rknn/rknpu2`，从板端 `scp` 出 `include/` + `lib/` | 不动 sysroot；`CMAKE_FIND_ROOT_PATH` 加一个路径即可；与 FFmpeg 的 prefix 模式一致 | 需扩展 `build.py` 支持该前缀 |
| **B. 塞进 sysroot** | 复制到 `build/sysroots/rk3568/usr/include` + `usr/lib` | CMake 无需改动 | 污染 sysroot 哈希；sysroot 与板端真实状态不再一致；后续 `make native-deps` 可能重建 |
| **C. 板端原生编译** | 在 RK3568 上 `cmake` + `make` | 天然匹配板端 gcc 10.2.1 与库版本 | 磁盘仅剩 **2.9G**；需同步源码；`ninja` 缺失只能 Makefile generator；CI 不可复现 |

**建议 A**，理由：

1. sysroot 保持「板端 rootfs 的忠实镜像」这一语义，`tree_digest` 仍然可信。
2. 与既有 FFmpeg 依赖模式一致（`build/deps/ffmpeg` + `zhulong-ffmpeg.json` 清单），可复用同一套 SHA-256 校验思路。
3. 交叉编译与板端原生编译的产物可用 `sha256sum` 对照，作为「同源」证据。

⚠️ 方案 A 需要扩展 `native/scripts/build.py`：新增 RKNN 前缀的获取与校验（`include/rknn_api.h` + `lib/librknnrt.so` 两个 SHA-256），并在 `configure()` 中把该前缀加入 `CMAKE_FIND_ROOT_PATH`。**这是本任务唯一触碰构建脚本的地方**，必须保持离线（从板端 scp 或本地已有副本，不联网下载）。

**版本固定值**（来自真机实测，AC6 校验用）：

```text
rknn_api.h        sha256 = c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a
librknnrt.so      sha256 = d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8
                  版本   = 2.3.2 (429f97ae6b@2025-04-09T09:09:27)
目标驱动          = RKNPU v0.9.8
目标内核          = 5.10.226 / Debian 11 arm64
```

⚠️ **显式排除 `/usr/lib/librknn_api.so`（1.5.2）**。它只有 25 个 `rknn_` 符号，与 2.3.2 的 39 个不同，误链接会在运行期出现难查的符号缺失。构建脚本应在链接后校验产物 `NEEDED` 里只有 `librknnrt.so`。

## 5. 链接方式：直接链接 vs dlopen

| 方式 | 优点 | 代价 |
| --- | --- | --- |
| **直接链接 `librknnrt.so`** | 简单；编译期校验符号存在；`NEEDED` 可见 | 后端进程启动即需库存在；宿主若误链接会硬依赖 |
| **dlopen + dlsym** | 库缺失时优雅降级到 CPU | 符号名手写，编译期无校验；错误延后到运行期 |

**建议直接链接**，因为：

1. profile 是编译期选定的，运行时不应再「猜」后端。
2. 编译期符号校验能在构建阶段暴露 1.5.2/2.3.2 的符号差异，这正是 R8 想防的问题。
3. CPU profile 根本不链接 RKNN，`NEEDED` 里不会出现，AC2 直接可验。

⚠️ 但 `Engine`（`libZhulongEngine.a` → Go 宿主）**绝不能**链接 `librknnrt.so`。它只被 `ZhulongAlgoRuntime`（`.so`）私有链接。AC2 的 `readelf -d` 检查就是守这条线。

## 6. 资源模型

```text
RknnModelAsset : ModelAsset          ← 每包/每模型一个，进程内共享
  ├─ rknn_context root_ctx           ← 根上下文
  ├─ std::mutex   dup_mutex          ← 串行化 rknn_dup_context
  ├─ 模型身份 (路径 + SHA-256)
  └─ 输入/输出 tensor 属性 (从 Runtime 查询，非文件猜测)

RknnSessionContext : SessionContext  ← 每 worker 一个，独占
  ├─ rknn_context ctx                ← rknn_dup_context(&root_ctx, &ctx) 派生
  ├─ 输入 tensor memory (独占)
  ├─ 输出 tensor memory (独占)
  └─ 可变 workspace (独占)
```

**生命周期次序**（R5，⚠️ 顺序错误会导致 use-after-free）：

```text
load_model:       rknn_init(&root_ctx) → rknn_query(tensor 属性) → rknn_query(MEM_SIZE)
create_session:   lock(dup_mutex) → rknn_dup_context(&root_ctx, &ctx) → unlock
forward:          rknn_inputs_set → rknn_run → rknn_outputs_get → 拷贝到 Tensor → rknn_outputs_release
session 销毁:     释放该 session 的 input/output tensor memory → 标记 ctx 已回收
model 销毁:       所有 session 回收后 → rknn_destroy(&root_ctx)
```

- `rknn_dup_context` 两个参数都是**指针**（`rknn_context*`），不能传裸值。
- `dup_mutex` 存在的原因：父任务研究明确「不假定复制接口可并发调用」，2.3.2 文档未给出并发保证。这是保守做法，不是性能优化。
- 根上下文**保守保留**至所有派生实例回收完成。父任务研究 §3 已记录此保守策略。

## 7. 权重共享：本任务的核心验证

`research/rockchip-context-sharing.md` 已证明声明存在（且 SHA-256 与目标板头文件一致），但**行为未验证**。验证方案：

**对照组设计**（AC3）：

| 组 | 做法 | 目的 |
| --- | --- | --- |
| 基线 | N 个独立 `rknn_init` | 对照：N 份权重 |
| 候选 | 1 个 `rknn_init` + (N-1) 次 `rknn_dup_context` | 验证共享 |

**必须同时采集**（缺一项就无法判定）：

1. **正确性**：两组对同一输入的输出逐元素比对（定容差）。
2. **设备内存**：`RKNN_QUERY_MEM_SIZE` 的 `total_weight_size` / `total_internal_size`。
3. **主机内存**：进程 RSS/PSS（`/proc/self/status` 或 `smaps_rollup`）。
4. **销毁行为**：根上下文保留至所有派生回收后销毁，重复启停无泄漏。
5. **SDK 版本**：`RKNN_QUERY_SDK_VERSION` 的实际 `api_version` / `drv_version`。

⚠️ **不要把各 context 的逻辑权重大小相加当作真实物理占用**（父任务研究 L146 行已警告）。物理占用要结合 RSS/PSS 与设备分配指标。

⚠️ **共享不成立时的行为**（R6）：明确返回阻塞/不支持并报告，**不得静默初始化 N 份权重**然后宣称满足共享目标。这是 R3 的硬要求。

**若 `rknn_dup_context` 不满足共享**，按父任务研究 §3 的第 4 条，再评估 `RKNN_FLAG_SHARE_WEIGHT_MEM` + `rknn_set_weight_mem` 的显式共享路线——但**不自行拼接未验证的 flag 组合**，须按版本匹配文档与官方示例。

## 8. Tensor 桥接（⚠️ 现有约束）

`native/src/algorithm/runtime/tensor.hpp` 的 `Tensor` 内部是 `std::vector<uint8_t>` 自有内存：

```cpp
class Tensor {
    std::vector<int64_t> dims_;
    DataType dtype_{DataType::FLOAT32};
    std::vector<uint8_t> data_;
};
```

这带来一个**无法回避的拷贝**：

- RKNN 输入需要把 `Tensor` 的 host 内存交给 `rknn_inputs_set`。
- RKNN 输出经 `rknn_outputs_get` 拿到的是 runtime 管理的 buffer，必须**拷贝**进 `Tensor`（因为 `Tensor` 不能接管外部内存）。

**本任务接受这个拷贝**，理由：

1. 改造 `Tensor` 支持外部内存会让它承担生命周期责任，牵动整个 SDK 与既有插件，超出本任务范围。
2. 零拷贝的正确做法是 DMA-BUF（后续任务），不是「让 `Tensor` 变复杂」。
3. 父任务 `design.md` §5.2 已把 DMA-BUF 列为独立门槛。

⚠️ 因此**不得声称本任务实现了零拷贝**。若后续需要零拷贝，应单独设计 `Tensor` 的 view 语义（`std::span` 风格 + 所有权标记），而不是在本任务里顺手改。

`DataType` 与 RKNN 类型的映射：`FLOAT32→RKNN_TENSOR_FLOAT32`、`UINT8→RKNN_TENSOR_UINT8`、`INT8→RKNN_TENSOR_INT8`、`INT32→RKNN_TENSOR_INT32`、`INT64→RKNN_TENSOR_INT64`。`RKNN_TENSOR_FLOAT16` 与量化类型需按模型属性决定，不硬编码。

## 9. 错误映射

```cpp
zhulong_status_t map_rknn_error(int ret) {
    switch (ret) {
        case RKNN_SUCC:                    return ZHULONG_PLUGIN_OK;
        case RKNN_ERR_PARAM_INVALID:       return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        case RKNN_ERR_MODEL_INVALID:       return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        case RKNN_ERR_CTX_INVALID:         return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        case RKNN_ERR_DEVICE_UNAVAILABLE:  return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
        case RKNN_ERR_MALLOC_FAIL:         return ZHULONG_PLUGIN_ERR_INTERNAL;
        default:                           return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}
```

⚠️ 具体 `RKNN_ERR_*` 枚举名与数值必须从目标板 `rknn_api.h`（SHA-256 已固定）读取确认，不凭记忆写。设备不可用映射为 `NOT_SUPPORTED` 而非 `INTERNAL`，便于上层区分「环境问题」与「代码 bug」。

所有导出路径的 C++ 异常由既有 ABI 防爆门兜底（`.trellis/spec/native/algo-plugin-contract.md` §2「异常防爆门守则」），后端内部额外在 `forward` 处捕获并转换，避免 `std::bad_alloc` 等穿透。

## 10. 多 Worker 与设备并发

RK3568 是**单 NPU 核**（父任务研究 §4）。因此：

- 1/2/4 worker 的对照是**测量**，不是「线性扩展」的承诺。
- 收益方向是 CPU 前处理/后处理与 NPU 执行的**重叠**，不是 NPU 并行。
- 每包 worker 数有界；设备级总并发还要考虑多包竞争（父任务 design §6）。
- **不启用** `RKNN_FLAG_ASYNC_MASK`（父任务研究 §4 明确）。

## 11. 验证路径

| 层 | 命令 | 说明 |
| --- | --- | --- |
| CPU profile 回归 | `make native-test` | 保证未破坏既有路径（AC2） |
| 头文件隔离 | `grep -rl 'rknn_api.h' native/src/ native/sdk/` | 只应命中 `backends/rknn/`（AC1） |
| 宿主无污染 | `readelf -d build/targets/rk3568/Zhulong \| grep NEEDED` | 不得出现 `librknnrt.so`（AC2） |
| 交叉构建 | `make native-build` + `CROSS_PROFILE` | 依既有 wrapper，不裸跑 `go build` |
| 真机部署 | `scp` + 板端执行 | 记录 `RKNN_QUERY_SDK_VERSION` 实际值（AC3） |
| 真机内存 | `/proc/self/smaps_rollup` + `RKNN_QUERY_MEM_SIZE` | 对照组采集（AC3） |
| 真机并发 | 1/2/4 worker 对照 | 输出一致性 + 稳定性（AC4） |
| 故障注入 | 非法模型路径、中途失败、forward 失败 | 资源全回收（AC5） |

⚠️ 板端磁盘仅剩 2.9G，部署路径需精简（只传 `.so` 与必要资源，不传整套构建目录）。

## 12. 残余风险

- `rknn_dup_context` 行为未知；若共享不成立，R3 目标转为阻塞报告，任务交付范围收窄。
- 编译器代差（宿主 14.2.0 / sysroot libstdc++ 10 / 板端 10.2.1）可能产生 ABI 细微差异，需在真机验证。
- `/dev/rga` 与 NPU 设备的权限模型（`video` 组）影响部署，需在部署脚本中处理。
- 板端磁盘紧张可能导致构建失败。
- 未验证功耗/温度，长时间运行的稳定性未知。
