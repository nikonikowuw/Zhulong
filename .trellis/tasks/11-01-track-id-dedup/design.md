# Design: 目标跟踪标识与抓拍去重

> 需求见 [prd.md](./prd.md)。本文只记录技术设计；D1 已决议为 **B（点记录、无迁移）**。

---

## 1. 定位与边界

- **位置**：`internal/record`（新文件 `tracker.go`）。理由见 PRD §7：这一层同时拥有"有目标的连续帧 + 消失信标 + 冷却状态 + 摄像机/算法命名空间"。
- **不做的事**：不改原生/ABI/SDK/算法包；不改 `internal/camera`、`internal/region`；不做 Kalman 预测与轨迹外推。
- **职责单一**：关联器只回答"这个框和之前见过的哪个目标大概率是同一个"，返回 `int64` 身份 ID。是否抑制、是否落库一律仍由 `CooldownManager` 与 `Service` 决定。

数据流（仅两处变化）：

```text
Runner MSG_RESULT → StreamHub 解析 + ROI 过滤 → record.Service
                                                ├─ tracker.ResolveFrame      ← 新增：赋予身份
                                                ├─ CooldownManager.AllowAll  ← 改造：两级抑制（§4.3）
                                                └─ store / snapshotJob      ← 不变
```

ROI 过滤在 `StreamHub` 已完成（`internal/camera/stream_hub.go:759-766`，仅在 `regionProv != nil && res.AlgorithmID != ""` 且该算法配置非空时生效），关联器看到的永远是过滤后的框——**出 ROI 即视为目标消失**，符合“进区域才抓一次”的产品直觉，不需要额外处理。

---

## 2. 数据结构

```go
// bucketKey 是一路「身份命名空间」。同一 bucket 内的 ID 互斥且单调。
type bucketKey struct {
    camera    string // res.CameraID
    algorithm string // res.AlgorithmID（可能为空串，见 §7 限制）
    kind      string // TaskType，detection 路径追加 label：如 "detection:car"
}
type observation struct {
    box   [4]float64 // 归一化 [x1,y1,x2,y2]
    score float64    // 原始置信度，用于传递给下游记录与快照
}

type activeTrack struct {
    id       int64
    box      [4]float64
    lastSeen time.Time
}

type targetTracker struct {
    mu       sync.Mutex
    cfg      trackerConfig
    buckets  map[bucketKey][]*activeTrack
    nextID   int64
    lastGC   time.Time
}
```

API（**按帧批量**，而不是逐项调用——否则无法做严格的一对一匹配）：

```go
// resolveFrame 为同一帧的 N 个观测返回等长的身份 ID。
// 命中已有轨迹则复用 ID 并刷新 box/lastSeen；未命中则分配新 ID。
func (t *targetTracker) resolveFrame(b bucketKey, obs []observation, at time.Time) []int64
```

---

## 3. 匹配算法

```text
输入：bucket b 的活跃轨迹 T[]、本帧观测 O[]、时刻 at

1. 单调守卫：若 at 早于该 bucket 任一轨迹的 lastSeen（算法重发 / 宿主 time.Now() 回落），
   本帧直接跳过老化，避免误删在跟轨迹（PRD R1.7）
2. 老化：删除 lastSeen + ttl 之前的轨迹（惰性，见 §5）
3. 贪心一对一（按载荷顺序，不排序）：
   对每个 o ∈ O：
     候选 = T 中未被本帧占用的轨迹
     先按 IoU(o.box, t.box) 取最大；若最大 IoU ≥ cfg.matchIoU → 命中
     否则按「中心距 / max(w,h)」取最小；若 ≤ cfg.maxCenterDistRatio
            且 高度比 ∈ [cfg.minHeightRatio, cfg.maxHeightRatio] → 命中
     命中 → t.box = o.box; if at.After(t.lastSeen) { t.lastSeen = at }; 占用 t, 输出 t.id
     否则 → 新建轨迹（id = nextID++）, 输出新 id
4. 返回 ids（与 O 同序）
```

> **不按 score 排序**：贪心匹配不需要顺序保证，而 person 路径的 `BodyScore` 对近景退化项是「人脸质量分」而非检测置信度（PRD §2.3），拿它排序是语义混用，收益为零。

两个判据必须都保留：

- **IoU** 是主判据，但在本系统的稀疏采样下（`max_in_flight_per_client=2` + 运动门禁，见 PRD §2.4）相邻观测可能隔 100~200ms，快走的人 IoU 会掉到 0.15 以下，纯 IoU 会频繁断链 → 洪泛复发；
- **中心距 + 尺寸一致性** 是兜底，尺寸比约束专门防"远处小人 ↔ 近处大人"被错连。

