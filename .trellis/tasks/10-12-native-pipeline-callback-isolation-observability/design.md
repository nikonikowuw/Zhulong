# 技术设计：Native Pipeline 回调隔离与可观测性

## 1. 问题重述（First Principles）

**一句话**：一个订阅者的异常，不应终结同一条流上其他订阅者的数据供给。

**根本事实**：

| 类别 | 事实 | 来源 |
|---|---|---|
| 技术不变式 | `packet_queue_` 是 FIFO，丢弃方向与出队方向相反 | `bounded_queue.hpp:85-87`（`pop_front`）、`stream.cpp:113`（`clear` 丢全部） |
| 契约 | 回调抛 C++ 异常是调用方违约，`engine.h` 已明文禁止 | `native/include/Zhulong/engine.h:161` |
| 契约 | `Subscription::invoke` 的重抛语义**已被测试依赖** | `native/tests/capture_unit_test.cpp:84-91` |
| 隔离边界 | Go 侧 `recover()` 已兜底，本缺陷仅暴露给 C++ 调用方 | `internal/engine/cgo_bridge.go:24-26` |

**推论**：修复点必须落在 `Stream::dispatch` 的**扇出循环**，而非 `Subscription::invoke`。

## 2. 边界与责任

```txt
read_rtsp (capture 线程)
   └─ consume lambda → Stream::dispatch        ← 【R1 修复点】逐订阅者 try/catch
        ├─ session->push_packet()              ← 【R2 计数器】解码队列丢包
        └─ for each subscriber:
             subscription->invoke()            ← 保持重抛（契约不变）
                  └─ 用户回调 → 抛异常 → invoke 恢复状态后 rethrow
```

**不修改**：

- `Subscription::invoke` 的重抛语义（`capture_unit_test.cpp:84-91` 依赖）
- `Subscription::disable_and_drain` 的排空语义
- 任何 C ABI 导出符号（`engine.h` 不动）

## 3. R1 设计：回调异常逐订阅者隔离

### 3.1 改动位置

`native/src/pipeline/stream.cpp:431-436`（`Stream::dispatch` 的扇出循环）。

### 3.2 目标代码形态

```cpp
    // 2. 在锁外逐个调用订阅回调
    //    逐订阅者隔离异常：抛异常是调用方违约 (engine.h)，不得中断同流其他订阅者的数据供给
    for (const auto &subscription : *subscribers) {
        if (cancel.stopped.load()) break;
        try {
            subscription->invoke(packet);
        } catch (...) {
            // invoke 已在抛出前恢复 in_callback 并置 enabled=false，
            // 此处仅计数：违约订阅者静默失效，流状态保持 RUNNING (决策 D1)
            callback_exceptions_.fetch_add(1, std::memory_order_relaxed);
        }
    }
```

### 3.3 正确性论证

| 关注点 | 论证 |
|---|---|
| `in_callback` TLS 是否泄漏 | 否。`subscription.cpp:27` 在 rethrow **之前**已置 `in_callback = false` |
| 违约订阅者是否会被反复调用 | 是，但极廉价。`invoke` 首行 `if (!enabled) return;`（`subscription.cpp:15-17`）直接返回，不取 `active` |
| 是否需从快照移除违约订阅者 | 不需要。移除需 `mutex` + `rebuild_subscribers()`，在扇出循环内做会重新引入持锁调回调风险；空转成本可忽略 |
| 异常类型是否需区分 | 不需要。`catch (...)` 足够；`bad_alloc` 也按违约处理（不惩罚其他订阅者） |
| 流状态 | 保持 `RUNNING`。异常不再逃逸到 `capture()`（`stream.cpp:444-455`），worker 不退出 |
| 死锁风险 | 无。`dispatch` 已释放 `Stream::mutex`（`stream.cpp:426-429` 的 scope 结束），`invoke` 在锁外调用 |

### 3.4 行为变更与兼容性

