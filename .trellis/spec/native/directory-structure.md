# 原生流水线目录架构规范 (C++)

> C++ 处理引擎目录规划、C ABI 公开头文件边界与单向依赖规则。

---

## 1. 当前实现与规划目录

当前实现：
- `src/abi/engine.cpp`：C ABI 异常与句柄边界转换；
- `src/pipeline/`：流水线核心组件解耦
  - `bounded_queue.hpp`：支持 BLOCK / DROP_OLDEST / DROP_NEWEST 策略的线程安全有界队列；
  - `subscription.hpp / .cpp`：数据包订阅上下文与同步排空 (Drain)；
  - `stream.hpp / .cpp`：物理流拓扑、拉流 Worker 与按需异步解码流水线；
  - `engine.hpp / .cpp`：顶层流池管理、Reaper 清理线程与 Probe。
- `src/nodes/capture/rtsp_input.*`：FFmpeg 所有权、URI 与时钟契约；
- `src/nodes/decode/`：
  - `decoder.hpp`：统一解码节点抽象契约 (`IDecodeNode`) 与统一返回码常量 (`decode_ok` / `decode_again` / `decode_backend_failure`)；
  - `decoder_factory.cpp`：`IDecodeNode::create` 后端分流与透明回退，不寄生在任何具体后端里；
  - `ffmpeg_decoder.hpp / .cpp`：单流确定性软解实现 (`FFmpegDecodeNode`)，产出带 RAII `release_fn` 的 `HardwareFrame`；
  - `mpp_loader.hpp / .cpp`：Rockchip MPP 动态装载器与只读符号表 (`MppLoader::Api`)；
  - `mpp_decoder.hpp / .cpp`：Rockchip MPP 硬解实现 (`MppDecodeNode`) 与外部缓冲组引用计数持有者 (`BufferGroup`)。
- `src/common/mpp/mpp_api_types.hpp`：无编译期 SDK 依赖的 MPP 类型/枚举/函数表声明（含 `MppBufferInfo` 镜像）。
  它是**跨节点共享契约**（`nodes/decode` 与 `nodes/snapshot` 都用），因此不放在任何一个节点目录里；
  镜像与厂商头的一致性由 `native/tests/mpp_abi_conformance_test.cpp` 的编译期断言守护。
- `scripts/build.py` 负责依赖、目标和最终 Go 链接；`tests/` 包含 Native、纯 C、真实 loopback RTSP、硬件帧生命周期与解码节点回归。当前基础构建与主机测试不要求厂商 SDK；可选硬件后端及其专有头文件由 `src/backends/` 隔离，并由对应 profile、SDK/BSP 和目标板环境单独启用与验证。

以下仍是后续完整节点架构的规划，功能未开始前严禁创建空目录。

```text
native/
  include/
    Zhulong/engine.h            # 唯一公开纯 C ABI 头文件 (提供给 Go/CGO 引用)
  sdk/
    include/zhulong/
      plugin.h                # 插件纯 C ABI 导出规范 (v1)
      algo.hpp                # 面向开发者的现代化 C++17 Header-only SDK
  src/
    abi/engine.cpp            # C ABI 包装实现 (句柄转换、全异常拦截)
    algorithm/                # 算法插件系统
      runtime/                # 公共运行库 HAL (IInferenceBackend, IVisionBackend, CPU桩)
      runner/                 # 独立算法 Runner 宿主与独立黑盒验包 CLI
    pipeline/                 # 节点基类、流水线编排、中立契约与有界队列
      node.hpp, pipeline.hpp, packet.hpp, frame.hpp, result.hpp, bounded_queue.hpp
    nodes/                    # 具体处理节点 (单向通过有界队列传递)
      capture/                # RTSP/V4L2 视频采集节点
      decode/                 # 硬件/软件解码节点
      preprocess/             # CSC、缩放、Padding 预处理节点
      inference/              # NPU/GPU 推理执行节点
    backends/                 # 厂商 SDK 适配层 (RKNN / CANN / TensorRT 等，隔离专有头文件)
  tests/                      # ABI、流水线背压与节点逻辑测试 (支持脱机合成数据运行)
```

---

## 2. 单向流转与边界铁律

当前已实现依赖：`include/Zhulong/engine.h ➔ src/abi ➔ src/pipeline/engine（编排） ➔ src/nodes/capture`，无反向依赖；采集层只依赖公开中立 C view/内部 FFmpeg。后续具体节点与中立 pipeline 数据契约分层时，保持节点只依赖契约、不互相窥探，厂商适配头仍隔离在 backends。

```txt
未来节点数据契约：src/nodes ➔ src/pipeline（中立契约/队列）
厂商 SDK：只在 src/backends 内包含专有头文件
```

1. **唯一对外公开头文件**：`include/Zhulong/engine.h` 是 Go/CGO **唯一允许引入的原生头文件**，严禁引入 C++ 容器、模板或厂商 SDK 头文件。
2. **ABI 异常防爆门**：`src/abi` 必须在最外层以 `try { ... } catch (...) { return Zhulong_ERR_INTERNAL; }` 包覆，严禁 C++ 异常穿越到 CGO。
3. **节点解耦流转**：未来异步处理节点不得直接窥探其他节点状态，应通过有界队列传递不可变包/帧。当前只有采集层与同步包回调，没有解码队列；详见 [接入合同](./ingestion-contract.md)。
4. **厂商 SDK 物理隔离**：特定 NPU/GPU 专有头文件只能在 `src/backends/` 内引用，严禁污染核心流水线契约。

---

## 3. 节点层命名约定（常量与枚举）

`src/nodes/**` 内部统一使用 `snake_case`，与节点层契约头 `decoder.hpp` 的 `decode_ok` /
`decode_again` / `decode_backend_failure` 以及 `frame.hpp` 的 `frame_plane_count` 保持一致：

| 种类 | 规则 | 示例 |
| --- | --- | --- |
| 常量 | `snake_case`，`constexpr` | `max_sessions`、`packet_retry_sleep_us`、`stride_align` |
| 枚举值（`enum class`） | `snake_case` | `DecoderBackend::automatic`、`JpegBackendKind::hardware`、`DmaState::validated` |
| 类型 / 成员 | `PascalCase` / `trailing_underscore_` | `MppDecodeNode`、`stream_id_` |
| 厂商 ABI 名 | **沿用原名**，不改写 | `MPP_FMT_YUV420SP`、`MPP_ERR_BUFFER_FULL`、`MPP_VIDEO_CodingHEVC` |

- ⚠️ **厂商 ABI 常量必须与 BSP 头逐字一致**（枚举名/数值都对）：它们是指令字，不是风格选择。
  这条与「统一 snake_case」不冲突——`MPP_*` 属于镜像面，不是我们自己起的名字。
- `src/pipeline/**`、`src/common/**`、`src/algorithm/**` 仍有历史 `kCamelCase` / `ALL_CAPS` 常量；
  迁移不强制一次做完，但**新增/重写代码一律按本表**，不要在一个目录里再混出第三种风格。
