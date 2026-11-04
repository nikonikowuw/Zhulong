# 技术设计：存储熔断自愈与快照容量淘汰

## 1. 边界与影响面

- 改动主要发生在 `internal/storage`，并向 `internal/record` 补充批量主键删除与 pending 重试接口，由 `internal/app` 完成钩子装配。
- **图亡行灭（零幽灵记录）**：快照物理删除后，`storage` 通过 `notifyAfterPrune` 将已删除的快照基名 ID 列表传递给 app 层；`record` 先把 ID 落到 pending 表，再精准级联删除 `captures` / `recognitions` 行。DB 删除失败或进程重启时，pending 表保证下一轮重试，避免「一次性内存投递丢失」。
- `storage` 依然保持纯文件系统边界，不依赖 `record` 领域包（只向回调吐出 `[]string`，另加一个可选的 evidence-guard 函数类型）。
- **新增一张 pending 表迁移**：`0000NN_create_pending_snapshot_deletes`。无 `StorageStatus` schema 变更、无 `StorageHealthStatus` 枚举变更 → OpenAPI 与前端类型仍保持兼容。
- 新增审计 action `storage.auto_cleanup`，需同步前端 action 映射与三语 locale。
- **发布门槛**：R3（未过期快照淘汰）、R5（组粒度）、R8（DB 级联 + pending）必须同一版本发布；R9（存量告警补锁/guard）必须在 R3 首次生效前完成。

## 2. 空间压力判定

### 2.1 单一口径

```go
// usagePercent 返回用户可用空间的占用比，向上取整并与 df 的 Use% 对齐。
// ext4 保留块只对非特权进程不可用，用 UsedBytes/TotalBytes 会系统性低估压力。
// 向上取整避免 89.9% 截断为 89% 导致 high=90 临界水位错过 warning 唤醒。
func usagePercent(stats *FSStats) int {
    available := stats.UsedBytes + stats.FreeBytes
    if available == 0 {
        return 0
    }
    return int((stats.UsedBytes*100 + available - 1) / available)
}
```

- `FSStats.FreeBytes` 是 statfs 的 `Bavail`，`UsedBytes = Blocks - Bfree`。实现前先核对 `inspector_linux.go`，保证 `Used+Free` 与 `df` 的 `Used+Avail` 同义。
- ⚠️ **root 运行时口径会退化**：Linux 对持有 `CAP_SYS_RESOURCE` 的进程通常令 `f_bavail == f_bfree`，公式退化为 `used/total`。现场证据（free 2.04G vs total-used 3.43G）说明板端服务是非 root；根部署仍可能复现死区，必须依赖 `EmergencyStopMinMB` + 熔断期清理兜底。测试要同时覆盖「保留块」与「root-like（`free == total-used`）」两种 `FSStats`。
- 所有调用点（gate / cleaner / service_refresh）改用 `usagePercent(stats)`，`TotalBytes` 只用于展示。
- **不变量测试要改成可实现的**：不再断言「任意卷上 warning 必先于 emergency」（小卷/高 minFree 下不成立），而是断言：
  1. `usagePercent` 在 `used=85, free=5, total=100` 时为 95；
  2. 默认配置下，只要 usage 触达 `EmergencyStopPercent`，`emergencyTarget` 能通过继续清理把 usage 降到 `< HighWatermarkPercent`；
  3. `EmergencyStopMinMB × 1.25` 不得超过当前卷容量（运行时封顶）。

## 3. 清理目标与触发器

### 3.1 触发器

```go
const (
    PruneTriggerManual    PruneTrigger = "manual"
    PruneTriggerWatermark PruneTrigger = "watermark"
    PruneTriggerEmergency PruneTrigger = "emergency"
)
```

- `manual`：用户显式触发，无门槛。Phase 1 全量清超期；Phase 2 仅在止点未满足时进入。
- `watermark`：usage ≥ high，止于 low watermark（保留回差带）。
- `emergency`：gate 处于 emergency，无门槛，止点见 §3.2。

### 3.2 止点抽象与两阶段阶梯顺序

`pruneScope` 从「low watermark 百分比」改为「停止谓词」；Phase 1 不使用止点，Phase 2 使用：

