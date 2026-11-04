# 存储熔断自愈与快照容量淘汰

## Goal

让边缘设备在磁盘写满后**在仍有可回收媒体的前提下无需人工干预即可自愈**：空间压力判定口径统一，熔断期间仍会执行清理并自动解除；普通抓拍图在容量压力下允许提前回收；物理删除的快照同步出清 DB 悬空记录（图亡行灭，拒绝无图幽灵记录）；告警/锁定证据不被自动删除且占用可观测。

收益：消除「高水位未到、熔断已到、清理永不唤醒」的死区，消除「纯快照盘清理 0 释放」的空转，保证证据真实性与无破图体验，把停录熔断从常态结果降级为真正无法回收时的最后保险。

---

## Background & Confirmed Facts

### 现场故障（RK3568 开发板，2026-11-04）

| 事实 | 证据 |
|---|---|
| 熔断触发 | `audit_logs`：`2026-11-04 01:26:22Z storage.emergency_stop`，detail `insufficient disk space: free space below minimum safe margin` |
| 至今未恢复 | 无 `storage.emergency_resume` 记录 |
| 触发分支是 min-free，不是 95% | `free = 2,139,811,840 B = 2040.7 MiB < 2048 MiB`；`used/total = 26.88G/30.31G = 88.7%` |
| 熔断时配置为默认 high=90 | 配置更新审计在 `01:26:22` **之后**（`01:39:17` 改为 high=85/low=70） |
| 熔断后抓拍停写 | 最后一张图 `01:26:21Z`；之后 1086 条 `captures` 的 `image_path` 为空 |
| 快照占满媒体根 | `snapshots/` 7.7G：11-02 732 文件 128M；11-03 43,580 文件 7.5G（08:55–18:13）；11-04 404 文件 72M |
| 快照全部在 TTL 内 | TTL 90 天 → cutoff `2026-08-06`；无候选 |
| 没有可删的录像/导出 | 媒体根下只有 `snapshots/`，无 `recordings/`、`exports/` |
| 没有锁定证据 | `alarms` 表 0 行，`.lock` / `_locked` 文件 0 个 |
| 高水位清理即便运行也释放 0 | 三级级联前两级无候选，快照只按 TTL 筛选 |

### 代码根因

1. **口径死区**：`percentUsed(used, total)`（`internal/storage/model.go:45`）与 `free < minFree`（`internal/storage/gate.go:199`）是两个不同口径。ext4 保留块让 `used/total` 比 `df` 的 `used/(used+avail)` 系统性低约 4~5 个点。默认 high=90 时 88.7% 不构成 `warning`，但 free 已跌破熔断线 → 自动清理从未被唤醒。
   - ⚠️ **前提**：该口径只在服务以**非 root** 运行时与 `df` 的 Use% 一致。Linux 对持有 `CAP_SYS_RESOURCE` 的进程通常令 `f_bavail == f_bfree`，此时 `used/(used+free)` 退化为 `used/total`。现场证据（`free=2.04G`，而 `total-used=3.43G`）说明板端服务是非 root；root 部署仍会存在同一口径偏差，只能由 `EmergencyStopMinMB` + 熔断期清理兜底。
2. **熔断状态不清理**：`Service.tick()` 只在 `status.Status == StatusWarning` 时启动 prune（`internal/storage/service.go:280`）。`EmergencyGate` 在 emergency 下返回 `StatusEmergencyStopped`（`gate.go:220`），因此熔断后清理永不运行，与 spec §3.2「清理使空间恢复后自动解除熔断」矛盾。
3. **快照缺少容量维度**：`collectSnapshots` 只按 `SnapshotsRetentionDays` 过滤（`cleaner.go:304`）；`recordings` 同时有 TTL 与 FIFO（`cleaner.go:330`）。快照占盘 100% 时容量策略完全失效。
4. **删除顺序注释与实现矛盾**：`cleaner.go:173` 注释称「抓拍图证据价值高于录像」，却把快照排在录像之前删除。
5. **TTL 被当作下限**：`SnapshotsRetentionDays` 实际语义是「未到期绝不删」，与工业 NVR「保留天数是上限、写满即覆盖」相反。

### 规范冲突

