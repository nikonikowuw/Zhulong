# 需求文档：Native 节点化解码器接入与流水线架构重构

## 1. 目标 (Goal)

随着硬件帧对象 `HardwareFrame` 的就绪，Native 引擎需要从单纯的“拉流解复用器”演进为真正的“媒体处理流水线（Media Processing Pipeline）”。
本任务的目标是：
1. **重构代码架构与目录职责**：理顺 `native/src/pipeline/` 内部代码结构，消除 `engine.cpp` 包含全生命周期的巨石结构，建立清晰的 `nodes/`（原子节点）与 `pipeline/`（管道拓扑与流池）职责边界；
2. **实现统一解码节点与软解后端**：在 `nodes/decode/` 中实现统一解码抽象 `IDecodeNode`，并基于内嵌静态 FFmpeg 实现 `FFmpegDecodeNode`，产出标准的 `HardwareFrame`；
3. **建立线程隔离与有界队列流转**：实现 CaptureNode ➔ DecodeNode 的异步解耦，提供送往 AI 消费者的极浅有界帧队列（容量 2~3 帧，溢出 Drop Oldest），确保拉流网络、解码运算互不阻塞。

---

## 2. 核心需求 (Requirements)

### 2.1 架构分层与目录重构 (Architecture & Directory Layout)
- **R1.1 节点原子化 (`src/nodes/`)**：
  - 维持 `src/nodes/capture/` 负责网络/设备拉流与协议解复用；
  - 新增 `src/nodes/decode/` 负责格式解码，提供 `decoder.hpp`（抽象接口）与 `ffmpeg_decoder.hpp/.cpp`（实现）；
- **R1.2 管道拓扑与控制器解耦 (`src/pipeline/`)**：
  - 将原本堆叠在 `engine.hpp/cpp` 里的物理流生命周期、订阅排空机制与顶层引擎做模块化拆分：
    - `stream.hpp / stream.cpp`：管理单路物理流的节点拓扑（Capture ➔ Packet 分发 ➔ Decode ➔ Frame 分发）；
    - `engine.hpp / engine.cpp`：顶层多流池（Stream Pool）、并发保护、全局宽限期回收（Reaper）与独立探测（Probe）；
- **R1.3 C ABI 100% 兼容**：严格保持公开 C ABI（`native/include/Zhulong/engine.h`）与外部 Go 调用的二进制与行为兼容性，原有的 RTSP 探测、连接池复用与数据包借用订阅逻辑不受影响。

### 2.2 统一解码节点定义与 FFmpeg 软解实现 (DecodeNode & FFmpeg Backend)
- **R2.1 `IDecodeNode` 抽象契约**：
  - 纯 C++ 抽象基类，定义 `send_packet(...)`、`receive_frame(HardwareFrame::Ptr& out)` 与 `flush()`；
  - 支持工厂创建：根据编解码格式（H.264 / H.265）实例化对应后端；
- **R2.2 `FFmpegDecodeNode` 软解实现**：
  - 基于已校验内嵌的静态 FFmpeg（`libavcodec.a`, `libavutil.a`）；
  - 完整解析 SPS/PPS/VPS 参数集与 extradata，正确初始化 `AVCodecContext`；
  - 产出标准的 `HardwareFrame`，正确填充 `width`, `height`, `stride`, `vstride`, `host_ptr`, `pts`；
  - 绑定专属 `release_fn`：在帧析构时安全归还底层 `AVFrame`，杜绝内存泄漏。

### 2.3 异步有界队列与背压防死锁 (Bounded Queues & Backpressure)
- **R2.3 采集与解码线程隔离**：
  - 拉流 Worker 线程仅负责收包，向解码器的 Packet 队列（容量 30~60）推送，不阻塞网络接收；
- **R2.4 AI 消费者 Frame 队列与 Drop-Oldest 机制**：
  - 解码后产生的 `HardwareFrame` 进入极浅帧队列（容量 2~3）；
  - 当队列满时执行 Drop-Oldest（丢弃队列头部的最老帧），保证 AI 消费者始终处理最新实时画面；
  - 被丢弃的帧在析构时立即触发 `release_fn` 归还解码缓冲区。

---

## 3. 非功能性约束 (Non-Functional Requirements)

1. **离线与零额外依赖**：仅使用现有已通过工具链指纹校验的静态 FFmpeg 库，不引入任何未受控外部第三方依赖。
2. **异常安全与严格停机时序**：遵循 5 步优雅停机规范，析构时安全唤醒队列条件变量并 `join` 解码线程，无死锁、无悬挂线程。
3. **平台与 CI 友好**：FFmpeg 软解可在标准 Linux x86/ARM64 宿主开发机和 CI 上直接运行并全绿通过。

---

## 4. 验收标准 (Acceptance Criteria)

- [ ] **AC-1 (目录重构与编译通过)**：完成 `src/nodes/decode/` 与 `src/pipeline/` 的职责拆分，`make native-build` 静态库编译通过，无符号冲突与警告。
- [ ] **AC-2 (解码功能与帧属性验证)**：基于已有 RTSP 循环测试桩或单元测试输入 H.264/H.265 码流包，成功解码出有效 `HardwareFrame`，验证宽、高、步长、PTS 及 `release_fn` 触发正常。
- [ ] **AC-3 (背压与丢帧防泄露)**：模拟下游慢速消费场景，验证极浅帧队列在溢出时正确丢弃最旧帧，且被丢弃帧内存即时回收，解码器持续工作无内存泄露与阻塞挂死。
- [ ] **AC-4 (回归兼容性)**：原有全套测试（`ZhulongEngineLifecycle`, `ZhulongCAbiLifecycle`, `ZhulongCaptureUnit`, `ZhulongFrameLifecycle`, `ZhulongRtspIntegration`, `ZhulongBuildContract`）与 Go bridge 测试全部通过。
