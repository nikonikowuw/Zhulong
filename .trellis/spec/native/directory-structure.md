# 原生流水线目录架构规范 (C++)

> C++ 处理引擎目录规划、C ABI 公开头文件边界与单向依赖规则。

---

## 1. 规划目录结构（Proposed Layout）

> ⚠️ 以下为指导后续实现的规划约定，功能未开始前严禁创建空目录。

```text
native/
  include/
    Zhulong/engine.h            # 唯一公开纯 C ABI 头文件 (提供给 Go/CGO 引用)
  src/
    abi/engine.cpp            # C ABI 包装实现 (句柄转换、全异常拦截)
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

```txt
include/Zhulong/engine.h (纯 C ABI) ➔ src/abi ➔ src/nodes ➔ src/pipeline (契约/队列) ➔ src/backends (厂商 SDK)
```

1. **唯一对外公开头文件**：`include/Zhulong/engine.h` 是 Go/CGO **唯一允许引入的原生头文件**，严禁引入 C++ 容器、模板或厂商 SDK 头文件。
2. **ABI 异常防爆门**：`src/abi` 必须在最外层以 `try { ... } catch (...) { return Zhulong_ERR_INTERNAL; }` 包覆，严禁 C++ 异常穿越到 CGO。
3. **节点解耦流转**：各节点严禁直接互相调用或窥探内部状态，只能通过有界队列单向传递 `std::shared_ptr<const Frame>` 或 `std::shared_ptr<const Packet>`。
4. **厂商 SDK 物理隔离**：特定 NPU/GPU 专有头文件只能在 `src/backends/` 内引用，严禁污染核心流水线契约。