参数默认值（全部集中在 `trackerConfig`，测试可直接覆盖）：

| 参数 | 默认 | 依据 |
| --- | --- | --- |
| `matchIoU` | `0.2` | 稀疏采样；0.5（ByteTrack 默认）在本系统会频繁断链 |
| `maxCenterDistRatio` | `0.5` | 相对 `max(w,h)`，容一次大幅位移 |
| `minHeightRatio` / `maxHeightRatio` | `0.5` / `2.0` | 防远小近大错连 |
| `ttl` | `2 × cooldown`（默认 6s，**跟随可变的 cooldown**） | 短漏检不重抓；又不至于长期占位。`SetCooldownWindow`（`service.go:41`）在运行时可改冷却，TTL 必须从当前冷却值派生，否则 TTL < cooldown 时洪泛复发（PRD R1.4） |
| `gcEntryThreshold` / `gcInterval` | `4096` / `30s` | 参照 `internal/record/cooldown.go:37-46` 的惰性清理风格（实际阈值 >5000 条目、30s 节流、截止 2×cooldown；数值可按身份条目量重估） |

**不设检测分数阈值**：低分框由插件侧后处理决定去留，关联器不再二次过滤，否则会与插件阈值语义打架。

---

## 4. 身份解析、去重键与产出上限

### 4.1 身份只来自宿主（载荷 `track_id` 不参与）

```go
// resolveIdentities 为一次结果中的一类目标返回与输入等长的宿主身份切片。
// payload TrackID 仅作为 IsFaceCloseup() 的判据存在，不在此参与。
func (s *Service) resolveIdentities(res aitypes.InferenceResult) []int64
```

- `track_id` 是**宿主生产字段**（PRD D3 / §3.4）：算法包内部的跟踪 ID 只活在包内线程/进程，不得经载荷出口。因此只有一套 ID 空间，不需要区分「载荷 ID / 宿主 ID」。
- 载荷里的 `track_id` **仅**用于一个目的：`IsFaceCloseup()` 的退化判据（`TrackID == 0`）。它不进身份、不进冷却键、不进库。
- 插件违约填了非 0 值：**忽略**并记一条可限流的 debug 日志（不改变任何行为）；违约由插件负责，宿主不为违约者牺牲自己的契约。
- `DetectionItem` 本就没有 `track_id` 字段（`internal/aitypes/model.go:35-40`），检测路径天然只有宿主身份。
- 解析层 `aitypes.ParseInferenceResult` **不改**（否则会打破自己的解析契约测试与退化判据）。宿主身份**不写回** `PersonItem.TrackID` / `VehicleItem.TrackID`（§6）。

### 4.2 键格式

| 用途 | 有身份 | 无身份回落（保持现状） |
| --- | --- | --- |
| 识别 | `rec:{camera}:trk:{id}` | `rec:{camera}:person:{person_id}` → `rec:{camera}:unmatched:{x}:{y}` |
| 人体 / 近景抓拍 | `cap:{camera}:person:trk:{id}` | `cap:{camera}:face:box:{x}:{y}` / `cap:{camera}:person:box:{x}:{y}` |
| 车辆抓拍 | `cap:{camera}:vehicle:trk:{id}` | `cap:{camera}:vehicle:plate:{no}` → `cap:{camera}:vehicle:box:{x}:{y}` |
| 通用检测抓拍 | `cap:{camera}:{label}:trk:{id}` | `cap:{camera}:{label}` |
| 告警（本期不变） | `alarm:{camera}:{label}` | 同左 |

`trk:` 前缀的作用只是把「身份键」与「人员/车牌/坐标键」区分开（后者是回落与二级判据），不是用来分隔两套 ID。

⚠️ 现有三种写法（`rec:%s:%d`、`cap:%s:person:%d`、`cap:%s:vehicle:track:%d`，见 `service.go:648`、`:661`、`:671`）**统一改**成上表形态，一次到位。冷却表是内存态，无迁移。

坐标键与人员/车牌键保留为**回落与二级判据**，不是死代码。

### 4.3 两级抑制：识别与车辆抓拍（PRD R2.7 / D4）

接入身份后 `face.PersonID` 与车牌号两条旧分支会变成死代码（PRD §2.1 新增行），而它们原本承担「同一人 / 同一车在半窗口内只留一条」的语义。改为两级判定，两条键**都放行**才产出记录：

