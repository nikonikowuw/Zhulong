# Implement: 目标跟踪标识与抓拍去重

前置：已阅读 `.trellis/spec/backend/record-guidelines.md`、`.trellis/spec/backend/quality-guidelines.md`。
设计见 [design.md](./design.md)，需求见 [prd.md](./prd.md)（D1 = B 点记录无迁移；D3 `track_id` 归宿主独占；D4 两级抑制；D5 废除人工截断，消除冷却饥饿）。

---

## 0. 开工前必须先落地的契约（评审修订，勿跳过）

| 契约 | 落点 |
| --- | --- |
| `track_id` 归**宿主独占**：载荷值不进身份、不进冷却键、不进库 | PRD D3 / R3.1 |
| 载荷 `track_id` 的唯一用途是退化判据（`TrackID == 0`），解析层不动 | design §4.1 |
| 身份键统一用 `trk:` 前缀，单命名空间（无 `p:` / `h:`） | design §4.2 |
| 识别 / 车辆抓拍走**两级抑制**，需要原子 `AllowAll` | design §4.3 |
| 检测路径不设人工截断，杜绝高分冷却目标霸占配额导致的饥饿 | design §4.4 |
| 宿主身份**不写回** `PersonItem.TrackID`，也不回写 ABI / 前端 | design §6 |

---

## 1. 有序改动清单

- [x] **S0 · 影响面审计（先做，不改代码）**
  - 列出语义会被改变的既有断言与注释：`service_test.go:103`（`TrackID == 1001`）、`capture_vehicle_test.go:72`（`108`）、`capture_face_closeup_test.go:85`（`42`）、`capture_face_closeup_test.go:59` 的注释（"落在不同 cooldown key"）。
  - 三条 `TrackID` 断言均从「载荷透传」改为「宿主身份」：按契约它们**应该改**，但必须在 PR 描述里写明「这是因为 D3 把 `track_id` 改为宿主独占」，而不是默默改绿。
  - 解析层契约**不动**：`internal/aitypes/model_test.go:165`（10）、`:200`（42）、`service_test.go` 里其余载荷字段均保留原断言。
  - 记下本次不碰的边界：`aitypes` 不清零、不拦截载荷 `track_id`（它仍被 `IsFaceCloseup()` 读）。
- [x] **S1 · 新增关联器** `internal/record/tracker.go`
  - `bucketKey` / `observation` / `activeTrack` / `trackerConfig` / `targetTracker`（照 design §2、§3）。
  - `resolveFrame(b, obs, at) []int64`：单调守卫 → 老化 → 贪心一对一（IoU 主判据、中心距+高度比兜底）→ 命中时 `lastSeen` 单调防倒退更新（`if at.After(t.lastSeen)`）→ 返回等长 ID。**不排序**。
  - 惰性 GC（阈值 + 节流），空 bucket 一并回收。
  - 单构造入口 `newTargetTracker(cfg trackerConfig)`；`ttl` 从 `CooldownManager.Window() × 2` 派生，不写死。
  - 暴露只读统计（活跃轨迹数 / 累计分配 / 命中率）供 R3.3，debug 级输出。
- [x] **S2 · `cooldown.go` 增量 API**（`Allow` 与既有调用点不动）
  - `AllowAll(keys []string, now time.Time) bool`：任一键命中窗口 → 整体拒绝且**不更新任何时间戳**；全部放行 → 一次性写入全部键。
  - `Window() time.Duration`：只读当前冷却窗口，供 tracker 派生 TTL。
  - 单测覆盖：部分命中不消费、全部放行只写一次、空 keys 的处理。
- [x] **S3 · 单元测试** `internal/record/tracker_test.go`（表驱动，显式传 `at`）
  - 首次分配 / 抖动复用 / 快移（IoU<0.2 但中心距命中）/ 擦身不合并 / TTL 过期重分配 / 桶隔离（camera、algorithm、label 三维各一例）/ GC 后条目数下降 / 空观测不 panic / 时间戳回退不误删 / TTL 跟随 `SetCooldownWindow`。
  - `internal/record` 现有测试全为 `package record_test`；需断言内部条目数时，同目录放 `package record` 亦可，或走 S1 的只读统计（优先后者，顺带覆盖 R3.3）。
- [x] **S4 · 身份解析接入**（`internal/record/service.go`）
  - 新增 `resolveIdentities`（design §4.1）：身份**只**来自 `resolveFrame`；载荷 `TrackID` 不参与（只由 `IsFaceCloseup()` 读）。
  - `processPersonStructure`：`res.Persons` 的 `BodyBox`/`BodyScore` 组成 `[]observation` **一次性** `resolveFrame`，再按索引取身份；近景退化项同样按 `BodyBox` 参与（其 `body_box == faces[0].box`）。
  - `processVehicleStructure`：以 `VehicleBox`/`VehicleScore` 组观测，同上。
  - 违约观测：载荷 `TrackID != 0` 时记一条可限流的 debug 日志（不改行为），供日后收紧头文件时收集证据。
  - **不改** `aitypes.PersonItem`、**不改** `IsFaceCloseup()`、**不写回** `p.TrackID`。
- [x] **S5 · `processDetection` 普通目标分支**
  - 按 `d.Label` 分组，每组一个 `bucket.kind = "detection:" + label`，组内一次 `resolveFrame`（`DetectionItem` 无 `track_id` 字段，只能是宿主身份）。
  - 组内目标正常遍历检查冷却：已在冷却期内的目标跳过，未冷却目标正常放行并产出抓拍，不设人工截断，彻底消除冷却饥饿。
  - 键：有身份 `cap:{camera}:{label}:trk:{id}`，无身份保持 `cap:{camera}:{label}`。
  - 告警分支**不动**。
