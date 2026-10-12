# Native Pipeline 回调隔离、丢包可观测性与文档同步修正

## 1. 目标与用户价值

修正 `native/src/pipeline/` 中被测试盲区掩盖的**回调异常隔离**缺陷，补齐解码丢包的**可观测性**，并清理死代码、让文档与代码重新对齐。

用户价值：10-10 算法插件任务将引入**原生 C++ 算法消费者**，本任务是它的前置清障——在接入更多 C++ 订阅者之前，先保证订阅通道互相隔离。

## 2. 背景与已确认事实

### 2.1 当前实现

- 单物理流 `Stream` 通过 `subscriber_snapshot_`（不可变快照）向 N 个订阅者扇出数据包，回调在**锁外**调用（`native/src/pipeline/stream.cpp:424-436`）。
- `DecodeSession` 随 `Zhulong_CONSUMER_AI` 消费者生灭，内部有 `packet_queue_`(60, `DROP_NEWEST`) 与 `frame_queue_`(3, `DROP_OLDEST`)。
- C ABI 层有 `boundary()` 异常防火墙（`native/src/abi/engine.cpp:44-58`）与 `in_callback` 重入拦截。

### 2.2 关键事实（已通过代码与测试核实）

| 事实 | 证据 |
|---|---|
| Go 回调 panic **不会**穿越 C++ 边界 | `internal/engine/cgo_bridge.go:24-26` 有 `defer func(){ _ = recover() }()` |
| `engine.h` 明确规定回调**严禁抛 C++ 异常** | `native/include/Zhulong/engine.h:161` |
| `Subscription::invoke` 异常时**已**置 `enabled=false` 并恢复 `in_callback`，随后 `throw` | `native/src/pipeline/subscription.cpp:25-33` |
| `invoke` 的生产调用点**仅** `Stream::dispatch` 一处 | `native/src/pipeline/stream.cpp:433` |
| `engine.h` **无**帧获取类导出符号（12 个导出中无 frame 相关） | `native/include/Zhulong/engine.h:180-335` |
| `BoundedQueue::reset()` 全仓**零调用** | `grep -rn "reset()" native/src native/tests` 仅命中 `decoder.reset`/`frame.reset`/`consumer.reset` |
| `stream.cpp` **未**使用任何 `mem*`/`str*` 函数 | `grep -nE "\bmemcpy\b|\bmemset\b|\bstrlen\b" native/src/pipeline/stream.cpp` 无命中 |
| `OverflowStrategy::BLOCK` 生产代码**零使用**（仅默认参数） | `native/src/pipeline/bounded_queue.hpp:19,27,52` |
| 集成测试已支持同流两个 Preview 消费者 | `native/tests/rtsp_integration_test.cpp` 中 `release(first,1)` / `release(first,2)` |
| 现有「拥塞重同步」测试通过 | `cd build/native/host && ./ZhulongDecoderTests` → `All Decoder & BoundedQueue tests passed successfully!` |

### 2.3 已排除的假设（重要的负面结论）

以下三条曾被怀疑为缺陷，经复核**均不成立，本任务不修改**：

**(a) 「解码线程关键帧门控与生产者重同步信号竞态导致花屏」—— 证伪。**

`packet_queue_` 是 FIFO，且丢弃方向与出队方向相反：

```txt
入队 →  [p1][p2]...[p60]   ← 溢出时 clear() 丢最新侧
出队 ←  decode pop 最旧侧
```

在途包 `pN` 的参考帧（`p1..pN-1`）早已被 pop 并解码进 DPB；被丢弃的包（`pN+1..p60`）在解码顺序上永远晚于 `pN`，**不可能**是其参考。故在途包永远可正常解码，最坏产出 1 帧陈旧画面（有界、自愈），不是花屏。

双门控实为**两个不同关注点的正确设计**：

| 门控 | 位置 | 保护对象 |
|---|---|---|
| `waiting_keyframe_`（生产者） | `stream.cpp:84,112` | **队列占用**——防非关键帧灌满队列把 IDR 挤掉 |
| `need_keyframe`（解码线程） | `stream.cpp:154` | **DPB 有效性**——解码器重建/`flush()`/解码错误后必须等 IDR |

补充证据：`flush()` 仅在解码错误路径调用（`stream.cpp:264,271`），溢出路径不 flush。

**(b) 「`Stream::start()` 首行 `stop_decode_session(sync_decode_pipeline())` 是恒空操作」—— 证伪。**

