# 原生流水线目录架构规范 (C++)

> C++ 处理引擎目录规划、C ABI 公开头文件边界与单向依赖规则。

---

## 1. 真实工程目录拓扑

当前 `native/` 已落地的目录与模块如下：

```text
native/
  include/Zhulong/
    engine.h                    # 唯一公开纯 C ABI 头文件 (提供给 Go/CGO 引用)
    frame.hpp                   # 原生帧数据结构契约
  sdk/
    abi.json                    # 插件 ABI 元数据契约
    include/zhulong/            # 插件纯 C ABI (plugin.h) 与现代 C++17 Header-only SDK (algo.hpp, algo_main.hpp, regions.hpp, platform/, runtime/)
    src/                        # 算法包入口骨架 (algo_main.cpp) 与内部辅助组件 (gallery_*, motion_gate, result_serializer, stall_watch, task_regions, worker_guard)
    tools/                      # 包工程脚手架 (new_package.py) 与黑盒验包探针 (algo_probe.py)
  src/
    abi/                        # C ABI 包装实现 (engine.cpp, snapshot.cpp：句柄转换、全异常拦截)
    algorithm/
      ipc/                      # FrameBroker 共享内存池、Unix Socket 与二进制 IPC 协议
      runtime/                  # 公共运行库 HAL (CPU 视觉桩、后处理、底库注册、Letterbox、运行时工厂)
    backends/                   # 厂商 SDK 适配层，物理隔离专有头文件：
      onnx/                     # ONNX Runtime 推理后端
      rknn/                     # Rockchip RKNN NPU 推理后端
      rga/                      # Rockchip RGA 硬件前处理后端
    common/                     # 跨节点共享契约与工具 (log.*, mpp/mpp_api_types.hpp, path_utils.hpp, view_window.hpp)
    nodes/                      # 媒体处理节点：
      capture/                  # RTSP 采集与 FFmpeg 所有权管理
      decode/                   # 统一解码节点契约 (IDecodeNode)、工厂、FFmpeg 软解与 MPP 动态装载硬解
      preprocess/               # 分析窗口裁剪 (window_crop)
      snapshot/                 # 抓拍 JPEG 编码器 (软编与 MPP 硬编)
    pipeline/                   # 顶层引擎、物理流池、有界队列、订阅、音视频归一化、运动检测与帧发布器
    platform/dmabuf/            # DMA-BUF 分配与同步原语
  profiles/                     # 目标平台交叉编译配置 (rk3568-rknn.json, rk3568-rknn-rga.json, rk3568-stub.json 等)
  scripts/                      # 构建包装脚本 (build.py)、依赖与包漂移校验 (check_package_drift.py)、板端验证脚本
  tests/                        # C/C++ 单测、RTSP/RTP 回环集成测试、ABI 镜像一致性测试与 Go 桥接回归脚本
  vendor/                       # 交叉编译所需的第三方头文件与运行库 (mpp, rga, rknpu2)
```

> **核心约定**：
> - `src/common/mpp/mpp_api_types.hpp` 为跨节点共享的 MPP 类型/枚举/函数表镜像，一致性由 `native/tests/mpp_abi_conformance_test.cpp` 编译期断言守护。
> - 基础构建与主机测试不要求厂商 SDK；可选硬件后端及其专有头文件由 `src/backends/` 隔离，并由对应 profile、SDK/BSP 和目标板环境单独启用与验证。
> - 新功能未开始前严禁预先创建空目录占位。

---

## 2. 单向流转与边界铁律

当前依赖流向严格单向，无反向依赖：

```txt
include/Zhulong/engine.h ➔ src/abi ➔ src/pipeline ➔ src/nodes/{capture, decode, preprocess, snapshot}
                                                  ➔ src/algorithm/ipc (FrameBroker)
sdk/ (plugin.h / algo.hpp) ➔ src/algorithm/runtime ➔ src/backends/{onnx, rknn, rga}
```

1. **唯一对外公开头文件**：`include/Zhulong/engine.h` 是 Go/CGO **唯一允许引入的原生头文件**，严禁引入 C++ 容器、模板或厂商 SDK 头文件。
2. **ABI 异常防爆门**：`src/abi` 必须在最外层以 `try { ... } catch (...) { return Zhulong_ERR_INTERNAL; }` 包覆，严禁 C++ 异常穿越到 CGO。
3. **节点解耦流转**：节点之间通过中立数据契约与有界队列传递不可变包/帧，不直接窥探其他节点内部状态；详见 [接入合同](./ingestion-contract.md) 与 [流水线规范](./pipeline-guidelines.md)。
4. **厂商 SDK 物理隔离**：特定 NPU/GPU 专有头文件只能在 `src/backends/` 内引用，严禁污染核心流水线与公开头文件契约。

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