```go
type pruneScope struct {
    mediaDir       string
    stopWhen       func(stats *FSStats) bool
    evidenceGuard  EvidenceGuard // 可选：历史告警证据兜底
}

func lowWatermarkTarget(low int) func(*FSStats) bool {
    return func(s *FSStats) bool { return usagePercent(s) <= low }
}

// emergencyTarget 必须同时覆盖 gate 的两个触发分支：
//   1) FreeBytes < EmergencyStopMinMB
//   2) usagePercent >= EmergencyStopPercent
// 只满足 free 条件时若 usage 仍高于 high，gate 仍可能保持 emergency，
// 因此 usage 必须继续降到 high 以下；这同时避开「刚结束 emergency 又进入 warning 清理」。
func emergencyTarget(cfg *StorageConfig, totalBytes uint64) func(*FSStats) bool {
    freeTarget := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024 * 5 / 4
    if cap := totalBytes * 9 / 10; cap > 0 && freeTarget > cap {
        freeTarget = cap // 小卷/大 minFree 时不能让止点大于总容量
    }
    return func(s *FSStats) bool {
        return s.FreeBytes >= freeTarget &&
            usagePercent(s) < cfg.HighWatermarkPercent
    }
}
```

- **进入 Phase 2 前先检查止点**：`deleteUnits` 在删除第一个 unit 前调用一次 `stopWhen`；若已满足，直接返回 `target_reached`，不先删 25 个文件。
- **Phase 1 全量超期出清，不受容量止点限制**：`exports` → `recordings` (TTL) → `snapshots` (TTL)。只有 `ctx` 取消会中断。这样「全量」与「早停」不再矛盾：早停只属于 Phase 2。
- **Phase 2 容量压力阶梯淘汰**（仅当 Phase 1 结束仍未达止点）：
  1. `recordings`（未过期录像按 mtime 升序 FIFO 淘汰）
  2. `snapshots`（未过期普通快照按 mtime 升序容量淘汰）
  - 每删除一个 unit 后检查止点；达到即退出。若候选耗尽仍未满足止点，返回 `candidates_exhausted`，由外层进入退避；emergency 下保持熔断是正确行为。
- **轻量探针避免 I/O 放大**：Phase 2 热路径只调用 `statfs` 刷新容量与块数，**严禁重复逐行扫描 `/proc/mounts`**（`findMountInfoLinux`）。新增一个平台隔离的 `capacityProbe`：
  - Linux：`unix.Statfs` + 与 `InspectFS` 相同的 `Blocks/Bfree/Bavail` 语义；
  - 非 Linux：使用 fallback 合成值，保证开发机测试可跑；
  - 不经过 `PathInspector` 主接口，避免给 `mockInspector` / `errorInspector` 强加无意义方法；若选择扩展 `PathInspector`，必须同步全部测试替身。
- **unit 级止点**：删除一个 unit 后立即 statfs；`batchSleep` 仍按 unit 数节流（默认 25 units）。
- `manual` 语义：Phase 1 全量清超期；Phase 2 在进入前检查 low watermark，健康盘上一个未过期文件都不删。**不沿用**「无门槛直接删一批」的旧语义。
- **原因可观测**：`CleanupSummary.StopReason` 取值 `target_reached` / `candidates_exhausted` / `context_canceled` / `skipped_below_high_watermark`。`deleted=0` 时必须能区分「没候选」和「已达止点」。

## 4. 快照容量淘汰

### 4.1 候选

```go
func (c *CleanerEngine) collectSnapshotUnits(
    mediaDir string,
    ttl time.Duration,
    includeUnexpired bool,
    now time.Time,
) []pruneUnit
```

- `includeUnexpired` 对 `watermark` / `emergency` 为 true；`manual` 也为 true，但 Phase 2 的进入由 §3.2 的止点检查控制（健康盘不删未过期文件）。
- 过期判定：组内所有文件 mtime 均早于 `now-ttl` 才进入「过期」队列；否则进入「容量」队列。
- 排序：过期队列在前、容量队列在后；队列内按组内最早 mtime 升序。
- 活跃保护：`now - 组内最新 mtime < activeFileGrace` 的组整组跳过。`activeFileGrace` 默认 2 分钟，通过 `CleanerOptions.ActiveFileGrace *time.Duration` 覆盖；测试不依赖真实时钟。
- **TTL 是上限不是到点必删**：低于 high 时自动清理不启动，超期文件可以继续占用；本任务不新增低频 TTL 巡检。文档和 UI 必须按这个语义写。

### 4.2 组粒度

```go
type pruneKind uint8

const (
    unitExport pruneKind = iota
    unitRecording
    unitSnapshot
)

type pruneUnit struct {
    kind      pruneKind
    id        string          // 快照：基名前缀（抓拍记录 ID）；录像/导出：仅分组标识
    files     []candidateFile // 主图 + crop + face 等实际媒体文件；不含 .lock
    size      int64
    oldest    time.Time
    protected bool
}
```

