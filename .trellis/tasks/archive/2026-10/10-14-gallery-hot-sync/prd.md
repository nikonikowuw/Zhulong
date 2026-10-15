# 实现底库热更新通道

## Goal

打通人员底库从 Go 宿主到算法进程的**下行链路**，使算法进程能在不重启的前提下获得全量底库（冷启动）与增量变更（运行期新增/删除人脸），并解决 64KB IPC 上限带来的分片问题。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

- **前置**：`10-14-algo-structure-abi` 的 ABI 冻结（共享 §2 跨层契约）。
- **可并行**：与 `10-14-inference-result-egress`、`10-14-face-feature-extraction` 并行。
- **后继**：`10-14-face-algo-package` 依赖本通道才能做 1:N 检索。

## 问题证据

| 位置 | 现状 |
| --- | --- |
| `native/src/algorithm/ipc/ipc_protocol.hpp:14-20` | 枚举止于 `MSG_DISCONNECT = 6`，**无** `MSG_GALLERY_*` |
| `native/src/algorithm/runner/runner.cpp:358` | 收包循环只处理 `MSG_FRAME_DISPATCH`，其他 `msg_type` **静默丢弃** |
| `internal/algorithm/process.go:446,460,474` | `CreateGallerySyncMsg` / `Upsert` / `Delete` 三函数**零调用方**，注释自标 `⚠️ NOT WIRED` |
| `internal/person/service.go:311` | `GetGallerySyncData` 零调用方，注释自标 `⚠️ NOT WIRED` + `TODO: 死代码` |

⚠️ 后果：native 侧会把 `msg_type=10` 当**未知消息静默丢弃**，表现为「功能没反应」而非报错。

## Requirements

- **R1 native 消息类型**：在 `ipc_protocol.hpp` 中补齐 `MSG_GALLERY_SYNC` / `MSG_GALLERY_UPSERT` / `MSG_GALLERY_DELETE`
  （数值与 Go 侧既有 10/11/12 保持一致），并纳入跨语言常量护栏测试。
- **R2 通道归属**：确认 gallery 通道与 FrameBroker **复用同一条 UDS**（父 `design.md` D1），
  在 `design.md` 中说明复用方式与消息多路复用约定。
- **R3 runner 侧接收**：runner 处理 gallery 消息，在进程内维护可查询的底库索引（人脸 ID → 特征向量）。
  索引结构需说明并发安全策略（多 worker 线程共享只读索引 vs 每 worker 副本）。
- **R4 冷启动全量同步**：runner 就绪握手后，Go 侧下发全量 `SYNC`。
- **R5 运行期增量**：`person.Service` 的 Create/Update/Delete/AddFace/DeleteFace 成功后下发 `UPSERT` / `DELETE`。
- **R6 分片（必须，非优化）**：单条 IPC 上限 64KB（`MAX_IPC_PAYLOAD_SIZE`）。
  512 维 float32 ≈ 2KB/人，全量同步约 30 人即触顶。**分片协议必须在一期实现**
  （分片序号、总片数、重组语义、超时丢弃）。
  ⚠️ **范围可能降级**：若 `10-14-face-feature-extraction` 采用插件内提取
  （即 Go 只下发 `image_path` 而非特征向量），单条载荷将大幅缩小，
  分片压力显著缓解。但**仍需实现分片**，因为人员规模无上界，且路径本身也有长度。
  分片粒度与阈值需在 `design.md` 中基于实际载荷尺寸计算。
- **R7 ACK 语义**：`SYNC` 后须有 ACK；增量可异步。复用既有 `MSG_FRAME_ACK` 序号机制或新增
  `MSG_GALLERY_ACK`（定案写入 `design.md`）。
- **R8 身份字段**：载荷 `person_id` 恒取 `persons.person_id`（对外业务键）**原值**；
  ⚠️ 严禁混用 `person_faces.person_id`（内部行 ID 外键）。
- **R9 载荷字段名**：统一使用 `features`（float32 数组），见父 `design.md` §2.2。
  ⚠️ **若采用插件内提取（D8）**，载荷可能只需 `image_path` 而非 `features`。
  两种形态的取舍需在 `design.md` 中定案，并相应更新
  `.trellis/spec/native/algo-plugin-contract.md` §7.2 的字段名契约。
- **R10 契约文档**：更新 `.trellis/spec/native/algo-plugin-contract.md` §7.3，
  将状态从「未实现，登记供后续任务」改为已实现，并记录分片协议。
- **R11 消除死代码状态**：四个 `⚠️ NOT WIRED` 注释必须因**接线完成**而移除，不得仅删注释。

## Acceptance Criteria

- [ ] native `ipc_protocol.hpp` 定义了 10/11/12，且 `ipc_contract_test.go` 式护栏覆盖。
- [ ] 冷启动全量 SYNC 生效：runner 能查到 SYNC 下发的全部人脸。
- [ ] 增量生效：新增/删除一张人脸后**不重启算法进程**即可反映变化。
- [ ] 分片正确：构造超过 64KB 的底库（例如 ≥ 50 人 × 512 维），SYNC 完整送达且**不截断**。
- [ ] 分片异常路径有测试：中途丢片 / 超时后的行为是**显式失败**而非静默半量底库。
- [ ] `person_id` 语义有测试：确认载荷中是业务键而非内部行 ID。
- [ ] 既有帧派发路径**未被破坏**：`MSG_FRAME_DISPATCH` 相关测试全绿。
- [ ] `make native-test` 与 `make go-check` 通过；ASan 干净。

## 范围外

- 特征向量的**生成**（属 `10-14-face-feature-extraction`；本任务假设底库中已有有效特征）。
- 结果上行通道（属 `10-14-inference-result-egress`）。
- 底库加密与合规审查。