| 场景 | 变更前 | 变更后 |
|---|---|---|
| 单订阅者抛异常 | 流 → `FAILED`/`ERR_INTERNAL`，worker 退出 | 该订阅者静默失效，流保持 `RUNNING` |
| 双订阅者，其一抛异常 | 流 → `FAILED`，**两个**订阅者都断流 | 违约者失效，**另一个持续收包** |
| Go 侧回调 panic | 已被 `recover()` 拦截，无影响 | 无影响（行为不变） |

**兼容性影响**：唯一受影响的现有测试是 `native/tests/rtsp_integration_test.cpp:390-406`（`throwing_callback_case`），它断言 `FAILED + ERR_INTERNAL`，必须重写。其余 FAILED 断言（`:214`、`:437`）分别对应「正常流不失败」与「空闲超时」，与本改动无关。

### 3.5 重写后的测试形态

```cpp
/** @brief 测试用户回调异常被逐订阅者隔离：违约者静默失效，同流其他订阅者持续收包 */
void throwing_callback_case(Zhulong_engine_h engine, const std::string &base) {
    // 1. 同一物理流注册两个 PREVIEW 消费者 (consumer 1 违约, consumer 2 正常)
    // 2. 消费者 1 的回调抛 C++ 异常；消费者 2 的回调累计收包计数
    // 3. 断言：消费者 2 的计数持续增长 (至少 N 个包)
    // 4. 断言：流状态保持 RUNNING，且 error == Zhulong_OK
    // 5. 断言：unsubscribe 两个订阅均成功，release 两个消费者均成功
}
```

⚠️ **测试需可确定性**：断言「消费者 2 收到 ≥N 个包」而非「恰好 N 个」；用 `wait_until` 轮询而非固定 sleep。

## 4. R2 设计：C++ 内部可观测性计数器

### 4.1 计数器清单

| 计数器 | 归属 | 递增点 | 含义 |
|---|---|---|---|
| `decoder_unavailable_drops` | `DecodeSession` | `stream.cpp:236-238` 的 `if (dec)` 的 else 分支 | 解码器不可用（退避窗口内 / codec 无效 / 创建失败）导致的丢包 |
| `keyframe_gate_drops` | `DecodeSession` | `stream.cpp:230-233`、`239-243`、`push_packet:85-87` | 关键帧门控拒绝的包（生产者侧 + 解码线程侧） |
| `congestion_resyncs` | `DecodeSession` | `push_packet:109-115` 的 push 失败分支 | 队列溢出触发的重同步次数 |
| `callback_exceptions` | `Stream` | R1 的 `catch (...)` | 被隔离的订阅者异常次数 |

### 4.2 接口设计

沿用 `stream.hpp` 现有查询风格（`frame_queue_size()` / `packet_queue_size()` 同级），但用**快照结构体**避免 4 个独立 getter：

```cpp
// native/src/pipeline/stream.hpp

/** @brief 解码会话累计丢包与重同步计数快照 (仅供测试与运维观测，不跨越 C ABI) */
struct DecodeCounters {
    uint64_t decoder_unavailable_drops{0}; /**< 解码器不可用导致的丢包数 */
    uint64_t keyframe_gate_drops{0};       /**< 关键帧门控拒绝的包数 */
    uint64_t congestion_resyncs{0};        /**< 队列溢出触发的重同步次数 */
};
```

```cpp
// DecodeSession
    /** @brief 获取累计计数快照 */
    DecodeCounters counters() const;

// Stream
    /** @brief 获取被隔离的订阅者回调异常次数 */
    uint64_t callback_exception_count() const;
```

成员声明（`lower_snake_case` + `atomic`，遵循 `naming-guidelines.md` §4）：

```cpp
    std::atomic<uint64_t> decoder_unavailable_drops_{0};
    std::atomic<uint64_t> keyframe_gate_drops_{0};
    std::atomic<uint64_t> congestion_resyncs_{0};
    // Stream 侧
    std::atomic<uint64_t> callback_exceptions_{0};
```

### 4.3 内存序与线程安全