- `.trellis/spec/backend/storage-guidelines.md` §2.2 三级级联只覆盖 `exports` 与 `recordings`，没有快照容量淘汰条款。
- 同节第 4 条「铁律」只保护**带告警标记或 `.lock` 标记**的高价值证据，普通快照并未获得免删特权，说明实现少了一半。
- §3.2 要求熔断后由清理恢复，但实现从未在 emergency 下清理。
- §2.4 规定 DB 行由 `record.RetentionPruner` 处理，本任务**仍保持 storage 不依赖 record 的边界**，但会扩展 `RetentionPruner` 的按 ID 级联入口，并新增一张 pending 表用于失败重试；该边界变化需同步 record 规范。

### 工业对照（公开文档，供决策参考）

- 海康 NVR：`Video Expiry Time` 默认 0 = 写满覆盖最旧；Storage Mode 支持按通道配额。
- ZoneMinder：`PurgeWhenFull` 按磁盘百分比删最旧事件。
- Frigate 0.12：剩余不足约 1 小时启动 emergency cleanup，无视 tier 删最旧，但保留 retained events。
- Milestone XProtect：Evidence Lock 保护不被 retention 删除，并在 Storage 页用垂直红线显示锁定占用。

结论：**容量是硬约束，TTL 只是上限；停录只能是最后保险。**

---

## Requirements

### R1（P0）统一空间压力判定口径

- 新增单一入口计算「用户可用空间占用比」：`used / (used + free)`，与 `df` 对齐；`TotalBytes` 仅用于展示容量。
- 采用**向上取整算法**（`(used*100 + available - 1) / available`），避免整数除法向零截断把 `89.9%` 算成 `89%` 而在 `high=90` 临界点错过 warning 唤醒。
- `gate.Evaluate`、`cleaner.RunPrune`、`isAboveLowWatermark`、`service_refresh` 的 `UsagePercent` 全部改用该口径。
- **不承诺任意卷上 warning 一定先于 emergency**：emergency 同时受百分比与 `EmergencyStopMinMB` 约束，小卷/高 `EmergencyStopMinMB` 下 min-free 可能先触发。本任务保证的是「gate 的封禁条件与清理止点同序」，即 emergency 清理必须能让 gate 自行解除。
- ⚠️ 口径对齐依赖服务以非 root 运行；root 部署下 `Bavail == Bfree`，公式退化为 `used/total`，不能消除原死区，必须由 emergency 清理兜底。测试必须同时覆盖「保留块」与「root-like（`free == total-used`）」两种统计。
- 验收：构造 `total=100, used=85, free=5`（保留块 10）的统计，`UsagePercent=95`（向上取整），`high=90` 触发 warning 而非 healthy/emergency。

### R2（P0）熔断期间自动清理与应急安全阀

- `Service.tick()` 在 `StatusWarning` **与** `StatusEmergencyStopped` 时都允许启动清理；沿用 `CleanerEngine.running` CAS 防重入。
- 新增 `PruneTriggerEmergency`；应急清理不受「usage ≥ high」门槛限制（熔断本身就是最高门槛）。
- **应急止点必须覆盖 gate 的两个触发分支**：`free >= EmergencyStopMinMB × 1.25` **且** `usagePercent < HighWatermarkPercent`。只满足 free 条件时若 usage 仍高于 `EmergencyStopPercent`，清理必须继续，否则 gate 永远无法解除。
  - `usagePercent < HighWatermarkPercent` 同时避开「清理结束立刻又进入 warning 清理」的连锁。
  - 若 `HighWatermarkPercent == EmergencyStopPercent`（配置允许），止点取 `usagePercent < EmergencyStopPercent`，仍保证 gate 能解除。
- **进入清理前先检查止点**：若止点已满足（例如 emergency 由百分比触发而 free 本就高于 1.25×minFree），只记审计/日志，不先删一批文件。
- `minFree × 1.25` 在运行时必须以卷容量封顶（建议 `min(1.25 × minFree, total × 0.9)`），避免小卷上止点大于总容量、gate 永久无法解除；配置保存时若 `minFree × 1.25 >= total` 应拒绝并给出稳定错误码。
- 清理结束后 `RefreshStatus`，由 gate 正常产生 resume 边沿；不重启、不人工干预。
- 若候选耗尽仍无法同时满足两个止点，**保持熔断**并记录 `stop_reason=candidates_exhausted`；这是「确实无法回收」，不是自愈成功，不能假装恢复。
- 验收：
  - min-free 触发：mock 探针模拟 `free < minFree`，tick 后候选被删且 `free >= 1.25×minFree`、`usage < high`，gate 回调触发 `storage.emergency_resume` 审计。
  - percent 触发：`free` 已高于 `1.25×minFree` 但 `usage >= EmergencyStopPercent`，清理必须继续删除直到 `usage < high`，不得在第一批后停止。

