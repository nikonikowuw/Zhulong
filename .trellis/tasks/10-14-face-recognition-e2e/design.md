# 设计：人脸识别端到端接入

> 本设计只覆盖**跨子任务的架构决策与跨层契约**。各子任务的内部实现细节属于子任务自己的 `design.md`。
> 父任务本身不实现产品代码。

## 0. 关键决策摘要

| # | 决策 | 理由 | 状态 |
| --- | --- | --- | --- |
| D1 | Gallery 通道与 FrameBroker **复用同一条 UDS**，以消息类型多路复用 | broker 宿主已在 native engine 内（`engine.cpp:233`），Go 侧另开服务端会引入第二个连接生命周期与第二套超时/重连语义 | 建议采纳，待子任务确认 |
| D2 | 结果上报**复用同一条 UDS**，方向为 runner → broker 宿主 | 同上；避免为结果流再建一条连接 | 建议采纳 |
| D3 | 结构化结果走**独立 result_type**，不再复用 `result_type == 1` | 现有 `verifier.go:249` 的复用是临时约定，`zhulong_bbox` 无法承载嵌套结构 | 待 `algo-structure-abi` 定案 |
| D4 | ABI 扩展采用**新增 `zhulong_plugin_api_v2` + `struct_size` 协商**，而非原地修改 v1 | v1 的 `struct_size` 字段已为前向兼容预留；原地加字段会让旧插件在旧 runner 上读出未初始化内存 | 待 `algo-structure-abi` 定案 |
| D5 | 特征向量维度**由算法包 manifest 声明**，不硬编码 | 当前 `service.go` 硬编码 2048 字节（=512 float32）是占位，跨模型不可移植 | 建议采纳 |
| D6 | 分片是**必须的**，不是优化 | 单条 IPC 上限 64KB（`MAX_IPC_PAYLOAD_SIZE`）。512 维 float32 ≈ 2KB/人，全量 SYNC 在 ~30 人时即触顶 | 待 `gallery-hot-sync` 定案 |

## 1. 端到端数据流

### 1.1 现状（断点标注）

```txt
RTSP ─→ 解码 ─→ FrameBroker ─→ SHM ─→ Runner ─→ api.process() ─→ 插件
         ✅       ✅(native)     ✅     ✅        ✅             ✅
                                                                    │
                            ┌───────────────────────────────────────┘
                            ▼
                    ❌ B1: result_release() 后结果消失

人员 CRUD ─→ SQLite ─→ ❌ B2: 无下行通道 ─→ 算法进程（拿不到底库）
                │
                └─→ ❌ B4: feature_vector 全零
```

### 1.2 目标形态

```txt
                    ┌─────────────── Go 宿主 ───────────────┐
                    │  person.Service                        │
                    │    ├─ 特征提取（归属见待决问题 3）      │
                    │    └─ GetGallerySyncData ──┐           │
                    │  algorithm.ResultConsumer ←┼─────┐     │
                    └────────────────────────────┼─────┼─────┘
                                                 │     │
                          gallery 下行（分片）     │     │ 结果上行
                                                 ▼     │
   ┌──────────────── native engine (broker 宿主) ──────┼─────┐
   │  FrameBroker (UDS: zhulong_broker_<stream_id>.sock)│     │
   │    ├─ MSG_FRAME_DISPATCH ──→ Runner                │     │
   │    ├─ MSG_GALLERY_SYNC/UPSERT/DELETE ──→ Runner    │     │
   │    └─ MSG_RESULT ←────────────── Runner            │     │
   └────────────────────────────────────────────────────┼─────┘
                                                        │
   Runner ─→ 插件 ─→ 结构化结果 ─────────────────────────┘
```

## 2. 跨层契约（所有子任务必须遵守）

### 2.1 身份字段空间

- 载荷 `person_id` **恒取 `persons.person_id`**（对外业务键）**原值**。该键必填、唯一、不可变，因此**不存在回退到内部主键 `persons.id` 的分支**。
- ⚠️ `person_faces.person_id` 是**内部行 ID 外键**，与载荷 `person_id` 同名但**语义不同**，二者严禁混用——否则 native 侧会同时收到两套标识符空间。
- 依据：`.trellis/spec/native/algo-plugin-contract.md` §7.3

### 2.2 载荷字段名

- IPC 载荷统一使用 **`features`**（float32 数组）。
- `Face.FeatureVector []byte` 是 **DB 实体**字段名，映射 SQLite 动态 BLOB 列，天然支持任意维度，**不强制改名**；仅在序列化为 IPC 载荷时映射为 `features`。
- 依据：同上 §7.2

### 2.3 内存与生命周期

- packet / frame 视图**仅在同步回调期间有效**；跨消息保留必须拷贝。
- 结果对象的所有权：插件 `process()` 分配 → runner 序列化后 `result_release()`。**上报发生在 release 之前**，序列化必须完成后再释放。
- 绝不向 C 传 Go 指针；回调 token 用 `cgo.Handle` 整数。
- C++ 异常绝不穿越 C ABI。

