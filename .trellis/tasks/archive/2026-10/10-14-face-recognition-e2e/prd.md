# 人脸识别端到端接入

## Goal

把「人脸识别」从当前的**推理机器空转**状态接成一条可用业务链路：人员底库能下达到算法进程，算法产出的识别结果能回到 Go 宿主并落到前端，且底库里的特征向量是真实可比的。

本任务是**父任务**，本身不直接实现产品代码。它拥有源需求集、任务地图、跨子任务验收标准与最终集成审查。

## 现状基线（实测证据，2026-10-14）

已经交付的是**算法包基础设施**，不是人脸识别业务。以下四条是实测断点，不是推测：

| # | 断点 | 证据 |
| --- | --- | --- |
| B1 | **结果出不去**：runner 拿到推理结果后直接释放，不上报 | `native/src/algorithm/runner/runner.cpp:388` `api.process(...)` 后紧跟 `api.result_release(result)`；native 全目录 `MSG_RESULT` / `send_result` 命中数为 0 |
| B2 | **底库进不去**：双向都无线路 | native `ipc_protocol.hpp:14-20` 止于 `MSG_DISCONNECT=6`，无 `MSG_GALLERY_*`；Go 侧 `CreateGallerySyncMsg` / `CreateGalleryUpsertMsg` / `CreateGalleryDeleteMsg` / `person.GetGallerySyncData` 四个函数零调用方，注释自标 `⚠️ NOT WIRED` |
| B3 | **结果无消费方**：`InferenceResult` 三个构造函数零调用方，不落库、不派发 | `internal/algorithm/result.go:61,76,91`；前端 `web/src/features/live/` 无任何检测框叠加层 |
| B4 | **特征是假的**：底库记录的特征向量为全零占位 | `internal/person/service.go:217` `feat := make([]byte, 2048)`，未经任何特征提取 |

补充约束（影响设计决策，非缺陷）：

- `native/sdk/include/zhulong/plugin.h` 的 `zhulong_bbox` **装不下** `track_id` / 嵌套 `faces[]` / `candidates[]`；`algo.hpp` 只有 `DetectionResultAdapter` 与 `ClassificationResultAdapter` 两个 adapter。
- `internal/algorithm/verifier.go:249` 目前把 `person_structure` 当作 `result_type == 1`（复用 detection）处理——这是临时约定，必须在 B1 的 ABI 扩展中被正式替代。
- FrameBroker 宿主在 **native engine 内**（`native/src/pipeline/engine.cpp:233` `ensure_frame_broker`），Go 通过 `Supervisor.SetBrokerSubscriptionResolver` 解析 socket 路径。因此 gallery 通道的**自然归属是与 broker 复用同一条 socket 上的多路复用消息类型**，而不是 Go 侧另开服务端。

## 任务地图

| 子任务 | 包 | 覆盖断点 | 交付物 |
| --- | --- | --- | --- |
| `10-14-algo-structure-abi` | native | ABI 缺口 | 结构化结果在 C ABI 上的正式落脚点 |
| `10-14-gallery-hot-sync` | backend + native | B2 | 底库下行通道（全量 SYNC + 增量 UPSERT/DELETE） |
| `10-14-inference-result-egress` | backend + native | B1, B3 | 结果上报通道 + Go 侧落库/派发 |
| `10-14-face-feature-extraction` | backend | B4 | 真实人脸特征写入底库 |
| `10-14-face-algo-package` | native | — | 可安装的人脸识别算法包 |
| `10-14-face-result-frontend` | frontend | B3（消费端） | 检测框叠加 + 识别记录界面 |

## 依赖顺序

父/子结构**不是**依赖系统。以下顺序必须写进各子任务的 prd/implement 并被遵守：

```txt
[决策门] 见「待决问题」——平台 / 人脸能力来源 / 特征提取归属
   │
   ├─ algo-structure-abi ─────┐
   │                          │
   ├─ face-feature-extraction ┤
   │                          ├──→ face-algo-package ──→ [端到端验收]
   ├─ gallery-hot-sync ───────┘                              ↑
   │                                                         │
   └─ inference-result-egress ──→ face-result-frontend ──────┘
```

- `algo-structure-abi` 是 `face-algo-package` 的**硬前置**：ABI 不定，插件写完必返工。
- `face-feature-extraction` 的**归属决策**会反过来约束 `algo-structure-abi`（若在插件内提取，结构化结果需携带特征字段）。两者必须一起决策。
- `gallery-hot-sync` 与 `inference-result-egress` 互不依赖，可并行。
- `face-result-frontend` 只依赖 `inference-result-egress`，可与算法包工作并行。

## 跨子任务验收标准

端到端唯一判据（在**目标硬件**上，非 mock）：

- [ ] **AC1**：新增一名人员 + 上传 2 张同一人的不同照片，`person_faces.feature_vector` 长度符合所选模型维度、**非常量、且两照间 cosine 相似度高于阈值**。
- [ ] **AC2**：算法进程启动后（冷启动全量 + 运行期增量两条路径都测），能在自身索引中命中 AC1 登记的人脸；新增/删除一张人脸后**无需重启**算法进程即可反映变化。
- [ ] **AC3**：把摄像头画面对准 AC1 的人员，算法产出的结构化结果经结果通道到达 Go 宿主，`matched=true` 且 `person_id` 为 `persons.person_id`（**对外业务键原值，不是内部行 ID**）。
- [ ] **AC4**：该结果在前端实时预览上表现为跟随画面的检测框与姓名标签；识别记录可在界面上查到。
- [ ] **AC5**：未登记人员在**同一阈值**下 `matched=false`，不产生误报身份。
- [ ] **AC6**：全链路 `make check` 通过；原生侧在 ASan/UBSan 下无内存错误；Go 侧 `-race` 无数据竞争。
- [ ] **AC7**：上述断点 B1–B4 的四处「未接线」注释与死代码状态被真实消除——**不允许通过删除注释来满足**。

## 待决问题（阻塞子任务规划，必须先答）

1. **目标平台**：`config.toml` 当前 `[algorithm] target = "cpu"`。先在 CPU 上跑通链路，还是直接对齐 RK3568 / RK3588 + RKNN？
2. **人脸能力来源**：自转模型（RetinaFace/SCRFD + ArcFace，ONNX → RKNN）还是对接现成 SDK？**这决定「特征提取」能否放进插件。**
3. **特征提取归属**：插件内（宿主只透传帧，符合现有插件边界）还是宿主内（会打破「`internal/engine` 是唯一可 `import "C"` 的包」这条约定）？

## 范围外

- 视频结构化中的**人体**检测/跟踪（本任务只做「人脸」这一条垂直切片，尽管 `PersonItem` 模型预留了 body/face 1:N 结构）。
- 底库的**加密存储**与**合规审查流程**（`docs/prd/prd-v1.0.md:190` 已列为上线前强制项，本任务不覆盖，但实现时不得让设计更难加密）。
- 识别结果的**长期留存策略**与容量水位联动。
- 前端 App/Chat/Tasks 等模板页面的清理。