`native/src/pipeline/engine.cpp:180-185`：`consumers.emplace(consumer, kind)` 发生在 `stream->start()` **之前**。当首个消费者即为 `Zhulong_CONSUMER_AI` 时，`has_ai_consumer()` 为真，**会真正创建 `DecodeSession`**。

**(c) 「`decode_loop` 开头的 `create_or_get_decoder(0, 1, 90000)` 是恒空预初始化」—— 证伪。**

运行中流新增 AI 消费者时（`engine.cpp:171`），`video_meta_` 已由 `on_ready()`（`stream.cpp:396-408`）填充，`video_meta_.view.codec > 0` → **会真正提前建好解码器**。这是有意的启动优化。

## 3. 需求

### R1 (P0) — Preview 回调异常逐订阅者隔离

- **现象**：`Stream::dispatch`（`native/src/pipeline/stream.cpp:431-436`）在 `for` 循环内调用 `subscription->invoke(packet)`。`Subscription::invoke` 恢复自身状态后 `throw`（`native/src/pipeline/subscription.cpp:32`），异常穿透整个 `for` 循环 → 穿透 `read_rtsp` 的 `consume` lambda → 被 `Stream::capture()` 的 `catch (...)` 捕获（`stream.cpp:454`），流被置为 `Zhulong_STREAM_FAILED` + `Zhulong_ERR_INTERNAL`。
- **后果**：
  1. 迭代顺序中位于违约订阅者**之后**的订阅者，收不到**当前**数据包；
  2. `capture` worker 退出 → 整条流终结 → **同流所有订阅者永久断流**。
- **影响范围（已核实）**：**仅 C++ 调用方**。Go 侧回调经 `recover()` 兜底，panic 不越界；当前唯一抛异常的调用方是测试 `native/tests/rtsp_integration_test.cpp:390-406`。真实风险面向 10-10 引入的原生算法消费者。
- **需求**：在 `Stream::dispatch` 的扇出循环内**逐订阅者**隔离异常。违约订阅者仅自身失效（`Subscription::invoke` 已置 `enabled=false`），其余订阅者继续正常收包。
- **契约依据**：抛异常是调用方违约（`engine.h:161`），不应惩罚同一流上的其他订阅者。
- **已决（D1）**：违约时**不**将流置为 `FAILED`。流继续运行，违约订阅者静默失效。理由：订阅通道互相独立，一个订阅者的 bug 不构成流的故障；`capture` worker 不退出，其他订阅者无感知。
- **连带改动**：`native/tests/rtsp_integration_test.cpp:390-406` 的 `throwing_callback_case` 必须**重写**——不再断言 `FAILED + ERR_INTERNAL`，改为断言「抛异常订阅者静默失效、同流其他订阅者持续收包、流状态保持 `RUNNING`」。

### R2 (P2) — 丢包与回调异常可观测性（C++ 内部，不新增 C ABI）

- **现象**：两处静默失败无任何计数或状态：
  1. `decode_loop` 中 `create_or_get_decoder` 返回 `nullptr` 时（`native/src/pipeline/stream.cpp:236-238`）静默 `recycle_buffer`。触发路径：`selected_codec <= 0`（永久丢包）、指数退避窗口内（最长 5s，`stream.cpp:150-151`）、解码器创建抛异常。
  2. R1 修复后，被隔离的回调异常若不记录，违约订阅者将完全静默。
- **需求**：在 `DecodeSession` / `Stream` 内增加 **C++ 内部**计数器，供单元测试断言与后续运维查询。**不新增 C ABI 导出符号，不改 `engine.h`。**
- **已决（D2）**：范围限定为 C++ 内部计数器，不跨层暴露到 Go。理由：跨层需新增 ABI 符号与 `internal/engine` 改动，显著扩大改动面，而当前无 Go 侧消费需求。
- **计数维度**：解码器不可用丢包数、关键帧门控丢包数、拥塞重同步次数、回调异常次数。

### R3 (P1) — 清理死代码

- 删除 `BoundedQueue::reset()`（`native/src/pipeline/bounded_queue.hpp:127-136`）：全仓零调用，且 `clear()` 的注释（`bounded_queue.hpp:139`）明确表明 `reset()` 已被有意取代（"不改变取消标记……停机竞态下不会复活已取消队列"）。
- 删除 `native/src/pipeline/stream.cpp:15` 的 `#include <cstring>`：零使用。

### R4 (P1) — 文档与代码对齐

