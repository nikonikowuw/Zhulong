# 执行计划：Native 节点化解码器接入与流水线架构重构

## 任务拆解与验证步骤

- [x] **Step 1: 实现并验证通用有界队列 `BoundedQueue`**
  - 文件：`native/src/pipeline/bounded_queue.hpp`
  - 交付：支持容量限制、`DROP_OLDEST` 溢出淘汰策略、超时等待与协作式取消 `cancel()`。
  - 验证：编写内部轻量测试验证 Drop-Oldest 淘汰和优雅停机唤醒。

- [x] **Step 2: 目录与职责模块化解耦重构**
  - 拆分重构：
    - `native/src/pipeline/subscription.hpp` & `.cpp`：负责数据包订阅通道、回调状态机与同步排空（Drain）；
    - `native/src/pipeline/stream.hpp` & `.cpp`：负责单个物理流拓扑、Worker 线程与消费者生命周期；
    - `native/src/pipeline/engine.hpp` & `.cpp`：精炼为顶层流池管理、Reaper 清理线程与 Probe。
  - 验证命令：更新 CMakeLists.txt 源码列表，执行 `make native-test` 确保原所有 6 项测试完全通过，重构零破坏。

- [x] **Step 3: 实现解码抽象与 FFmpeg 软解节点**
  - 文件：
    - `native/src/nodes/decode/decoder.hpp`
    - `native/src/nodes/decode/ffmpeg_decoder.hpp`
    - `native/src/nodes/decode/ffmpeg_decoder.cpp`
  - 交付：支持 H.264 / H.265 解码，产出装配好 `release_fn` 的 `HardwareFrame`。
  - 验证：测试用例直接喂入真实 H.264 关键帧与非关键帧，成功产出对应尺寸的 `HardwareFrame`。

- [x] **Step 4: 将解码节点接入 `Stream` 流水线并支持按需激活**
  - 文件：`native/src/pipeline/stream.hpp` & `stream.cpp`
  - 交付：当有 `Zhulong_CONSUMER_AI` 接入时启动解码线程；从 Capture 获取 Packet 送入解码队列，输出帧进入极浅 FrameQueue。
  - 验证：测试消费者引用生命周期与按需拉起/休眠逻辑。

- [x] **Step 5: 单元测试集成与全链路回归**
  - 文件：`native/tests/decoder_test.cpp`，更新 `native/CMakeLists.txt`
  - 验证命令：`make native-build && make native-test && make go-check`
  - 验收：所有测试全项通过，无内存泄漏与死锁。