### R3（P0）普通快照纳入容量淘汰

- `collectSnapshots` 在 TTL 候选之后追加容量候选：未过期、未锁定、非活跃的普通快照按 mtime 最旧优先。
- 触发语义：
  - `watermark` / `emergency` 自动启用容量淘汰；
  - `manual` 也启用，但**只在 Phase 2 进入前检查止点**：若当前已低于 low watermark，则 Phase 2 一个文件都不删；健康盘上点「立即清理」只清 TTL 超期媒体，不清未过期快照。
- **TTL 语义改为上限**：保留天数表示「超过它即可被回收」，不是「到点必删」。低于高水位时自动清理不启动，超期文件可以继续占用；本任务不新增低频 TTL 巡检（见 Out of Scope）。
- 活跃保护：跳过 `now - mtime < activeFileGrace`（建议 2 分钟）的文件；实现为 `CleanerOptions` 可覆盖项，测试不依赖真实时钟。
- 验收：
  - 构造 3 天前（TTL 90 天）的快照占满盘，`RunPrune(warning)` 在 low watermark 前停止且删除了最旧文件；带 `.lock` 的文件字节数不变。
  - 健康盘（usage < low）上 `RunPrune(manual)` 对未过期快照删除数为 0；TTL 超期文件仍被清理。
  - 活跃文件（`now-mtime < grace`）所在组整组跳过，边界时刻可测。

### R4（P0）两阶段删除顺序与保护规则修正

- 顺序调整为**两阶段阶梯淘汰**，优先出清全量超期负债，再动刀未过期合规媒体：
  1. **Phase 1（TTL 超期出清）**：`exports` (TTL) → `recordings` (TTL 过期) → `snapshots` (TTL 过期)。**Phase 1 全量执行，不受容量止点限制**；超期即出清，这正是「全量」的含义。
  2. **Phase 2（容量压力阶梯淘汰）**：`recordings` (未过期 FIFO 最旧优先) → `snapshots` (未过期容量淘汰，最旧优先)。进入 Phase 2 前先检查止点；之后每批/每单元后检查，达到止点立即退出。
- 锁定证据绝对不参与删除。
- 修正 `cleaner.go:173` 的误导性注释，并在 spec §2.2 增加快照容量淘汰条款与两阶段阶梯说明；注释必须引用正确章节（§2.2，不是 §4）。
- 验收：
  - 同时存在超期快照与未超期录像时，超期快照先被删除（Phase 1 全量）。
  - **同一阶段内**，未超期录像先于未超期快照被删除；跨阶段不比较。
  - 删除日志按层与阶段输出计数。

### R5（P0）删除粒度改为记录组

> 上调为 P0：R3 的容量淘汰和 R8 的按 ID 级联都依赖「整组原子删除」，否则会退化成半删/误删。R5 必须与 R3、R8 同一版本发布。

- 快照按同目录下基名前缀聚组：以 `{dir}` 与去除扩展名和 `.lock` 后的第一段前缀（`strings.Split(base, "_")[0]`）作为组键，将主图、`_crop` 图、`_face` 图及其 `.lock` 视为一个原子删除单元。
- 不强绑定 26 字符 ULID 格式，天然兼容测试用例（如 `rec-1.jpg` 与 `rec-1_face.jpg`）与各类非标准前缀；但**只有快照组才允许把前缀当作 DB 主键传播**，录像组不参与 DB 级联。
- 组内任一 `.lock` / `_locked` 存在 → 整组跳过；删除时整组删，避免「只删主图留 crop」导致 `RetentionPruner` 永远无法删行。
- 组内**全部媒体文件已删除或已不存在**才把 ID 交给 R8；只要还有任一媒体文件存在（例如权限失败），保留 DB 行并记 `warn`，不制造新的幽灵。
- 组排序键取组内最早 mtime；释放字节按组累计。删除每个 unit 前重新检查保护标记；已保护则整组跳过，`.lock` 不参与删除。
- 验收：
  - 一组两文件删除后，不存在只删一半的中间态；带锁组 0 删除；非 ULID 命名的关联图也能成组删除。
  - 组内一个文件删除失败、另一个成功时，DB 行保留且日志出现可定位的 warn。

