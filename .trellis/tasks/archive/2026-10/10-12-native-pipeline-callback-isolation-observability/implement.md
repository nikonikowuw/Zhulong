# 实施计划：Native Pipeline 回调隔离与可观测性

## 前置条件

- [ ] 工作区干净，当前分支 `dev`
- [ ] 静态 FFmpeg 依赖已就绪（`build/deps/ffmpeg/*/install/include`）
- [ ] 基线：`cd build/native/host && ./ZhulongDecoderTests` 通过
- [ ] **决策 D1–D3 已由用户确认**（"采用你的推荐"），且本文件的最终规划摘要已获明确批准

## 变更文件清单

| 文件 | 变更类型 | 对应需求 |
|---|---|---|
| `native/src/pipeline/stream.hpp` | 新增结构体 + 计数器成员 + 查询接口 | R2 |
| `native/src/pipeline/stream.cpp` | R1 异常隔离 + R2 计数递增 + R3 删 include | R1/R2/R3 |
| `native/src/pipeline/bounded_queue.hpp` | 删除 `reset()` | R3 |
| `native/tests/rtsp_integration_test.cpp` | 重写 `throwing_callback_case` | R1 |
| `native/tests/decoder_test.cpp` | 新增计数器断言 | R2 |
| `native/README.md` | 3 处过时描述修正 | R4 |
| `.trellis/spec/native/pipeline-guidelines.md` | 2 处对齐 | R4 |

⚠️ **本任务不修改**：`native/include/Zhulong/engine.h`、`native/src/abi/engine.cpp`、`internal/engine/*`、`native/src/pipeline/subscription.{hpp,cpp}`。

## 实施步骤

### Step 1 — R3 死代码清理（最小、独立、可先验证）

1. 删除 `native/src/pipeline/bounded_queue.hpp:124-136` 的 `reset()` 方法及其文档注释块。
2. 删除 `native/src/pipeline/stream.cpp:15` 的 `#include <cstring>`。

**验证**：

```bash
grep -rn "\.reset()" native/ | grep -v "decoder\.reset\|frame\.reset\|consumer"
# 期望：无输出
INC=$(dirname $(dirname $(find build/deps/ffmpeg -path "*install/include/libavcodec/avcodec.h" | head -1)))
for f in native/src/pipeline/*.cpp; do
  g++ -std=c++17 -Wall -Wextra -Wpedantic -fsyntax-only -I native/src -I native/include -I "$INC" "$f"
done
# 期望：零告警
```

**回滚点 1**：`git checkout native/src/pipeline/bounded_queue.hpp native/src/pipeline/stream.cpp`

---

### Step 2 — R2 计数器：`DecodeSession` 部分

1. 在 `native/src/pipeline/stream.hpp` 的 `namespace zhulong` 内、`QueuedPacket` 定义之前，新增 `DecodeCounters` 结构体（见 design.md §4.2）。
2. 在 `DecodeSession` private 区新增 3 个 `std::atomic<uint64_t>` 成员（`decoder_unavailable_drops_` / `keyframe_gate_drops_` / `congestion_resyncs_`），初值 0。
3. 在 `DecodeSession` public 区新增 `DecodeCounters counters() const;`。
4. 在 `stream.cpp` 实现 `counters()`：3 个 `load(std::memory_order_relaxed)` 组装快照，**不取任何锁**。
5. 在以下位置插入 `fetch_add(1, std::memory_order_relaxed)`：
   - `push_packet`：`waiting_keyframe_` 拦截非关键帧处（现 `stream.cpp:85-87`）→ `keyframe_gate_drops_`
   - `push_packet`：push 失败进入重同步处（现 `stream.cpp:109-115`）→ `congestion_resyncs_`
   - `decode_loop`：`need_keyframe` 拦截处（现 `stream.cpp:230-233`）→ `keyframe_gate_drops_`
   - `decode_loop`：重建后二次拦截处（现 `stream.cpp:239-243`）→ `keyframe_gate_drops_`
   - `decode_loop`：`if (dec)` 的 **else** 分支（现 `stream.cpp:236-238`）→ `decoder_unavailable_drops_`

⚠️ **注意**：`stream.cpp:239-243` 的二次拦截在 `if (dec)` 块**内部**，else 分支在其外部；插入时勿破坏原有控制流缩进。

**验证**：

```bash
INC=$(dirname $(dirname $(find build/deps/ffmpeg -path "*install/include/libavcodec/avcodec.h" | head -1)))
g++ -std=c++17 -Wall -Wextra -Wpedantic -fsyntax-only -I native/src -I native/include -I "$INC" native/src/pipeline/stream.cpp
```

---

### Step 3 — R1 回调异常隔离 + R2 的 `Stream` 侧计数