- 分组键：同目录下按基名前缀聚组。取 `dir := filepath.Dir(path)`，文件名去掉 `.lock` 后缀与扩展名后按 `_` 分割取首段 `prefix := strings.Split(base, "_")[0]`：
  - 组键为 `filepath.Join(dir, prefix)`；
  - 快照单元 `id` 记录 `prefix`；**只有 `unitSnapshot` 允许把 `id` 传播给 R8**，录像/导出永不参与 DB 级联。
- 不绑定 26 字符 ULID 格式，天然兼容测试用例（如 `rec-1.jpg`、`rec-1_face.jpg`）与各类前缀，避免格式不匹配回退单文件组导致的半删。
- 组内任一文件 `isCleanupProtected`（名字含 `_locked`、`.lock` 后缀、或存在 `.lock` sidecar）→ `protected = true`，整组不参与删除，只累计跳过统计。
- 主图写盘前先写 `.lock`（`record/snapshot.go:257`），所以清理器不会看到「图已存在但锁还没写」的窗口。
- **删除与 ID 收集**：
  - 逐文件 `os.Remove`；`os.IsNotExist` 视为已删除。
  - 只有组内**全部媒体文件都已删除或原本就不存在**时，才把 `unitSnapshot.id` 收集进 `deletedSnapshotIDs`。
  - 任一媒体文件仍存在（权限、只读、I/O 错误）→ 保留 DB 行，记 `warn`；该行还有图，不制造新的幽灵。若主图已删、仅剩 crop/face，API 仍可能返回已删的 `imagePath`，前端必须 fallback 到 crop/face；实现时优先补一个「清空已删路径」的窄接口（见 §7）。
  - 孤立 `.lock` 文件在扫描时若无对应媒体，不计入待释放媒体字节；也不自动删除（可能是正在写入的证据锁）。
- **active/protected 再检查**：扫描后到删除前存在竞态（例如运维刚补锁）。删除每个 unit 前重新 `isCleanupProtected` 一次；带锁则整组跳过。

### 4.3 保护统计

- `pruneUnit` 扫描阶段累计 `protectedFiles` / `protectedBytes`，写入 `CleanupSummary`（新增字段，JSON 向后兼容）。
- `CleanupSummary` 新增 `ProtectedFiles int` / `ProtectedBytes int64`；前端类型同步可选字段（不新增 API 版本）。
- 只有清理实际运行时才产生保护统计；没有清理就没有实时占用数据，这是 D7 的取舍。

## 5. Service 触发与退避

```go
type Service struct {
    // ... 现有字段
    backoffMu            sync.Mutex
    nextAutoPrune        time.Time // warning 与 emergency 共用
    lastAutoCleanupAudit time.Time
    autoPruneFailCount   int
}

func (s *Service) tick() {
    ctx, cancel := context.WithTimeout(context.Background(), tickTimeout)
    status, err := s.RefreshStatus(ctx)
    cancel()
    if err != nil { ... }

    switch status.Status {
    case StatusWarning, StatusEmergencyStopped:
    default:
        // 状态回落时在锁内重置退避
        s.resetAutoPruneBackoff()
        return
    }
    if s.cleaner.IsCleaning() {
        return
    }

    s.backoffMu.Lock()
    shouldSkip := time.Now().Before(s.nextAutoPrune)
    s.backoffMu.Unlock()
    if shouldSkip {
        return
    }

    // trigger := watermark | emergency
    // 异步执行清理并在协程结束安全反哺退避状态
}
```

- **warning 与 emergency 共用退避**：`DeletedFiles > 0` 且止点未满足时重置 `autoPruneFailCount`；`DeletedFiles == 0`（无候选或未达止点）或 `StopReason == candidates_exhausted` 时 `autoPruneFailCount++`，指数退避（1m → 5m → 15m，上限 15m）。否则 warning 下每 15s 扫 4 万文件并打 warn，自己把自己拖死。
- **状态回落重置**：healthy 时重置退避；`emergency → warning` 的恢复由清理结果（`DeletedFiles > 0` 且未达止点）重置。**不能在每个 warning tick 无条件重置**，否则 warning 的 0 进展退避会失效。
- **手动清理打断退避**：运维通过 API 手动执行 `ManualCleanup` 成功后，同步在锁内重置 `nextAutoPrune` 与 `autoPruneFailCount`。
- **对账回调**：`Service.notifyAfterPrune(ctx, deletedSnapshotIDs)` 将成功删除的快照单元 ID 列表向外分发；回调使用独立 bounded context，不受 `pruneRunTimeout` 影响；清理结束后执行 `RefreshStatus`，让 gate 产生 resume 边沿。
- `Stop` 语义不变：`wg` 等待进行中的 prune。