### R6（P1）锁定证据占用可观测

- 后端字段在本任务的 P0 观测项落地：`CleanupSummary.ProtectedFiles` / `ProtectedBytes` 通过审计/日志输出；本条的 P1 部分只剩前端展示与后续预算分析。
- 清理日志与审计记录 `protected_skipped_files` / `protected_skipped_bytes`，让运维看到「有证据被保护、所以无法继续回收」。
- 不在本任务新增 `StorageHealthStatus` 枚举值，不改 `StorageStatus` schema；前端类型零破坏。
- 锁定证据仍**绝对不自动删除**；预算告警（如锁定占比 > 30%）列为后续任务候选，需产品决定阈值与拒绝策略。
- 保护统计只在清理实际运行时产生；没有清理就没有实时占用数据，这是 D7 的已知取舍。
- 验收：构造带 `.lock` 的快照，自动清理后审计 detail 含跳过文件数与字节数。

### R7（P0）清理可观测性

- 新增审计 action `storage.auto_cleanup`（system 用户，无 IP），记录 trigger、deleted、freed、targetStatus、protected 统计与 `stopReason`。
- `CleanupSummary` 增加 `StopReason`（`target_reached` / `candidates_exhausted` / `context_canceled` / `skipped_below_high_watermark`），让 `deleted=0` 能说清原因。
- 自动清理候选为 0 或未达止点时，日志级别从 `info` 提升为 `warn`，明确输出 `deleted=0` 与原因；**warning 与 emergency 共用退避/节流**，不能每 15s 扫一遍 4 万文件打一条 warn。
- 手动清理保持现有 API，但审计 detail 同步扩展 protected 统计；手动清理成功后重置自动清理退避。
- 同步 `web/src/features/audit/data/audit-actions.ts` 与三语 locale；不涉及 OpenAPI schema 变更（只更新描述与可选字段）。
- 前端容量条必须与口径同步：`storage-capacity-bar.tsx` 目前用 `totalBytes` 算分段、用 `usagePercent` 算标签，新口径下会出现「标签 93%、分段 88.7%」的矛盾；统一改用 `used + free` 作为分段/标签分母，`totalBytes` 仅作物理容量展示。
- 前端文案：`snapshots_days_desc` 从「告警事件图最大留存」改为「AI 抓拍最大留存；空间不足时提前回收未锁定抓拍，告警/锁定图除外」；手动清理弹窗说明会清理未过期录像/快照（容量压力时）。
- 验收：`make go-check` + 前端 audit/storage 单测通过；三语 locale key 一致；容量条标签与分段百分比同源。

### R8（P0）容量淘汰联动出清 DB 悬空行（图亡行灭）

- 快照单元被物理删除后，`CleanerEngine` 收集已删除单元的基名前缀 ID 列表（即抓拍记录 ID），写入 `CleanupSummary.DeletedSnapshotIDs []string`，**必须 `json:"-"`**，避免手动清理 API 回传几万个内部 ID。
- `Service.notifyAfterPrune` 将这批已删除 ID 传给 app 层的对账钩子；回调使用独立于 prune 生命周期的 bounded context（例如 `context.WithTimeout(context.WithoutCancel(pruneCtx), 30*time.Second)`），避免 5 分钟 `pruneCtx` 到期把 DB 删除一并取消。
- **失败可重试（新增持久化）**：新增迁移 `0000NN_create_pending_snapshot_deletes`（`id TEXT PRIMARY KEY, queued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`）。app 回调先 `EnqueueSnapshotDeletes(ids)` 落 pending，再尝试删除 `captures` / `recognitions`；删除成功才清 pending。失败/进程退出后，`record.RetentionPruner` 在启动和每轮 sweep 时重试 pending，直到成功。
  - 该表只存容量淘汰产生的 ID，不改变现有 TTL 巡检语义；不扫描未过期全表，避免误删「记录已建、图还没写完」的新记录。
  - 若产品坚持零迁移，则 AC6 只能覆盖成功路径，且必须在 Risks 中写明「失败/重启窗口内幽灵行会活到 TTL」；不建议。
