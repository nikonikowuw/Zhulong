# PRD: 目标跟踪标识与抓拍去重 (Target Track Identity & Capture Deduplication)

## 1. 背景与目标 (Goal)

当前感知结果的去重键是**检测框左上角坐标**（`%.3f:%.3f`），带来两个相反方向的缺陷：

- **洪泛**：目标框轻微抖动（抖动 0.001 即换键）→ 同一目标在冷却窗口内反复生成抓拍/识别记录与证据图；
- **误抑制**：不同目标出现在相同坐标（如画面同一位置先后经过两人）→ 后一个目标被前一个的冷却窗口吞掉，直接漏记录。

目标：为感知结果引入**跨帧目标身份关联**，让抓拍去重从"位置近似"升级为"目标身份"，从而在不改算法模型的前提下同时消除洪泛与误抑制。

用户价值：同一目标不再因框抖动而重复产出，不同目标不再因位置相同而互相吞掉记录（**注意：本次不做「轨迹级单条」，同一目标持续在场仍按冷却窗口产多条，见 §6 D1**）；落库的 `track_id` 为后续「行迹回溯」提供可用锚点。

---

## 2. 已确认事实 (Background & Evidence)

### 2.1 现状去重链路

| 事实 | 证据 |
| --- | --- |
| 默认冷却窗口 3s | `internal/record/cooldown.go:9` `DefaultCooldown = 3 * time.Second` |
| 人脸识别去重键：`rec:{camera}:{trackID}`，退化回落 `person_id`，再回落 `box[0]/box[1]` | `internal/record/service.go:646-654` |
| 人体/近景人脸抓拍去重键：近景**恒用 box 坐标**（不看 track），否则 `trackID` 优先，回落 box 坐标 | `internal/record/service.go:656-664` |
| 车辆抓拍去重键：车牌号优先 → `trackID` → box 坐标 | `internal/record/service.go:666-674` |
| **易误读点**：识别键与车辆键的「trackID 优先」目前是**空转**——载荷 `track_id` 恒为 0，实际生效的是 `face.PersonID`（人级）与车牌号（车牌级）分支 | `internal/record/service.go:650-651`、`:667-668` |
| **通用检测路径更粗**：普通目标抓拍键是 `cap:{camera}:{label}`、告警键是 `alarm:{camera}:{label}` —— 全画面同类目标共用一把钥匙 | `internal/record/service.go:358`、`:407`、`:474` |
| 证据图后台队列是**全局单 worker + 容量 32 + 满则丢**，且注释显式假设「每相机受 3s 冷却约束，同时在飞量很小」——该假设由 `cap:{camera}:{label}` 的粗粒度维持 | `internal/record/snapshot.go:116-117`、`internal/record/snapshot.go:167`、`.trellis/spec/backend/record-guidelines.md` §3 |
| 三类记录都是**点证据**：`captures` / `recognitions` / `alarms` 均只有单个 `captured_at`，无 `first_seen`/`last_seen`/区间字段 | `internal/record/model.go:44`、`:67`、`:93` |
| 无连续录像可回溯：仓库内不存在录像写入模块，证据只有抓拍图 | `internal/` 目录清单无录像域 |
| 消费入口 | `internal/record/service.go:57` `ProcessInferenceResult`，按 `res.TaskType` 分派 |
| 展示已就位：响应带 `trackId`，前端 `trackId > 0 ? #n : '-'` | `internal/record/responses.go:20`、`:163`；`web/src/features/records/components/views/captures-view.tsx:212`、`.../captures/capture-card.tsx:95` |

### 2.2 `track_id` 全链路已贯通，只缺生产端

| 层 | 状态 | 证据 |
| --- | --- | --- |
| ABI 结构体字段 | 已存在 | `native/sdk/include/zhulong/plugin.h`；`native/sdk/include/zhulong/algo.hpp` `PersonItem.track_id` / `VehicleItem.track_id` |
| SDK 序列化 | 已序列化进 JSON 载荷 | `native/sdk/src/algo_main.cpp:239`、`:288`（`"track_id":`） |
| Go 解析 | 已解析 | `internal/aitypes/model.go:69`、`:105` |
| DB 列 | 已存在 | `internal/database/migrations/000011_...up.sql:6`、`:50`；`internal/record/model.go:25`、`:86` |
| 生产端 | **全部填 0** | `packages/face_recognition/{onnx,rk3568}/src/pipeline/face_recognition.cpp:59`、`:395`；`packages/plate_recognition/onnx/src/pipeline/plate_recognition.cpp:287` |
| SDK 侧 | 不校验、不禁止，原样序列化插件填的值 | `native/sdk/src/algo_main.cpp:239`、`:288` |