1. 在 `Stream` private 区新增 `std::atomic<uint64_t> callback_exceptions_{0};`。
2. 在 `Stream` public 区新增 `uint64_t callback_exception_count() const;`，实现为单次 relaxed `load`。
3. 改造 `Stream::dispatch` 的扇出循环（现 `stream.cpp:431-436`）为 design.md §3.2 的目标形态：循环内 `try { invoke } catch (...) { fetch_add }`。

⚠️ **不得修改** `Subscription::invoke`：`native/tests/capture_unit_test.cpp:84-91` 依赖其重抛语义。

**验证**：

```bash
cd build/native/host && ./ZhulongCaptureUnitTests
# 期望：URL/options/ownership/drain unit tests passed
```

---

### Step 4 — 重写 `throwing_callback_case`

位置：`native/tests/rtsp_integration_test.cpp:390-406`。

新测试逻辑：

1. 用 `fixture` 路径 `/pool`（已有帧流，见 `rtsp_fixture.py:168 send_frames`）。
2. 同一物理流 acquire **两个** `Zhulong_CONSUMER_PREVIEW` 消费者（consumer 1、consumer 2）。
3. consumer 1 订阅一个**抛异常**的回调；consumer 2 订阅一个**累计计数**的回调（复用 `CallbackState`，见 `rtsp_integration_test.cpp:72-87`）。
4. 用 `wait_until` 断言 consumer 2 的 `count` 增长到 ≥ 20（不写死精确值）。
5. 断言流状态为 `Zhulong_STREAM_RUNNING` 且 `status.error == Zhulong_OK`。
6. 断言 `unsubscribe` 两个订阅、`release` 两个消费者均返回 `Zhulong_OK`。
7. 断言进程存活（函数返回后主流程继续执行即为证明）。

⚠️ **顺序要求**：`unsubscribe → release`（`engine.h` 契约：有活跃订阅时 release 返回 `BUSY`）。

**验证**：

```bash
make native-test
# 期望：ZhulongRtspIntegration 通过
```

**回滚点 2**：`git checkout native/tests/rtsp_integration_test.cpp`

---

### Step 5 — 新增计数器断言

位置：`native/tests/decoder_test.cpp`。

在现有第 6 节「拥塞重同步」用例（约 `:356-400`）中补充：

- 喂满队列触发重同步后，断言 `session.counters().congestion_resyncs >= 1`。
- 断言 `session.counters().keyframe_gate_drops > 0`（非关键帧被拦截）。
- 在正常产帧用例中（第 5 节）断言各计数为 0（无丢包场景）。

**验证**：

```bash
cd build/native/host && ./ZhulongDecoderTests
# 期望：All Decoder & BoundedQueue tests passed successfully!
```

---

### Step 6 — R4 文档对齐

1. `native/README.md`：**先按原文定位**（行号已漂移），再改 3 处（见 design.md §6）。
2. `.trellis/spec/native/pipeline-guidelines.md`：改 2 处（第 42 行 `reset()` → `clear()`；第 36 行标注规划中）。

⚠️ **不做整段替换**，逐条按原文定位，避免误伤相邻内容。

**验证**：

```bash
grep -n "not exposed as a continuous decode pipeline\|capture-only pipeline" native/README.md
# 期望：无输出
grep -n "packet_queue_.reset()" .trellis/spec/native/pipeline-guidelines.md
# 期望：无输出
```

---

### Step 7 — 全量回归

```bash
make native-test          # Native 全量（含 RTSP loopback、纯 C ABI、构建合同）
make go-check             # Go/CGO 桥接
python3 native/scripts/build.py --sanitizer thread test    # TSan：无死锁/竞争
python3 native/scripts/build.py --sanitizer address test   # ASan+UBSan：无泄漏/越界
```

⚠️ **ASan/TSan 需独占写者**：确认无其他构建进程占用 `build/`。

## 验收对照

| AC | 验证步骤 |
|---|---|
| AC-1 订阅者隔离 | Step 4 |
| AC-2 丢包可观测 | Step 5 |
| AC-3 死代码清理 | Step 1 + Step 7 |
| AC-4 文档一致性 | Step 6 |
| AC-5 全量回归 | Step 7 |

## 风险与回滚点

| 回滚点 | 范围 | 触发条件 |
|---|---|---|
| 1 | Step 1（死代码） | 编译告警或测试失败 |
| 2 | Step 4（测试重写） | 新测试不稳定（flaky） |
| 全量 | 全部 4 源文件 + 2 文档 | 任一 AC 不通过 |

无 ABI 变更、无数据迁移、无配置变更，`git checkout` 即可完全回退。

## 提交前检查

- [ ] `git diff --stat` 确认仅改动清单内文件
- [ ] `native/include/Zhulong/engine.h` 无改动
- [ ] 无新增 `TODO` / 调试输出
- [ ] 提交信息遵循 conventional commits（`fix(native):` 前缀）