- app 回调**不得**因 `PruneNow` 出错而跳过 `DeleteBySnapshotIDs`：两者独立执行、各自记录错误。`PruneNow` 仍负责 TTL 行的文件存在性对账，pending 负责容量淘汰的精准主键删除。
- **杜绝无图幽灵记录**：普通抓拍无图即无真实性与核验价值；物理删除必须级联清理 DB 行（成功路径即时、失败路径由 pending 重试），最终一致消除前端 404 破图与 DB 无效膨胀（删除文件与删除 DB 行之间仍有瞬时窗口）。
- 锁定告警证据从源头由 `.lock` 保护，永不进入删除列表，因此绝不误删告警表及告警快照。
- **发布门槛**：R3 / R5 / R8 必须同一版本。R3 不允许在没有 R5+R8 的构建里单独打开；理由见 Risks。
- 验收：
  - 模拟快照容量淘汰，磁盘文件删除后，DB 中对应的 capture/recognition 行被同步删除，API 不再返回已删图片对应的悬空记录。
  - 注入 `DeleteBySnapshotIDs` 失败后，pending 表保留 ID；下一轮 sweep 或进程重启后重试成功，行最终被删除。
  - `PruneNow` 失败时，`DeleteBySnapshotIDs` 仍执行。

### R9（P0）存量告警证据补锁

- R3/R5 的安全承诺依赖 `.lock` sidecar；升级前的历史告警图可能没有锁。任务必须提供一次**幂等补锁**：
  - 启动时（或独立的 pre-deploy 步骤）扫描 `alarms` 表中的非空 `image_path` / `crop_path`，文件存在且无 `.lock` 时补写；写锁失败记录 error 并计入报告。
  - 补锁必须在 storage 首次自动清理前完成；若生命周期顺序无法保证，storage 侧增加一个可选的 evidence-guard 回调（按 ID 查询 `alarms`）作为兜底。
- 验收：构造一条历史 alarm 行 + 无锁图片，启动补锁后图片带 `.lock`，`RunPrune` 容量淘汰不删除该组；补锁幂等，重复执行不产生重复文件。

---

## Decisions

| ID | 决策 | 依据 |
|---|---|---|
| D1 | 普通（未锁定、非告警）快照允许在容量压力下提前删除 | 用户 2026-11-04 确认；工业 NVR 默认行为 |
| D2 | TTL 语义 = 保留上限，不是删除下限；低于高水位时不自动巡检，不承诺到点必删 | 现场故障直接根因；避免把「上限」误读成「到期必删」 |
| D3 | 应急止点 = `free >= minFree × 1.25` **且** `usage < high`；进入清理前先检查 | 只镜像 minFree 会让 percent 触发的熔断永远无法解除；用 high 作 usage 止点同时避开 warning 连锁 |
| D4 | Phase 1 全量超期出清（不受容量止点限制）→ Phase 2 录像 FIFO → 快照 FIFO（进入前检查止点） | 避免超期快照滞留而误杀合规录像；同阶段录像单位价值最低、释放效率最高 |
| D5 | 锁定/告警证据不自动删除；本期只观测，不拒绝新锁定 | 与 Milestone Evidence Lock 一致，降低产品决策面 |
| D6 | 普通快照容量淘汰联动删除 DB 记录（图亡行灭） | 无图记录无法证明真实性且导致前端 404，快照物理删除后通过对账钩子按 ID 批量级联出清 DB 行 |
| D7 | 不新增状态枚举、不改 `StorageStatus` schema | 避免 API/前端/apidocs 三处破坏 |
| D8 | R3 / R5 / R8 同一版本发布；DB 级联使用 pending 表保证失败重试 | 防止只发容量淘汰不发级联造成 404；一次性内存投递无法跨重启 |
| D9 | 存量告警图上线前补锁；补锁失败必须显式上报 | 历史数据没有 `.lock` 时，R3 的「告警图永不删」只对新数据成立 |
| D10 | `manual` 只在 Phase 2 止点未满足时淘汰未过期媒体；健康盘不先删一批 | 避免用户点「立即清理」误删 25 个合规快照 |