**结论（D3）**：`track_id` 是**宿主生产字段**。算法包若能自己跟踪，它内部的跟踪 ID 只能活在自己的线程/进程内，不得经 IPC 载荷出口——与此相合的现状是四个生产点全填 0。载荷里的 `track_id` 唯一用途是退化判据（`TrackID == 0`），**不是身份来源**。

### 2.3 退化语义契约（对本次改造的硬约束）

- 近景/纯人脸退化项约定 `track_id == 0`，且 `body_box` 与 `faces[0].box` 逐位相等；宿主用该判据把抓拍记成 face 而非 person。
  证据：`internal/aitypes/model.go:76-91` `IsFaceCloseup()`；`native/sdk/include/zhulong/algo.hpp` `PersonItem` 注释。
- 该契约意味着：**插件侧无法为本项目提供目标身份**——退化项（恰恰是最需要去重的场景）按契约必须填 0；而真人体项若由插件填 ID，就又变成「两套 ID 空间」。因此身份只能由宿主生产（§7 / D3）。

### 2.4 帧采样特征（决定跟踪器能放在哪一层）

| 事实 | 证据 |
| --- | --- |
| 运动门禁：画面静止时跳过推理，只在"上次有目标"时补发一条空结果信标 | `native/sdk/src/algo_main.cpp:1056-1066` |
| 结果抑制：仅当 `has_targets || last_had_targets` 才上报 | `native/sdk/src/algo_main.cpp:1137-1145` |
| 背压：单客户端在途上限 2 帧，满载丢弃最新帧 | `native/src/algorithm/ipc/frame_broker.hpp:38` |
| 每路一个 Worker/TaskContext（worker ↔ stream 1:1） | `native/sdk/src/algo_main.cpp:918-936`、`:1005` |
| 宿主侧 AI 结果消费是**每相机一条订阅、一个 goroutine**（多算法复用同一条流） | `internal/camera/stream_hub.go:389`、`:476`；`record-guidelines.md` §1 |
| Go 侧看到的是"有目标的帧全量 + 目标消失时的空信标" | 由以上三条推出；空信标会进入 `ProcessInferenceResult`（`task_type == ""` 走 default 分支返回 nil） |
| 抓拍可按 PTS 精确取历史帧，但**历史环形缓存只保留 4 帧**（推理回传延迟 1~4 帧） | `native/include/Zhulong/engine.h:533-548`（`target_pts` 语义与持帧上限）；Go 侧透传见 `internal/record/snapshot.go:59`、`:225` |
| 单帧 JPEG 编码在 RK3568 上 1080p 约 35~60ms，且禁止在 AI 回调内同步调用 | `native/include/Zhulong/engine.h:556-560` |

**推论**：Go 宿主缺少"帧时钟"，无法做按帧老化的轨迹预测；但去重只需要"有目标的连续帧 + 消失信号"，信息是完整的。

### 2.5 行业形态（决策依据，非需求）

同构产品（DeepStream、Frigate、云端结构化）中，跟踪器位于**能看见连续帧的宿主流水线**，ID 在宿主进程内自增，命名为 **per-stream 作用域**，生存期绑定实例运行期；模型/算法包保持无状态纯函数。"跨摄像机全局唯一 ID"属于多镜重识别（MTMC）产品能力，不在本次范围。

### 2.6 成熟产品的做法（外部调研，用于 D1 决策）

结论先给：成熟产品的默认形态是**「一条轨迹 → 一条权威记录 + 一个最佳证据帧」**，时间窗去重只作为**业务层二次补充**，而不是主要机制。