### 5.2 联动出清 DB 悬空行（图亡行灭）

为了避免快照被容量淘汰后在数据库中残留无图「幽灵记录」，采用「持久化 pending + 精准主键级联删除」：

```go
// internal/record/retention.go
func (p *RetentionPruner) EnqueueSnapshotDeletes(ctx context.Context, ids []string) (int64, error)
func (p *RetentionPruner) DeleteBySnapshotIDs(ctx context.Context, ids []string) (int64, error)
func (p *RetentionPruner) ProcessPendingSnapshotDeletes(ctx context.Context, limit int) (int64, error)
```

1. **pending 表（新增迁移 `0000NN_create_pending_snapshot_deletes`）**：
   ```sql
   CREATE TABLE IF NOT EXISTS pending_snapshot_deletes (
       id TEXT PRIMARY KEY NOT NULL,
       queued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
   );
   ```
   - 只存容量淘汰/物理删除产生的 ID，不改变现有 TTL 巡检语义。
   - 不扫描未过期全表，避免误删「记录已建、图还没写完」的新记录。
   - `Pruner.Start` 与每轮 `sweep` 都调用 `ProcessPendingSnapshotDeletes`；进程重启后 pending 自动续跑。
2. **分批事务删除**：
   - `ids` 按 200 个一组切分；
   - 在 `captures` 与 `recognitions` 表中执行 `DELETE FROM <table> WHERE id IN (?)`；
   - 告警记录存放在 `alarms` 表且图片受 `.lock`/guard 保护，绝对不进入 `ids`，从根本上隔离高价值证据。
   - 删除成功后才清 pending；重复处理幂等。
3. **装配层组装（`internal/app/app.go`）**：
   ```go
   p.Storage.SetAfterPrune(func(ctx context.Context, prunedUnitIDs []string) {
       reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
       defer cancel()

       // 1. 先落 pending，保证进程崩溃/ctx 取消后仍能重试
       if len(prunedUnitIDs) > 0 {
           if _, err := comp.Pruner.EnqueueSnapshotDeletes(reconcileCtx, prunedUnitIDs); err != nil {
               p.Logger.Warn("enqueue pending snapshot deletes failed", zap.Error(err))
           }
       }
       // 2. 原有超期 TTL 对账；失败不阻断第 3 步
       if _, err := comp.Pruner.PruneNow(reconcileCtx); err != nil {
           p.Logger.Warn("reconcile perception records after storage cleanup failed", zap.Error(err))
       }
       // 3. 消费 pending（含本轮与历史残留）
       if _, err := comp.Pruner.ProcessPendingSnapshotDeletes(reconcileCtx, 0); err != nil {
           p.Logger.Warn("process pending snapshot deletes failed", zap.Error(err))
       }
   })
   ```
4. **性能与一致性**：
   - 主键 `IN` 查询只走索引，无磁盘 I/O 放大；
   - 成功路径即时删除，失败路径 pending 重试；组内部分删除失败时保留行，不误删还有图的记录；
   - `PruneNow` 与 `ProcessPendingSnapshotDeletes` 独立执行，前者失败不再阻断后者。

### 5.3 存量告警证据兜底

- **补锁**：`record` 提供幂等的 `EnsureAlarmEvidenceLocks(ctx)`，扫描 `alarms` 表中的非空 `image_path` / `crop_path`，文件存在且无 `.lock` 时补写。启动时执行一次；写锁失败记录 error 并上报，不静默。
- **guard 兜底**：即使补锁尚未完成（或历史数据被人工移动），storage 在删除快照 unit 前调用可选 `EvidenceGuard`：
  ```go
  type EvidenceGuard func(ctx context.Context, ids []string) (map[string]bool, error)
  ```
  - app 注入：`record.Store.ExistingAlarmIDs(ctx, ids)` 查 `alarms` 主键；
  - guard 返回 true 的 unit 整组跳过并计入 protected；
  - guard 出错时**失败关闭**：跳过删除并记 error，绝不因为查询失败误删证据；
  - 按 batch 调用，避免 4 万次单 ID 查询。
- 两者关系：补锁修复历史数据，guard 处理竞态/漏网；都不是「拒绝新锁定」的预算策略。

## 6. 可观测性

- **审计**：成功路径写 `storage.auto_cleanup`（system、空 IP），detail 固定格式：
  `trigger=<t>, deleted=<n>, freed=<bytes>, protectedFiles=<n>, protectedBytes=<n>, target=<status>, stopReason=<r>, durationMs=<n>`。
  - 节流：`deleted > 0` 且距上次 ≥ 5m 才写；`deleted == 0` 且距上次 ≥ 1h 才写。否则一次 4 万文件的清理会连续写几十条审计。
  - `lastAutoCleanupAudit` 的读取与更新受 `backoffMu` 保护，防止多协程 race。
