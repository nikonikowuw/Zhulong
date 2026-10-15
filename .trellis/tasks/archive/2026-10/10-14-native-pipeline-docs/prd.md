# native 引擎与流水线层注释补齐

> 父任务：`.trellis/tasks/10-14-native-core-code-docs/`
> ⚠️ 动手前必读父任务 `design.md` §3（风格基线）与 §4（术语表）。
> ⚠️ 本子任务**不单独编写 design.md / implement.md**：技术设计与执行计划由父任务的 `design.md` / `implement.md` 统一持有。本 PRD 只补充本层特有的范围与重点。

## 1. 目标

为 `native/src/abi/`、`native/src/pipeline/`、`native/src/nodes/` 补齐中文注释。这一层承载引擎生命周期、按需解码会话、有界队列背压、线程汇合与异常隔离——**约束最多、注释最少**（多数文件在 3%~32%）。

⚠️ 本层的意图**不能从实现反推**。`10-11-native-pipeline-stability` 任务修复了一批隐蔽缺陷（生命周期不可恢复、持锁 join、参考帧链被破坏、后台异常终止进程等），这些"为什么这么写"的知识只存在于该任务的 `prd.md` / `design.md` 中。**必须先读再写。**

---

## 2. 范围（15 文件 / ~2000 行）

| 文件 | 行数 | 当前注释率 |
| --- | --- | --- |
| `native/src/abi/engine.cpp` | 215 | 23% |
| `native/src/pipeline/engine.hpp` | 111 | 32% |
| `native/src/pipeline/engine.cpp` | 401 | 3% |
| `native/src/pipeline/stream.hpp` | 190 | 23% |
| `native/src/pipeline/stream.cpp` | 487 | 8% |
| `native/src/pipeline/bounded_queue.hpp` | 170 | 17% |
| `native/src/pipeline/frame_publisher.hpp` | 57 | 29% |
| `native/src/pipeline/frame_publisher.cpp` | 85 | 10% |
| `native/src/pipeline/subscription.hpp` | 51 | 39% |
| `native/src/pipeline/subscription.cpp` | 50 | 22% |
| `native/src/nodes/capture/rtsp_input.hpp` | 138 | 57% |
| `native/src/nodes/capture/rtsp_input.cpp` | 384 | 20% |
| `native/src/nodes/decode/decoder.hpp` | 61 | 44% |
| `native/src/nodes/decode/ffmpeg_decoder.hpp` | 34 | 11% |
| `native/src/nodes/decode/ffmpeg_decoder.cpp` | 158 | 6% |

### 明确排除

- `native/vendor/**`、`native/tests/**`、公开头文件（由 `10-14-native-public-contract-docs` 负责）。
- 行为变更、重构（不提取函数、不合并分支、不整理格式）。

---

## 3. 重点内容清单

### 3.1 `abi/engine.cpp`（C ABI 边界）

- 每个导出函数的**异常防爆门**与输出参数置空约定。
- 不透明句柄 ↔ C++ 对象的转换方式与所有权归属。
- ⚠️ 回调期间调用控制接口的拒绝路径（`Zhulong_ERR_CALLBACK_CONTEXT`）。
- `unsubscribe` 的**同步排空**语义在实现上如何保证。

### 3.2 `pipeline/engine.*`

- 引擎的 5 步优雅停机顺序（停止生产者 → 唤醒等待 → 汇合工作线程 → 释放资源 → 状态置位）。
- ⚠️ **锁外 join 禁令**：为什么 `Engine::control` 不得在持锁状态下 `join()`（会阻塞其他流的 acquire/release/status）。
- 流池与消费者引用计数、8 秒宽限期（Grace Period）的意图。
- 探测（probe）的独立生命周期与可取消性。

### 3.3 `pipeline/stream.*`

- **按需解码会话**（`DecodeSession`）的三态：激活 → 优雅休眠 → 再次激活。⚠️ 重点说明"绝不复用已取消的队列"。
- ⚠️ `packet_queue` 满载时的**有界重同步**：丢弃至下一个关键帧/IDR，以及为什么不能简单 `DROP_OLDEST`（H.264/H.265 帧间参考依赖）。
- `frame_queue` 容量 3 + 丢弃最旧策略的意图（AI 消费始终处理最新画面）。
- ⚠️ **PTS 有效性**：`has_pts` / `has_dts` / `time_base_*` 必须透传，禁止把缺失时间戳当作有效 0。
- `dispatch` 的优先级顺序（先送 AI 解码队列，再在锁外触发 Preview 回调）。
- 帧消费借用通道（`shared_ptr<const HardwareFrame>` 多消费者只读借用 + `release_fn` 延迟回收）。