| 位置 | 现文 | 修正方向 |
|---|---|---|
| `native/README.md:170` | `Software H.264/HEVC decoders are compiled for stream-info, **not exposed as a continuous decode pipeline**.` | 已过时：现有完整连续解码流水线（`DecodeSession` + `decode_loop` + `frame_queue_`） |
| `native/README.md:143` | `There is no async packet queue or pre-decode dropping.` | 需限定作用域：C ABI 层确无异步包队列，但 C++ 内部存在 `packet_queue_` 与关键帧丢包 |
| `native/README.md:199` | `...is **inapplicable to a capture-only pipeline**` | 已过时：不再是 capture-only |
| `.trellis/spec/native/pipeline-guidelines.md:42` | `packet_queue_.reset()` | 实际代码为 `packet_queue_.clear()`（语义不同） |
| `.trellis/spec/native/pipeline-guidelines.md:36` | `Decode ➔ Storage (录像) \| ... \| 背压阻塞唤醒` | 该路径**未实现**，`OverflowStrategy::BLOCK` 生产零使用，应标注为规划中 |

### 已决事项汇总（D1–D3）

| ID | 决策 | 理由 |
|---|---|---|
| **D1** | R1 违约订阅者抛异常时，流**不**置 `FAILED`，其余订阅者继续收包 | 订阅通道互相独立；一个订阅者违约不构成流的故障。需重写 `throwing_callback_case` |
| **D2** | R2 范围限定 C++ 内部计数器，不新增 C ABI、不动 `internal/engine` | 避免跨层改动面扩大；当前无 Go 侧消费需求 |
| **D3** | `pipeline-guidelines.md:36` 的「录像背压」行**改标为规划中**，不删除 | 保留设计意图，同时消除「已实现」的误导 |

## 4. 验收标准

- [x] **AC-1（订阅者隔离）**：同一条物理流上注册两个 Preview 订阅者，其一回调抛 C++ 异常；验证另一订阅者**持续收到数据包**，抛异常订阅者静默失效（后续不再收到回调），且**流状态保持 `RUNNING` 而非 `FAILED`**，进程不崩溃、无死锁。
- [x] **AC-2（丢包可观测）**：验证 R2 各计数器在对应路径上正确递增，且在无丢包/无异常场景保持为 0。
- [x] **AC-3（死代码清理）**：删除 `BoundedQueue::reset()` 与 `#include <cstring>` 后，全量 Native 测试通过。
- [x] **AC-4（文档一致性）**：`README.md` 与 `pipeline-guidelines.md` 的表述与实际代码行为逐条对齐，无残留过时描述。
- [x] **AC-5（全量回归）**：`make native-test` 全部测试通过；`make go-check`（CGO 桥接）通过；`-Wall -Wextra -Wpedantic` 零告警。

## 5. 范围外

| 项 | 原因 |
|---|---|
| 解码线程侧同步 `waiting_keyframe_` 门控 | 经证伪非缺陷（见 2.3a）；双门控是不同关注点的正确设计 |
| `Stream::start()` 的 `sync_decode_pipeline()`、`decode_loop` 的预初始化 | 经证伪非死代码（见 2.3b/2.3c） |
| `HardwareFrame` 软解多平面契约（`data[1]/data[2]`、`linesize[1]/[2]` 缺失） | 属 10-10 算法插件任务的前置设计，不应混入缺陷修复 |
| `HardwareFrame` 增加 `dma_fd` 平台标记与回收策略字段 | 待 RK3568 板卡到位、MPP 后端定型后一并设计 |
| `acquire_frame` 无 C ABI 导出 | 符合 10-10 的 FrameBroker 设计意图（"像素不绕 Go"） |
| 新增任何 C ABI 导出符号 | 本任务仅动 C++ 内部与文档 |
| `BoundedQueue` 无超时 `pop(T&)` 重载 | 测试在用，属合理 API 面，保留 |
| 真实摄像头 / 板端 / RK3568 运行验证 | 无设备与 sysroot，本任务只做 Linux 主机验证 |

## 6. 阻塞性开放问题

无。D1–D3 已由用户确认（"采用你的推荐"）。

## 7. 验证命令

```bash
# Native 全量（含 RTSP loopback、纯 C ABI、构建合同）
make native-test

# Go/CGO 桥接回归
make go-check

# 零告警静态检查
INC=$(dirname $(dirname $(find build/deps/ffmpeg -path "*install/include/libavcodec/avcodec.h" | head -1)))
for f in native/src/pipeline/*.cpp; do
  g++ -std=c++17 -Wall -Wextra -Wpedantic -fsyntax-only \
      -I native/src -I native/include -I "$INC" "$f"
done

# Sanitizer（无死锁/竞争验证）
python3 native/scripts/build.py --sanitizer thread test
python3 native/scripts/build.py --sanitizer address test
```