---

## Out of Scope

- per-camera / per-algorithm 存储配额（海康 Quota 模式）。
- 预分配环形文件与覆盖写（需要原生引擎与索引层配合）。
- 归档、冷存储、异地备份。
- 低于高水位时的低频 TTL 巡检：本任务不改变自动清理唤醒条件，超期文件在健康盘上可能继续占用。
- root 运行时的 ext4 保留块口径：`Bavail == Bfree` 时无法用用户可用空间比例消除死区，只能靠 `EmergencyStopMinMB` + 熔断期清理兜底。
- 熔断期间「先写记录后拍图」产生空路径记录的优化（`internal/record` 域，另立任务）；现场 1086 条空路径记录不在本任务清理范围。
- 锁定证据预算的拒绝/告警阈值（本期只做跳过量观测）。
- 旧媒体根迁移与清理。

---

## Acceptance Criteria

| ID | 验收标准 | 验证方式 |
|---|---|---|
| AC1 | `used=85, free=5, total=100` 时 `UsagePercent=95`，high=90 得 `warning`；root-like 统计（`free=total-used`）单独覆盖 | `internal/storage/gate_test.go` 表驱动 |
| AC2 | emergency 下 `tick()` 启动清理；`free >= 1.25×minFree` **且** `usage < high` 后 `Evaluate` 返回非 emergency；percent 触发且 free 已高于止点时仍继续清理 | `internal/storage/service_test.go` + mock inspector |
| AC3 | 未过期普通快照在 warning/emergency 下被最旧优先删除；`.lock` 文件零删除；健康盘 `manual` 删除 0 个未过期快照；活跃组跳过 | `internal/storage/cleaner_test.go` |
| AC4 | Phase 1 全量超期先删；同一阶段内录像先于快照；日志层级顺序正确 | `cleaner_test.go` 断言删除顺序与 summary |
| AC5 | 同一前缀的主图 + crop/face 整组删除；半删不存在；组内删除失败时行保留且有 warn | `cleaner_test.go` |
| AC6 | 容量淘汰后 DB 行被同步删除；`DeleteBySnapshotIDs` 失败后 pending 保留并在下一轮/重启后重试成功；`PruneNow` 失败不阻断级联 | `internal/record/retention_test.go` + 集成测试 |
| AC7 | 自动清理写入 `storage.auto_cleanup` 审计，detail 含 trigger/deleted/freed/protected/stopReason；0 进展走 warn 且被退避限流 | `internal/storage/service_test.go` + `audit` 查询 |
| AC8 | `go test -race ./internal/storage ./internal/record` 全绿；`make go-check` 通过 | 命令 |
| AC9 | 前端 audit action 三语 locale 键一致；容量条标签与分段同源；配置/手动清理文案更新；`pnpm test` 通过 | `cd web && pnpm test` |
| AC10 | 存量 alarm 行补锁幂等；补锁后带锁组零删除 | `internal/record` 测试 + `cleaner_test.go` |
| AC11 | 发布清单确认 R3 / R5 / R8 同版本；任一项回退必须三者一起回退，不允许只上线 R3 | 代码评审 + 发布清单 |

---

## Risks

