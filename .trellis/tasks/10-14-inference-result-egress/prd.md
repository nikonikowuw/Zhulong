# 实现推理结果出口

## Goal

让 runner 的推理结果**回到 Go 宿主**：native 侧新增结果上报通道，Go 侧接收并接线 `InferenceResult` 的落库与事件派发，消除「`InferenceResult` 三个构造函数零调用方」与「runner 拿到结果直接释放」这两个断点。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

- **前置**：`10-14-algo-structure-abi` 的 ABI 冻结（结果结构决定上报载荷形态）。
- **可并行**：与 `10-14-gallery-hot-sync`、`10-14-face-feature-extraction` 并行。
- **后继**：`10-14-face-result-frontend` 依赖本任务的输出。

## 问题证据

```cpp
// native/src/algorithm/runner/runner.cpp:388
rc = api.process(task, &view, &result);
if (result) { api.result_release(result); }   // ← 结果直接释放，从不上报
```

| 位置 | 现状 |
| --- | --- |
| `native/src/algorithm/ipc/ipc_protocol.hpp:14-20` | **无任何结果上报消息类型**（实测 native 全目录 `MSG_RESULT` / `send_result` 命中数为 0） |
| `internal/algorithm/result.go:61,76,91` | `NewDetectionResult` / `NewClassificationResult` / `NewPersonStructureResult` **零调用方** |
| `web/src/features/live/` | 无任何检测框叠加层 |

⚠️ 当前是**一台完整的推理机器，没有出料口**。

## Requirements

- **R1 native 上报消息类型**：在 `ipc_protocol.hpp` 定义结果上报消息类型（数值需与既有约定不冲突），
  并纳入跨语言常量护栏测试。
- **R2 runner 上报替代释放**：`runner.cpp` 中 `api.process()` 之后改为**序列化上报**，
  上报完成后再 `result_release()`。
  ⚠️ **生命周期**：结果对象在 `result_release` 前有效；序列化必须在其之前完成，不得持指针跨越。
- **R3 序列化格式**：定案载荷格式（二进制结构 vs JSON），在 `design.md` 中说明理由。
  ⚠️ 若选 JSON，需评估边缘设备高帧率下的序列化开销（父 `design.md` §3.2 方案 C 的缺点）。
- **R4 背压与丢弃策略**：结果流速率可能高于宿主消费速率。
  必须明确**丢弃策略**（丢最旧 / 丢最新 / 阻塞）与可观测计数，禁止无界缓冲。
- **R5 Go 侧接收**：在 broker 宿主或 Go 侧接收结果消息，解析为 `InferenceResult`。
- **R6 落库与派发**：接线 `InferenceResult` 的消费路径（落库和/或事件派发），
  使三个构造函数**有真实调用方**（非测试）。
  ⚠️ 存储设计须遵循 `.trellis/spec/backend/storage-guidelines.md`：schema 变更 = 新增编号迁移对，**不用 `AutoMigrate`**。
- **R7 通道归属**：确认与 FrameBroker 复用同一条 UDS（父 `design.md` D2），或说明为何另开。
- **R8 可观测性**：结果丢弃、解析失败、背压触发需有日志与计数，遵循 `.trellis/spec/backend/logging-guidelines.md`。
- **R9 消除死代码状态**：`runner.cpp` 的「收到即释放」与 `result.go` 的零调用方状态必须**真实消除**。

## Acceptance Criteria

- [ ] runner 在 `api.process()` 后上报结果；原生测试验证上报内容与插件输出一致。
- [ ] 原生侧 ASan 干净：**无 use-after-free**（这是本任务最可能的内存错误——序列化与释放的顺序）。
- [ ] Go 侧有真实消费路径；`grep` 可确认 `NewPersonStructureResult` 等有非测试调用方。
- [ ] 背压策略有测试：模拟慢消费，验证丢弃策略生效且计数正确，内存不无界增长。
- [ ] 结果解析失败是**显式错误 + 日志**，不是静默丢弃。
- [ ] 既有帧派发路径**未被破坏**。
- [ ] `make native-test` 与 `make go-check` 通过（含 `-race`）。
- [ ] 前端可在**不做任何改动**的前提下拿到结果数据（验证出口真的通了）。

## 范围外

- 前端叠加层与识别记录 UI（属 `10-14-face-result-frontend`）。
- 识别结果的长期留存策略与容量水位联动（父任务范围外）。
- 底库下行（属 `10-14-gallery-hot-sync`）。