### 3.4 `pipeline/bounded_queue.hpp`

- 容量上限与背压语义；三种满载策略（阻塞 / 丢弃最旧 / 丢弃最新）的适用场景。
- `cancel()` 的语义：唤醒所有阻塞读者/写者，且**取消后不可复用**（与 stream 的会话重建呼应）。
- ⚠️ 清理与丢弃元素时**不在锁内**执行用户回调（避免持锁调用外部代码）。

### 3.5 `pipeline/frame_publisher.*`

- 发布侧节流口径（基于 `max(sample_fps)`）与它为什么在 Native 侧而不是 Go 侧。
- 与 FrameBroker 的关系（发布通路归属）。

### 3.6 `nodes/capture/rtsp_input.*`

- RTSP 建连、超时（`open_timeout_ms` / `idle_timeout_ms`）与重连策略。
- 视频元数据（codec / extradata / time_base / 分辨率）的解析与向上透传时机。
- ⚠️ `extradata` 的借用语义与生命周期。

### 3.7 `nodes/decode/*`

- `IDecodeNode` 接口的职责边界与 `flush` / drain 语义。
- ⚠️ `avcodec_send_packet` 的 `EAGAIN` 处理与 drain 循环，**不得静默吞包**。
- ⚠️ 解码线程顶层**必须**捕获全部异常（含未知异常），失败转化为流状态，绝不允许后台异常导致 `std::terminate()`。
- 软解输出的平面格式标记（如 `FRAME_FORMAT_YUV420P`）与多平面步长，⚠️ 防止外部误当作连续 NV12 解析。

### 3.8 `frame.hpp` 的交叉引用

`HardwareFrame` 的注释归属公开契约子任务，但 pipeline 中所有**借用/回收**该帧的位置需要写清"何时借出、何时归还、谁负责归还"。

---

## 4. 验收标准

### 4.1 继承父任务共性标准

- [x] **AC-D1（覆盖率）**：范围内每个公开声明与每个**非平凡逻辑块**（状态机、并发协议、队列策略、错误处理）都有注释；仅命名自明的 getter/setter 可豁免。
- [x] **AC-D2（零代码变更）**：父任务 `implement.md` §3 的注释剥离不变性检查，全量输出 `OK`。
- [x] **AC-D3（构建测试）**：`make native-build` 成功；`make native-test` 全绿。
- [x] **AC-D4（注释质量）**：无逐行复述；无臆造技术细节。
- [x] **AC-D5（既有注释保真）**：既有注释未被删除或改变技术含义。

### 4.2 本子任务专属

- [x] **AC-C2**：§3 重点内容清单逐项落地。
- [x] **AC-C2-a**：父任务 `prd.md` §4.3 中归属 C2 的风险点 **R15、R16、R17、R18、R19、R20** 全部在注释中有明确落点。
- [x] **AC-C2-b**：本层注释的意图描述与 `.trellis/tasks/archive/2026-10/10-11-native-pipeline-stability/{prd,design}.md` 一致，**未凭实现反推**。若发现实现与该任务的决策不符，⚠️ **不修改代码**，只在任务 `implement.md` 中登记为待确认项。
- [x] **AC-C2-c**：注释中未出现父任务 `design.md` §4 术语表列出的禁用别名（特别是 `join` 统一译"汇合"、`drain` 译"排空"、`flush` 译"冲刷"）。
- [x] **AC-C2-d**：`bounded_queue.hpp` 的"取消后不可复用"与 `stream.*` 的"会话重建"两处注释**互相呼应**，不产生矛盾表述。

---

## 5. 参考

| 用途 | 路径 |
| --- | --- |
| 风格基线 / 术语表 | 父任务 `design.md` §3 / §4 |
| 执行循环 / 门禁 / 回滚点 | 父任务 `implement.md` §2 / §4 / §5 |
| 本层意图权威来源 | `.trellis/tasks/archive/2026-10/10-11-native-pipeline-stability/prd.md`、`design.md` |
| 流水线规范 | `.trellis/spec/native/pipeline-guidelines.md` |
| 接入合同 | `.trellis/spec/native/ingestion-contract.md` |
| 既有风格样本 | `native/src/nodes/capture/rtsp_input.hpp`、`native/src/algorithm/ipc/shm_pool.hpp` |