### 2.4 跨语言常量一致性

`internal/algorithm/ipc_contract_test.go` 已有护栏：读取 `native/src/algorithm/ipc/ipc_protocol.hpp`，正则提取 `IPC_MAGIC` / `IPC_VERSION` / `MAX_IPC_PAYLOAD_SIZE` 与 Go 侧比对。

**本任务新增的所有 `MSG_*` 枚举值必须纳入同一护栏**，否则枚举漂移会再次发生（历史上已漂移过一次：Go 单方面定义了 native 不存在的 10/11/12）。

### 2.5 就绪语义

`ready` 只代表数据库**与**引擎就绪，与摄像头无关（`AGENTS.md`）。人脸链路不得改变此语义，也不得把「底库已同步」塞进 readiness。

## 3. ABI 扩展策略（`algo-structure-abi` 的核心决策）

### 3.1 问题

`native/sdk/include/zhulong/plugin.h` 的 `zhulong_bbox` 字段为
`{x, y, width, height, confidence, class_id, label[64]}` —— **扁平、单层**。
而 `result.go` 的 `PersonItem` 需要 `track_id` + `body_box` + 嵌套 `faces[]`，`FaceSubItem` 又需要 `landmarks` / `matched` / `candidates[]`。

**没有落脚点。** 现有 `DetectionResultAdapter` 无法表达。

### 3.2 候选方案

| 方案 | 做法 | 优点 | 缺点 |
| --- | --- | --- | --- |
| A. 原地扩展 v1 | 往 `zhulong_plugin_api_v1` 加 `process_structured` 函数指针 | 改动小 | ⚠️ 旧插件在旧 runner 上会读出未初始化内存；`struct_size` 协商失效 |
| B. 新增 v2 表 | 新增 `zhulong_plugin_api_v2`，含独立结构化结果 item 与独立导出函数 | 前向兼容清晰；v1 插件继续可用 | 需同时维护两套 adapter；runner 需按 `api_version` 分派 |
| C. 结果改 JSON 字符串 | `process` 返回 JSON 文本 | 无 ABI 版本问题；扩展灵活 | ⚠️ 每帧序列化开销；放弃零拷贝；类型安全丢失 |

**倾向 B**：`struct_size` 字段的存在意义就是版本协商，原地扩展（A）是对该设计的破坏。C 在边缘设备的高帧率场景下序列化开销不可接受。

### 3.3 若采纳 B，需要定案的点

- v2 表是否复用 v1 的 `model_create` / `worker_create` / `task_open` 语义（建议复用，仅结果部分分叉）。
- 结构化 item 的内存布局与释放责任（沿用现有 `internal_token` + `result_release` 模式）。
- `verifier.go:249` 的 `result_type` 映射如何更新，以及**旧包**（`result_type == 1` 的 person_structure）是否需要迁移期兼容。
- `algo.hpp` 的 adapter 模板如何泛化以同时支持 v1/v2 注册宏。

## 4. 风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| **ABI 决策滞后** | `face-algo-package` 必然返工 | 把 ABI 定案作为硬前置，先于任何插件编码 |
| **特征提取归属选错** | 若选宿主内提取，会打破「`internal/engine` 是唯一可 `import "C"` 的包」这条架构约定 | 待决问题 3 必须先答；倾向插件内 |
| **分片协议设计不足** | 人员规模增长后 SYNC 静默截断 | D6：分片必须在一期实现，不允许「先全量后分片」 |
| **枚举漂移** | native 把未知 msg_type 静默丢弃，表现为「功能没反应」而非报错 | §2.4 的护栏必须覆盖新增枚举 |
| **误报身份** | 安全/合规后果 | AC5 要求未登记人员在同一阈值下 `matched=false`；阈值必须可配置且随包声明 |
| **敏感数据合规** | `docs/prd/prd-v1.0.md:190` 要求上线前经安全与合规审查 | 本任务不覆盖审查，但设计不得让后续加密更难（特征与照片分列存储已具备条件） |

## 5. 交付与回滚形态

- 六个子任务**独立可验证、独立可归档**，但只有全部完成才满足父任务 AC1–AC7。
- 每个子任务必须能在**不破坏既有功能**的前提下单独回滚：
  - ABI 扩展（v2）不回滚 v1 路径。
  - Gallery 通道新增消息类型，不影响 `MSG_FRAME_DISPATCH` 既有路径。
  - 结果上报为新增方向，不回滚帧派发。
  - 特征提取替换占位时，需保留「维度不匹配」的显式失败路径（而非静默写入）。
  - 前端叠加层为新增 UI，可独立隐藏。
- 端到端验收必须在**目标硬件**上做，CPU 上的 mock 结果不作为 AC1–AC5 的证据。

## 6. 未决

见父 `prd.md`「待决问题」三条。**在它们被回答之前，各子任务只应完成规划，不应进入实现。**