- 全部 `fetch_add(1, std::memory_order_relaxed)`：计数器仅用于统计，无同步依赖。
- `counters()` 用 `load(std::memory_order_relaxed)` 取快照，**不取任何 mutex** → 不引入锁竞争，不破坏既有锁序。

### 4.4 不做的事

- 不新增 C ABI 导出（决策 D2）：`engine.h` 无 `Zhulong_*counters*` 符号。
- 不加日志：`initialize_ffmpeg()` 已全局静默 FFmpeg 日志（`rtsp_input.cpp:266-269`），且日志会引入线程安全问题。
- 不做时间窗口/速率统计：只需累计值，速率由消费方按需计算。

## 5. R3 设计：死代码清理

| 目标 | 位置 | 验证方式 |
|---|---|---|
| 删除 `BoundedQueue::reset()` | `native/src/pipeline/bounded_queue.hpp:124-136`（含注释块） | 删除后 `grep -rn "\.reset()" native/` 仅剩 `decoder.reset`/`frame.reset`/`consumer.reset` |
| 删除 `#include <cstring>` | `native/src/pipeline/stream.cpp:15` | 删除后编译通过（`-Wall -Wextra -Wpedantic` 零告警） |

⚠️ **保留** `OverflowStrategy::BLOCK` 枚举值与默认参数：它是 `BoundedQueue` 的合理 API 面，且 spec 中的规划路径将来会用到。

## 6. R4 设计：文档对齐

| 文件 | 行 | 修正要点 |
|---|---|---|
| `native/README.md` | 170 | 删除「not exposed as a continuous decode pipeline」；改述为「C++ 内部提供按需连续解码流水线，但不经 C ABI 暴露帧」 |
| `native/README.md` | 143 | 将「no async packet queue or pre-decode dropping」限定为 C ABI 作用域，并说明 C++ 内部存在 `packet_queue_` 与关键帧丢包 |
| `native/README.md` | 199 | 删除「capture-only pipeline」定性，说明连续解码的验证状态 |
| `.trellis/spec/native/pipeline-guidelines.md` | 42 | `packet_queue_.reset()` → `packet_queue_.clear()`，并说明语义差异（不触碰取消标记） |
| `.trellis/spec/native/pipeline-guidelines.md` | 36 | 该行标注为「规划中（未实现）」 |

⚠️ **README 行号会因前面的编辑漂移**：实施时先定位原文再改，不按行号硬改。

## 7. 风险与回滚

| 风险 | 缓解 |
|---|---|
| R1 改变流状态语义，可能影响未发现的调用方 | 全仓 `grep -rn "Zhulong_STREAM_FAILED"` 已确认仅 3 处断言，其中 2 处无关 |
| 测试重写后失去「异常不崩溃」的覆盖 | 新测试保留进程存活断言（后续用例继续执行即为证明） |
| 计数器影响解码热路径 | 纯 `relaxed` 原子自增，无锁无分支预测惩罚；且仅在**已丢包**路径上执行 |
| 文档改动误伤其他章节 | 逐条按原文定位，不做整段替换 |

**回滚点**：改动集中在 4 个源文件 + 2 个文档，`git checkout` 即可完全回退，无数据迁移、无 ABI 变更、无配置变更。

## 8. 验证策略

| 层级 | 命令 | 覆盖 |
|---|---|---|
| 静态 | `g++ -Wall -Wextra -Wpedantic -fsyntax-only`（3 个 TU） | 零告警、零未使用 include |
| 单元 | `./ZhulongDecoderTests`、`./ZhulongCaptureUnitTests` | 队列语义、计数器、`invoke` 重抛契约 |
| 集成 | `make native-test`（含 RTSP loopback） | R1 双订阅者隔离端到端 |
| 跨语言 | `make go-check` | Go/CGO 桥接未被破坏 |
| 竞态 | `python3 native/scripts/build.py --sanitizer thread test` | 无死锁、无数据竞争 |
| 内存 | `python3 native/scripts/build.py --sanitizer address test` | 无泄漏、无越界 |