- [x] **S6 · 键函数改造**（design §4.2 / §4.3）
  - `recognitionCooldownKey` / `personCaptureCooldownKey` / `vehicleCaptureCooldownKey` 支持宿主身份，统一 `trk:` 前缀（`rec:{c}:trk:{id}`、`cap:{c}:person:trk:{id}`、`cap:{c}:vehicle:trk:{id}`）；`personCaptureCooldownKey` 的身份判据提到 `isCloseup` 之前。
  - 识别与车辆抓拍改调 `AllowAll([identityKey, personKey|plateKey?])`；人体/近景抓拍与检测抓拍仍单键。
  - 坐标键 / `person_id` 键 / 车牌键全部保留为回落或二级判据，不是死代码。
  - `buildRecognitionRecordAndJob` / `buildPersonCaptureRecordAndJob` / 车辆记录构造增加 `trackID int64` 参数（或等价传参），按 §0 契约写最终生效值——**这是 R3.1 的唯一落地点，别靠改 `p.TrackID` 抄近路**。
- [x] **S7 · 集成测试**（`service_test.go` 现有风格，必要时新增 `capture_track_dedup_test.go`）
  - 同一 track 连续 3 帧框抖动 ±0.002 → 仅 1 条 capture、1 条 recognition；
  - 目标 A 出记录后 1s，IoU<0.2 的另一目标 B → 必有自己的记录；
  - `detection` 路径同 label 多个首次出现目标均产出 capture（AC10），且后续帧已冷却目标被抑制时未冷却新目标仍能正常产出（不饥饿）；
  - 空结果信标（`task_type == ""`）不 panic、不清空其它桶；
  - 混合帧场景（载荷 `TrackID: 42` + 退化项 `TrackID: 0`）→ 两项身份均来自关联器、库里无 42、退化项 `TargetType == "face"`（AC9）；
  - 同一已登记人员 / 同一车牌因断链产生两个身份 → 窗口内仍只 1 条（AC11）；
  - 键格式表驱动断言 `trk:` 前缀（防回退成旧格式；不存在 `p:` / `h:` 两套空间）。
- [x] **S8 · 更新 spec**
  - `.trellis/spec/backend/record-guidelines.md` §1 目前只有一句「按 cooldown 去重」。本次引入的是**可执行契约**，必须写进去：`track_id` 归宿主独占且算法包内部跟踪 ID 不得出口、身份 ID 语义（仅进程运行期内可比）、四类键格式与 `trk:` 前缀、两级抑制与原子 `AllowAll`、抓拍队列容量假设与 `Dropped()` 观测。
  - §3 明确抓拍队列通过既有 `Dropped()` 承接瞬时并发压力，感知层不设人造截断。
- [x] **S9 · 回归钉住**
  - 断言「宿主身份不写回 `PersonItem.TrackID` / `VehicleItem.TrackID`」；理由见 design §6：`IsFaceCloseup()` 第一判据就是 `TrackID != 0`，回写会让近景记录静默变成 `person`。
  - 断言「载荷 `track_id` 不进库、不进冷却键」（AC9），以钉住 D3 的宿主独占口径。

---

## 2. 高风险文件与注意点

| 文件 | 风险 | 注意 |
| --- | --- | --- |
| `internal/record/service.go` | 核心消费路径，改动面最大 | 只加身份解析与两级抑制，不动批量写入/快照入队顺序；失败路径仍需"记录写失败则不排快照任务" |
| `internal/record/cooldown.go` | 共享冷却状态 | **只做增量**（`AllowAll` + `Window`），不把 TTL 或身份状态塞进冷却管理器；既有 `Allow` 语义不变 |
| `internal/record/snapshot.go` | 全局单 worker + 队列 32 + 满则丢 | **不改**容量与丢弃策略；`Dropped()` 保持为观测量承接多目标瞬时并发 |
| `internal/record/responses.go`、`web/**` | 无需改动 | `trackId` 已透出与渲染；若发现改动必要，先回 PRD 确认 |
| `internal/aitypes/model.go` | 退化契约 + 解析契约 | **禁改** `IsFaceCloseup()`；也不要在解析层清零/拦截 `track_id`（它仍被退化判据读，且会打破 `model_test.go` 的解析契约） |
| `internal/region/*`、`internal/camera/*` | 无需改动 | ROI 过滤在 StreamHub 已完成，关联器只看过滤后的框 |

---

## 3. 验证命令

```bash
# internal/record 间接依赖 internal/engine，必须走 wrapper（裸 go test 会复用陈旧归档）
python3 native/scripts/build.py go test -race ./internal/record -run 'TestTargetTracker|TestCooldown|TestService'
python3 native/scripts/build.py go test -race ./internal/record

make go-check      # ./cmd/... ./internal/...，内部已走 wrapper
make check         # 收工门禁：前端 lint/type-check/test + native-test + api-docs + go-check
```

- C++ / 迁移 / 前端均无改动 → 不需要 `make native-build`，不需要新迁移，不需要 `internal/webui/dist` 重建。
- `-race` 为强制项（AGENTS.md 硬性约定）。

---

## 4. 开工前检查

- [x] PRD 的 D1/D2/D3/D4/D5 已决议（B / 跟随 B / `track_id` 宿主独占 / 两级抑制 / 废除人工截断），无残留阻塞项。
- [x] §0 的六条契约已逐条确认，S0 影响面审计完成。
- [x] `implement.jsonl` / `check.jsonl` 已含真实 spec 条目（`task.py validate` 通过）。
- [x] 用户已明确批准本计划（`task.py start` 之前）。