```go
// AllowAll 是 Allow 的多键原子版：任一键命中窗口 → 整体拒绝且不更新任何时间戳；
// 否则一次性更新全部键并返回 true。
func (cm *CooldownManager) AllowAll(keys []string, now time.Time) bool
```

⚠️ **不能用两次 `Allow` 拼**：第一次返回 true 已写入时间戳，第二次失败会留下「已消费但未产出」的键，导致下一个窗口被误抑制。`cooldown.go` 的改动因此是**增量**（新增 `AllowAll` + 一个只读 `Window()`），既有 `Allow` 与调用点不动。

| 路径 | 参与判定的键 |
| --- | --- |
| 识别 | `AllowAll([identityKey, personKey?])`，`personKey` 仅当 `face.PersonID != ""` 时参与 |
| 车辆抓拍 | `AllowAll([identityKey, plateKey?])`，`plateKey` 仅当车牌非空时参与 |
| 人体 / 近景抓拍 | 单键（不涉及 `person_id`） |
| 通用检测抓拍 | 单键（带身份，见 §4.4 上限） |
| 告警 | 不变（粗键） |

### 4.4 检测路径产出与背压解耦（废除原 D5 人工截断）

废除原 `maxCapturesPerLabel = 2` 的粗暴人造限制。理由：按 score 截断会导致画面中持续在场的高分目标在冷却期内霸占 top 2，将低分但从未抓拍过的新目标永远截断在冷却判定之外（冷却饥饿 / Starvation）。

现定方案：
- 组内目标正常遍历，已处于冷却期内的目标跳过，未冷却的目标正常放行产出抓拍与快照。
- 容量保障：`SnapshotStore`（容量 32、单 worker、满则丢）保持既有契约，通过 `Dropped()` 指标如实反映后端压力，感知层不做伪限流，杜绝业务饥饿。

---

## 5. 装配、生命周期、并发与可见性

- **装配**：`Service.tracker` 在 `NewService`（`service.go:26`）里直接构造——关联器无外部依赖、不开文件、不起 goroutine，符合 Fx「构造函数纯粹」的约束；**不走 setter**（`SetSnapshotStore` 用 setter 是因为它依赖 camera/storage 生命周期，情境不同）。
- **并发**：`Service` 被所有相机 goroutine 共享——每相机只有一条 AI 订阅与一个消费 goroutine（`stream_hub.go:389` `ResolveAIStreamBrokerPath(cameraID)` → `stream_hub.go:476` `go consumeAIResults`），同相机挂多算法也复用同一条流。关联器单锁保护；锁内只做 `O(n·m)` 匹配，n/m 为个位数到十几，微秒级；不需要分片。`-race` 必须干净。
- **时钟**：一律用 `InferenceResult.Timestamp`（`capturedAt`），**禁止** `time.Now()` 与帧计数。时间戳缺失时 `Service` 已回落宿主 UTC 时间（`service.go:62-65`），沿用即可；但回落会让同一 bucket 内时间戳非单调，因此必须保留 §3 步骤 1 的单调守卫。
- **TTL 与冷却的耦合**：`ttl` 不从常量派生，每次使用时取 `CooldownManager` 当前窗口 ×2（需新增只读 `Window()`）。否则 `SetCooldownWindow` 调大冷却后 TTL < cooldown，目标一离开就换身份，洪泛复发。
- **清理**：`len(buckets 总条目) > gcEntryThreshold` 且距 `lastGC ≥ gcInterval` 时删除 `lastSeen + ttl` 之前的条目与空 bucket。
- **空结果信标**（`task_type == ""`）：**不**做显式清除。AI 流按相机共享多个算法，空信标不带 `algorithm_id`，据此清桶会误杀其它算法的在跟目标；交给 TTL 自然老化。
- **可见性**：
  - DB：`track_id` 列只写**宿主身份**（单一语义、单一生产者，PRD R3.1 / D3）。载荷里的 `track_id` 不进库。
  - 响应层与前端渲染已就位（PRD §2.1 末行），无需改动。
  - 运行指标：关联器暴露只读统计（活跃轨迹数、累计分配 ID、匹配命中率）供排障「为什么漏抓/多抓」；走既有观测出口、debug 级，不刷业务日志（R3.2 / R3.3）。

---

## 6. 与退化契约的关系（明确不动）

`IsFaceCloseup()`（`internal/aitypes/model.go:86-91`）保持原判据：`TrackID == 0 && len(Faces)==1 && BodyBox == Faces[0].Box`。