| 产品 / 项目 | 形态 | 来源 |
| --- | --- | --- |
| Frigate（0.13-） | 事件即「一个被跟踪/被检测到的目标」：*"An event was synonymous with a tracked or detected object."* | [review 文档](https://docs.frigate.video/configuration/review) |
| Frigate（0.14+） | 进一步改为按**时间片聚合**的 review item：*"a review item is a time period where any number of tracked objects were active"* | 同上 |
| Frigate | 事件内维护 `best_image`，并在 `detect → stationary → threshold`（默认 10×帧率）后停止重复推理 | [stationary objects 文档](https://docs.frigate.video/configuration/stationary_objects) |
| FindFace（NtechLab） | 流水线：运动检测 → **人脸跟踪** → 搜索最佳快照 → *"snapshot deduplication (only one snapshot per face detection event)"* | [video-config 文档](https://docs.ntechlab.com/projects/ntech-lab-sp-findface/en/master/video-config.html) |
| FindFace | 另有一层**事件去重**：可按相机组开启 `Deduplicate Events` 并设置秒级去重区间 | [deduplication 文档](https://docs.ntechlab.com/projects/ntech-lab-sp-findface/en/master/deduplication.html) |
| 海康人脸抓拍机 | *"对运动人脸进行检测、跟踪、抓拍、评分、筛选，输出最优的人脸"*；**支持「快速抓拍」与「最佳抓拍」两种模式**；支持人脸去重；一线产品要求抓拍次数 ≥2 张（多帧比对） | 厂商规格页（二手来源） |
| 海康客流 | **去重时间可配置**，配 0 则关闭动态库去重 | 渠道 FAQ（二手来源） |
| 海康 iDS-TSV300 | 内置**投票算法**对同一车多个识别结果综合评分选最优上传；内置**去重算法**使「点位只上传一条记录」 | 经销商页（二手来源） |
| 边缘车牌识别方案（Seeed） | 「每辆车一条记录」；同一车牌在冷却时间内再次到达记为 `suppressed`，不重复动作 | [方案文档](https://wiki.seeedstudio.com/cn/solutions/edge-license-plate-recognition/) |

归纳出的三段式结构：

1. **帧级观测**（算法包，无状态，只吐框）；
2. **轨迹级权威记录**：轨迹结束时（或质量达标时）提交 1 条记录 + 1 个证据帧（best shot / 多帧投票）；
3. **业务层去重窗口**：可配置，默认短或关闭，只用于「轨迹断了导致的重复」或「一次性动作（开闸/上传）」的二次抑制。

> 对本项目的重要含义：「快速抓拍 vs 最佳抓拍」在成熟产品里是**显式模式开关**而不是二选一；而 (a)/(b) 的真实差异在于**记录提交时机**是否绑在轨迹生命周期上。

> ⚠️ **容易被忽略的前提**：上表中「一条记录」指的都是**区间实体**（带 start/end 的事件，背后有连续录像可回溯）。本项目当前的记录是**点证据**（单个 `captured_at`，无录像），直接照搬「一条轨迹一条记录」会丢掉「停留时长 / 持续在场」信息。

---

## 3. 需求清单 (Requirements)

### 3.1 目标身份关联器 (Tracker)

- **R1.1** 在 `internal/record` 内新增目标身份关联器：对每一帧的每个目标框，返回一个在 `(camera_id, algorithm_id, target_type)` 命名空间内稳定的身份 ID。
- **R1.2** 关联判据须覆盖"稀疏采样"场景（相邻两次观测间隔 100~200ms 甚至更长）：不依赖运动模型预测，采用 IoU 与中心距/尺寸一致性双判据，任一命中即接受。
- **R1.3** 匹配必须**一对一**（已匹配的候选不参与后续匹配），避免两人擦身而过被合并成同一身份（会把"多抓"变成"漏抓"）。
- **R1.4** 目标生命周期一律按结果时间戳（`InferenceResult.Timestamp`）老化，**禁止**用帧计数。TTL 不短于冷却窗口（取 2× 冷却窗口），且必须**跟随可变冷却值**（`SetCooldownWindow` 存在，`service.go:41`），不得在构造时固定成常量。
- **R1.5** 状态须有界：条目按命名空间分桶，惰性清理（参照 `internal/record/cooldown.go:37-46` 的阈值+节流风格）。
- **R1.6** 并发安全：每相机一条 AI 订阅、一个 `consumeAIResults` goroutine（`internal/camera/stream_hub.go:389`、`:476`），`Service` 被这些 goroutine 共享；关联器需内部同步或按命名空间分片；`-race` 下必须干净。
- **R1.7** 时间戳单调守卫：同一命名空间内若本帧时间戳早于轨迹 `lastSeen`（算法重发、宿主 `time.Now()` 回落），跳过老化，不得据此误删在跟轨迹。

### 3.2 去重键接入 (Dedup Keys)

- **R2.1** 三个去重键（`recognitionCooldownKey` / `personCaptureCooldownKey` / `vehicleCaptureCooldownKey`）在有身份时使用身份 ID，替代当前的 `%.3f:%.3f` 坐标键，统一用 `trk:` 命名空间与回落键区分（见 design §4.2）。因 `track_id` 由宿主独占，**只有一套 ID 空间**，不需要区分「载荷 ID / 宿主 ID」。
- **R2.2** 近景人脸退化项（`IsFaceCloseup() == true`）必须同样获得稳定身份——按其 `body_box`（等于 `face_box`）参与关联，**不得**改造 `IsFaceCloseup` 契约、**不得**修改原生/AIB 层字段语义。
- **R2.3** 关联失败（首个观测）时行为与现状一致：立即生成记录，不引入额外延迟或"确认帧"等待。
- **R2.4** 去重语义（**已决议 D1 = B**）：每个身份在**每个冷却窗口**（现有 `DefaultCooldown` 3s）内最多产生一条记录；不做"轨迹级单条"、不引入 `first_seen_at`/`last_seen_at`、不新增迁移。已知后果：目标持续逗留时仍按窗口产出点记录（只消除抖动洪泛与同位置互抑，不压缩行密度；真实密度受运动门禁与 TTL 影响，见 §6 D1）。
- **R2.5** 通用检测路径必须同步接入：普通目标抓拍键从 `cap:{camera}:{label}` 改为携带身份，修复"全画面同 label 目标共用一把键"的粗粒度缺陷；label 仍参与分桶。抓拍量从「每 label 1 条/窗口」自然升级为「每身份 1 条/窗口」，不再受全画面粗粒度互抑。
- **R2.6** 告警键（`alarm:{camera}:{label}`）本期**不改**：告警是动作语义，改为身份键会引入"同场景多个同类告警被合并"的产品风险，需单独评估。
- **R2.7** 识别与车辆抓拍采用**两级抑制**：身份键 **与** 人员键（`face.PersonID`）/ 车牌键都放行才产出。理由：接入身份后这两条旧分支会变成死代码，同一已登记人员跨轨迹断链、同一辆车短暂丢框都会在窗口内多出记录（AC11）。两级判定必须**原子**：任一命中即整体拒绝且不更新时间戳，不能用两次 `Allow` 拼接。
- **R2.8（已废除）** 原「每帧每 label 最多 2 条」的人造限制已废除。理由：人工截断会导致已冷却的高分目标长期占领配额，饿死后续合法新目标（冷却饥饿）；抓拍反压由 `SnapshotStore` 既有的队列容量（32）与 `Dropped()` 观测自然承接，不在感知业务层做阉割。

### 3.3 观测与可诊断性

- **R3.1** 记录落库的 `track_id` 一律写**宿主身份**（不再是恒 0），语义为「**仅进程运行期内可比**」；载荷里的 `track_id` **不进库**、不进冷却键。因现有三条断言钉的是载荷透传（`service_test.go:103`=1001、`capture_vehicle_test.go:72`=108、`capture_face_closeup_test.go:85`=42），它们要改为断言宿主身份——这是本次有意的契约变更，不是测试手段。
- **R3.2** 清理/合并事件不写业务日志噪声；仅在 debug 级别可查（避免 3s 级日志刷屏）。
- **R3.3** 关联器需暴露**只读**运行指标（活跃轨迹数、累计分配 ID、匹配命中率），供排障「为什么漏抓/为什么多抓」；不得把指标塞进业务记录或推给前端（§3.4）。

### 3.4 范围外说明

- 本次**不改** `packages/**` 任何插件源码，**不改** `native/sdk/include/zhulong/plugin.h` / `algo.hpp`，**不重新分发算法包**。
- 关联器产出的身份 ID 仅用于宿主内部去重与落库标注，**不**回写进推送给前端的 `ai_result` 事件载荷。
- **边界原则（本条为硬约束）**：`track_id` 是宿主生产字段。算法包若自己维护跟踪 ID，只能在**自己的线程/进程内部**使用，**不得经 IPC 载荷出口**（也不得以任何形式依靠宿主为它命名空间隔离）。载荷里出现非 0 `track_id` 属于违约，宿主**忽略**它，不参与身份、不进冷却键、不进库。

---

## 4. 验收标准 (Acceptance Criteria)

- **AC1 抖动不洪泛**：同一目标框在连续 N 帧中抖动 ±0.002 且顶点跨越原有坐标键边界，冷却窗口内只产生 **1** 条抓拍记录与 1 条识别记录。
- **AC2 同位置不同目标不互抑**：目标 A 产生记录后 1s 内，不同目标 B 出现在与 A 重叠 IoU < 0.2 的位置，B **必定**产生自己的记录。
- **AC3 擦身不合并**：两个目标从两侧靠近、框短暂重叠后分开，各自保持独立身份，输出 2 条记录（不得合并为 1 条）。
- **AC4 稀疏采样不断链**：相邻两次观测间隔 200ms、目标位移导致 IoU 降至 0.15，仍关联为同一身份（由中心距/尺寸判据兜住）。
- **AC5 短时漏检不重抓**：目标连续 3 帧无检测但仍在 TTL 内重新出现且位置合理，不产生第二条记录。
- **AC6 多命名空间隔离**：不同 camera / algorithm / label 的观测互不连线（用「同一帧喂给两个命名空间」验证，不依赖 ID 数值巧合——`nextID` 是进程全局单调，跨相机同号不会发生，按旧判据写不出测试）。同时冷却键必须携带 camera 与 `trk:` 前缀。
- **AC7 内存有界**：目标长时间离开后条目被清理；模拟时钟推进 ≥1 小时后条目数不单调增长（单测显式喂 `at`，不依赖真实时钟）。
- **AC8 质量门禁**：新增测试表驱动、与源码同目录；`make check` 通过（Go 测试带 `-race`）；不触碰 `internal/engine`，无需 native 重建。
- **AC9 载荷 `track_id` 不参与身份**：同一帧内「真人体项载荷 `track_id = 42`」+「近景退化项 `track_id = 0`」，两项的身份都来自关联器（互不相同且 != 42），库里的 `track_id` 也无 42；退化项的 `TargetType` 仍为 `face`，`IsFaceCloseup()` 判据不变。
- **AC10 多目标独立抓拍不饥饿**：`detection` 结果里同一 label 有 3 个首次出现的目标时，3 个目标均成功获得独立身份并各自产出 1 条抓拍记录；后续帧中已冷却的目标被抑制，未冷却的新目标仍能正常产出，不发生冷却饥饿。
- **AC11 两级抑制不回退**：同一已登记人员（`face.PersonID` 非空）在窗口内因断链产生两个身份，仍只产出 1 条识别记录；同一车牌在窗口内因丢框产生两个身份，仍只产出 1 条车辆抓拍。
- **AC12 时间戳回退不误删**：同一命名空间收到时间戳早于前一帧的结果时，已有轨迹不被老化删除。

---

## 5. 明确不做 (Out of Scope)

- 多镜重识别（ReID）、跨摄像机全局唯一 ID、特征底库与轨迹检索 API。
- 越线/方向判定、轨迹持久化表、轨迹回放 UI。
- 插件侧跟踪器（ByteTrack/Kalman 视频模型）、`packages/**` 与原生 ABI/SDK 变更。
- 改动运动门禁、结果抑制或 FrameBroker 背压策略（不为了跟踪去改采样节奏）。
- 冷却窗口时长调整（沿用现有 `DefaultCooldown` 3s）。
- 若后续任务选方案 A：轨迹结束/持续在场提交单条记录 + 最佳帧选择 + 多帧投票（依赖候选帧池，见 §2.4 的 4 帧环形缓存限制）。
- 告警键改造（`alarm:{camera}:{label}` 仍为粗键），需单独评估产品风险。
- `SnapshotStore` 的队列容量、worker 数量与丢弃策略调整（维持既有容量 32 与单 worker，极端瞬时压力由既有 `Dropped()` 观测承接，不在业务层人造截断）。
- 新增 `host_track_id` 列或任何迁移（D3 决定 `track_id` 列归宿主独占，单一语义）。
- 把 `zhulong_plugin.h` 里的 `track_id` 收窄为保留字段、并加 SDK 侧「必须填 0」校验（D3 的后续独立任务；本期宿主只做到「忽略」，不改头文件）。
- 前端任何改动（`trackId` 已透出并渲染）；不改 `ai_result` 事件载荷。

---

## 6. 已决议 (Resolved Decisions)

- **D1 · 记录形态 → 选 B（点记录，不迁移）**

  具体例子（张三站在镜头前 10 分钟，现状会产生约 200 条抓拍）：

  ```
  现状：         09:00:00 / 09:00:03 / 09:00:06 / ... 每 3s 一条 + 每条约 2 张图
  选 B（点）：   同一密度，但不再因框抖动额外爆炸，也不再被同位置的其他目标顶掉
  方案 A（区间）：1 行 first_seen=09:00:00 last_seen=09:10:00 1 张图  ← 本次不做
  ```

  ⚠️ 上表是**示意**：运动门禁在画面静止时跳过推理（§2.4），TTL 6s 老化也会让静止目标周期性重分配身份，真实密度取决于场景。这里不承诺具体行数，只承诺「抖动不再放大行数」。

  决议后果：接受"同一目标每 3s 一条点记录"的行密度；方案 A（加 `first_seen_at`/`last_seen_at`、一条轨迹一行）**推迟为独立任务**，届时可复用本次的身份关联器。

- **D2 · 提交粒度 → 跟随 B**：首次出现即提交（不延迟、不等轨迹结束），后续同身份命中由冷却窗口抑制。行业「最佳抓拍/多帧投票」不在本期（依赖候选帧池，且引擎环形缓存仅 4 帧）。

- **D3 · `track_id` 归宿主独占（原「按项解析 + 键前缀」方案作废）**

  `track_id` 是宿主生产字段；算法包内部的跟踪 ID 只存在包内线程/进程，不得经载荷出口。因此只有**一套 ID 空间**：不需要 `p:` / `h:` 前缀，不需要混合帧合并规则。载荷里的 `track_id` 只作为一个用途——退化判据（`TrackID == 0`）——存在，宿主身份不读它。代价是三条现有断言（载荷透传 1001/108/42）要改为断言宿主身份，且 `plugin.h` 里「将来接入跟踪器时真人体项为非 0」的注释与本原则相左，**列为后续独立任务**（把该字段收窄为保留字段并加 SDK 侧校验）。

- **D4 · 识别与车辆抓拍 → 两级抑制（身份 + 人员/车牌）**

  两条键都放行才产出记录，判定原子。代价是 `CooldownManager` 需要一个新增量 API（`AllowAll`），收益是保住「同一人/同一车在窗口内只留一条」的既有语义，不必在 PRD 里承认降级。

- **D5 · 废除人造产出上限（不设「每帧每 label 最多 2 条」）**

  原 D5 方案试图用 `maxCapturesPerLabel = 2` 保护 `SnapshotStore` 队列，但逻辑推演发现会导致严重缺陷：画面中持续在场的高分目标在冷却期内会霸占 top 2 名额，将低分但从未抓拍过的新目标永远截断在冷却判定之外（冷却饥饿 / Starvation）。因此决议**废除人造产出上限**：所有目标均正常赋予身份，未被冷却抑制的目标自然放行产出抓拍。`SnapshotStore`（容量 32、单 worker、满则丢）保持既有契约，通过 `Dropped()` 指标如实反映后端压力，感知层不做伪限流。

## 7. 技术决策（依据 §2.4、§2.5 得出，非用户选择项）

- **关联器位置：Go 宿主 `internal/record`**。理由：Go 侧是唯一同时拥有"有目标连续帧 + 消失信标 + 冷却状态 + 摄像机/算法命名空间"的一层；单点生成 ID 天然消除跨进程/跨路重号；无需改 ABI、SDK 或重新分发算法包；同时绕开 §2.3 的退化契约冲突。
- **身份 ID 语义**：进程内单调、不复用、永不回写 ABI；文档标注"仅进程运行期内可比"。
- **`track_id` 单一生产者**：宿主独占该字段。算法包内部的跟踪 ID 不得出口（§3.4），因此宿主无需为插件 ID 预留命名空间；头文件语义收紧列为后续任务。
- **禁止**把关联器放入算法插件：`algo_main.cpp` 的 worker 与 broker 1:1，插件内计数器会导致"每路第一个目标都是 1"，展示与检索层必然串号（并且需要宿主下发 ID 前缀才能补救）。