- **证据提前删除是语义变化**：配置页与文档必须说明「保留天数为上限、空间不足时可提前回收未锁定抓拍」。否则用户会认为 90 天承诺被破坏。
- **root 运行时的口径退化**：`Bavail == Bfree` 时新公式等于旧公式；现场是非 root，但 root 部署仍可能复现「高水位未到、熔断已到」。上线前确认服务运行用户；root 部署必须依赖 minFree 熔断与 emergency 清理，不能宣称口径死区已彻底消除。
- **DB 级联删除的事务与性能**：容量淘汰每批可能删除数百张快照，按 ID 批量删除 DB 行需分批（如 200 个一组），避免长时间锁定 SQLite；pending 只覆盖「已入队后」的失败/重启。`unlink → enqueue` 之间崩溃仍会丢 ID（行活到 TTL），彻底闭合需 write-ahead intent / 状态机；本任务接受该残余窗口并记录在此。enqueue 失败时 app 退化为直接主键删除，仍失败才由 TTL 兜底。
- **新增 pending 表迁移**：这是本任务唯一的持久化变更；回滚代码后表可保留（空表无害），但迁移不可跳过。若产品拒绝迁移，AC6 必须降级，且 Risks 中明确幽灵行会存活到 TTL。
- **应急清理一次删除量**：`minFree × 1.25` 在 28G 盘上约 640 MiB（1.25×2GB），可接受；若盘很小（<8G）需复核比例，并对 `minFree × 1.25 >= total` 做配置拒绝或运行时封顶。
- **活跃文件误删**：抓拍写盘耗时秒级，2 分钟宽限足够；录像分片依赖 mtime 持续更新，需在实现时确认原生切片行为。`activeFileGrace` 必须可在测试中覆盖，避免真实时钟抖动。
- **批次删除高频探针 I/O 放大**：每批删除调用 `InspectFS` 若扫描 `/proc/mounts` 会引发高频 I/O；探针在容量止点检查时必须跳过 mountinfo 解析，仅执行 `statfs`，并覆盖非 Linux fallback。
- **口径变更导致历史快照与前端不一致**：`UsagePercent` 会整体上移约 4~5 点；容量条若仍用 `totalBytes` 作分母，标签与分段会矛盾。前端必须同批修改。
- **存量告警图无锁**：升级前的 alarm 图片如果没有 `.lock`，R3 会把它们当普通快照删除；R9 补锁是上线前置条件，不能跳过。
- **组内部分删除**：主图已删、crop/face 仍存在时行会保留，但 API 的 `imagePath` 仍指向已删文件；前端必须 fallback 到 crop/face，或在实现中补「清空已删路径」的窄接口。
- **并发与退避安全**：tick 的异步清理协程完成时必须加锁更新退避与 `lastAutoCleanupAudit`；warning 与 emergency 共用退避；手动触发清理必须能打断退避并重置状态。

---

## Technical Anchors

| 文件 | 作用 |
|---|---|
| `internal/storage/model.go:45` | `percentUsed` 口径 |
| `internal/storage/gate.go:196-220` | `usage`、emergency 双条件、resume 边沿 |
| `internal/storage/service.go:271-320` | `tick()` 与自动清理触发 |
| `internal/storage/service.go:26` | `pruneRunTimeout`（回调 ctx 必须解耦） |
| `internal/storage/service_refresh.go:100` | 状态里的 `UsagePercent` |
| `internal/storage/cleaner.go:128-260` | `RunPrune`、层级顺序、`deleteCandidates`、止点 |
| `internal/storage/cleaner.go:304-360` | `collectSnapshots` / `collectRecordings` |
| `internal/storage/service_admin.go:148` | 手动清理 |
| `internal/storage/inspector.go:31-37` | `PathInspector` 接口（轻量探针需同步替身） |
| `internal/record/snapshot.go:257` | `.lock` 写入时机（写图前） |
| `internal/record/retention.go:205` | `PruneNow` 与 pending 重试入口 |
| `internal/record/store.go:476` | 批量删除 DB 记录实现 |
| `internal/app/app.go:764` | storage 与 record 对账钩子组装 |
| `internal/database/migrations/` | 新增 pending 表迁移 |
| `.trellis/spec/backend/storage-guidelines.md` §2.2 / §2.4 / §3.2 | 需同步的 storage 规范条款 |
| `.trellis/spec/backend/record-guidelines.md` | 需同步的 record 级联边界 |
| `web/src/features/audit/data/audit-actions.ts:53` | 新审计 action 同步点 |
| `web/src/features/settings/storage/components/storage-capacity-bar.tsx` | 容量条分母修正 |
| `web/src/features/settings/storage/components/manual-cleanup-dialog.tsx` | 手动清理文案 |
| `web/src/features/settings/storage/components/storage-config-form.tsx` | 保留天数文案 |

## Open Questions

无阻塞项。D8 的 pending 表与 D9 的补锁已定为推荐方案；若产品明确拒绝新增迁移或补锁，需要把 AC6/AC10 降级并在 Risks 中标注失败窗口，再开工。