- **日志**：
  - `finished storage cleanup cycle` 增加 `trigger` / `protected_files` / `protected_bytes` / `target_status` / `stop_reason`。
  - 0 进展时 `warn`，由 warning/emergency 共用退避限流。
- **前端**：
  - `audit-actions.ts` 增加 `storage.auto_cleanup`；三语 locale 增加文案。
  - `storage-capacity-bar.tsx` 的分段与标签统一用 `used + free` 作分母；`totalBytes` 仅作物理容量展示，避免「标签 93%、分段 88.7%」。
  - `snapshots_days_desc` 改为「AI 抓拍最大留存；空间不足时提前回收未锁定抓拍，告警/锁定图除外」；手动清理弹窗说明会清理未过期录像/快照（容量压力时）。
  - `handler.go` 的手动清理 swagger 描述同步为 exports/recordings/snapshots。
- **CleanupSummary**：新增 `StopReason`、`ProtectedFiles`、`ProtectedBytes`；`DeletedSnapshotIDs []string` 必须 `json:"-"`。

## 7. 兼容性与回滚

- **持久化变更**：新增 pending 表迁移；回滚代码后表可保留（空表无害），但迁移不可跳过。若产品拒绝迁移，AC6 必须降级，并在 Risks 中写明「失败/重启窗口内幽灵行会活到 TTL」。
- 配置语义变化：`snapshotsRetentionDays` = 上限。配置页描述需要增加「空间不足时会提前回收未锁定抓拍」。
- **零 404 的边界**：容量淘汰成功路径即时删除 DB 行；失败路径由 pending 重试。组内部分删除失败时保留行（至少还有一张图），前端需 fallback 到 crop/face；若实现「清空已删路径」窄接口，则主图 404 也能消除。这里的「零 404」是**最终一致**，不是文件删除与 DB 删除之间的瞬时无窗口。
- **仍未闭合的崩溃窗口**：cleaner `unlink` 返回后、app 把 ID 写入 pending 前进程崩溃/断电，ID 会丢失，对应行保留到 TTL 巡检（默认 90 天）。彻底闭合需要 write-ahead intent（删除前写 `intent`，删除后标 `ready`，恢复时按文件存在性判定），本任务不引入；当前 pending 只保证「已入队后」的失败/重启可重试。
- **enqueue 失败兜底**：app 先 enqueue；失败时记 error 并退化为直接按主键删除，仍失败才由 TTL 巡检兜底。这避免了「SQLite 短暂不可用」直接丢 ID。
- 回滚：代码回退即可；已删除的媒体与记录不可恢复（清理不可逆），因此灰度顺序是：
  1. 先完成 R9 补锁/guard；
  2. 发布 R1/R7（口径 + 观测）；
  3. 同版本发布 R3/R5/R8；
  4. 观察自动清理审计与 pending 表，再公开 UI 语义。

## 8. 权衡

| 选择 | 备选 | 取舍 |
|---|---|---|
| 应急止点 = `free >= 1.25×minFree` **且** `usage < high` | 只镜像 minFree | 能让 percent 触发的熔断自行解除，并避开 warning 连锁；代价是大卷上可能多删到 high 以下（仍远少于删到 low） |
| Phase 1 全量超期、不受止点限制 | 每批都检查止点 | 「TTL 上限」语义一致，超期负债一次出清；代价是 Phase 1 可能耗时较长，需要 ctx 取消 |
| 进入 Phase 2 前先检查止点 | 先删一批再检查 | 健康盘手动清理不会误删 25 个未过期快照；代价是多一次 statfs |
| 同目录基名前缀聚组 | 强校验 26 字符 ULID | 通用稳健，兼容测试与非标命名；代价是基名带下划线的前缀必须规范，且录像/导出不得参与 DB 级联 |
| 向上取整计算百分比 | 整数向零截断 | 杜绝 89.9% 错过 90% 水位；代价是水位触碰会比截断稍灵敏约 0.1% |
| pending 表 + 主键级联 | 一次性内存投递 / 未过期全表扫描 | 失败可重试、重启可续跑、不误删新记录；代价是新增一张表和一次迁移 |
| 批次检查轻量 `statfs` | 每次调用完整 `InspectFS` 扫 mounts | 消除边缘节点系统调用风暴；代价是新增平台隔离的探针实现 |
| 不加状态枚举 | 新增 `blocked_no_candidates` | 避免 API/前端/apidocs 破坏；空转通过审计、`stopReason` 与日志表达 |