- 退化判据读的是**载荷** `TrackID`，而插件在退化项上恒填 0；宿主身份只活在 `identity` 里，因此判据完全不受影响；
- 宿主关联得到的身份只用于**去重键与落库列**，不写回 `PersonItem` / `VehicleItem`；
- 因此近景人脸抓拍既能获得稳定身份，又不会污染"近景/真人体"的区分。
- ⚠️ 必须有一条回归测试钉住「宿主身份不写回 `TrackID`」：`IsFaceCloseup()` 的第一个判据就是 `TrackID != 0`（`internal/aitypes/model.go:86`），一旦有人图省事把宿主 ID 回写，近景记录会被静默记成 `person` 且 `capture_face_closeup_test.go` 仍可能是绿的。

---

## 7. 已知限制与风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 误合并（两人擦身） | 第二个目标被抑制 → 漏抓拍 | 严格一对一 + 高度比约束 + TTL 6s；AC3 覆盖 |
| 断链（快移 + 稀疏采样） | 同一目标多身份 → 仍会重复 | IoU 0.2 + 中心距兜底；AC4 覆盖 |
| 插件违约填了非 0 `track_id` | 若被当身份用，会与宿主 ID 抢同一个字段 | 宿主**忽略**载荷值（§4.1），并记可限流 debug 日志；头文件语义收窄列为后续任务 |
| 插件把近景退化项填成非 0 | `IsFaceCloseup()` 失效 → 脸被记成 person | 属于插件违约（契约要求填 0）；宿主不为违约者牺牲解析契约；以 AC9 钉住宿主行为 |
| 识别 / 车牌旧分支被绕过 | 同一人 / 同一车窗口内多出记录 | 两级抑制 + 原子 `AllowAll`（§4.3）；AC11 覆盖 |
| 检测路径抓拍量乘数（R2.5） | 队列满 → 证据图丢失（记录仍在） | 废除人造截断，靠 SnapshotStore 既有容量 32 与 `Dropped()` 观测承接，AC10 覆盖不饥饿 |
| `res.AlgorithmID` 为空时多算法共用桶 | 同一相机两算法可能互相连线 | 桶键含 `kind`（TaskType/label）；同 TaskType 且都缺 `algorithm_id` 的极端场景标注为已知限制 |
| 时间戳非单调 | 老化误删在跟轨迹 | §3 步骤 1 的单调守卫；AC12 覆盖 |
| 相机/算法重启后 ID 回绕 | 历史记录出现相同 `track_id` | 语义已声明"仅进程运行期内可比"；冷却窗口 3s 内撞车概率低且影响有限（另立任务处理持久计数） |
| 关联器 bug 导致大面积漏抓 | 证据丢失 | 坐标键 / 人员键 / 车牌键回落路径全部保留；关闭能力 = 让 `resolveIdentities` 恒不返回宿主身份（单点开关） |

**回滚**：无迁移、无 schema 变更。回滚 = revert `tracker.go` + `cooldown.go` 的 `AllowAll`/`Window` 增量 + 4 处调用点 + 3 个键函数。

---

## 8. 验证策略

- 单元（`tracker_test.go`，表驱动 + 显式传入 `at`，无真实时钟）：首次分配、抖动复用、快移兜底、擦身不合并、TTL 过期、桶隔离（camera/algorithm/label 三维）、GC 生效、空观测不 panic、时间戳回退不误删、TTL 跟随可变冷却。
  - 现有 `internal/record` 测试全部是 `package record_test`；需要断言内部条目数时，同目录允许 `package record` 与 `record_test` 共存，或改用只读统计方法（后者同时服务 R3.3）。
- 集成（`service_test.go` 现有风格）：同一 track 抖动只产 1 条抓拍 + 1 条识别；同位置不同目标各产 1 条；`detection` 路径同 label 多个目标首次出现各产 1 条抓拍且不发生冷却饥饿（AC10）；载荷 `track_id = 42` 不进身份也不进库、退化项的 `TargetType` 仍为 `face`（AC9）；两级抑制不回退（AC11）。
- 键契约：表驱动断言四类键格式与 `trk:` 前缀，防止有人回退成旧格式（§4.2）。
- 命令（`internal/record` 间接依赖 `internal/engine`，**必须**走 wrapper）：

```bash
python3 native/scripts/build.py go test -race ./internal/record -run 'TestTargetTracker|TestService'
python3 native/scripts/build.py go test -race ./internal/record
make go-check
```
