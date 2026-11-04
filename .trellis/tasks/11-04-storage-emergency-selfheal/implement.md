# 实施计划：存储熔断自愈与快照容量淘汰

## 分批

| 批次 | 范围 | 提交策略 |
|---|---|---|
| A（P0，必须同版发布） | R1 口径 + R2 应急清理 + R3 快照容量淘汰 + R4 顺序 + R5 组粒度 + R7 观测 + R8 联动出清 + R9 存量告警兜底 | A1–A10 顺序提交；**A 整体不可拆开发布**，R3 不允许在没有 R5+R8 的构建里单独打开 |
| B（P1，可后置） | R6 前端展示/预算分析 + 部分删除路径清理 | 依赖 A；可单独 revert |

> 发布门槛：R3 / R5 / R8 必须同一版本；R9 补锁/guard 必须在 R3 首次生效前完成。A 内部可以分提交评审，但不能分版本发。

---

## Batch A（P0）

### A1. 统一口径

- [ ] 改 `internal/storage/model.go`：`percentUsed(used, total)` → `usagePercent(stats *FSStats)`，分母为 `used + free`，采用向上取整算法 `(used*100 + available - 1) / available`。
- [ ] 更新调用点：`gate.go:196`、`cleaner.go:147`、`cleaner.go:217`、`service_refresh.go:100`。
- [ ] 核对 `inspector_linux.go`：确认 `FreeBytes` 取 `Bavail`、`UsedBytes` 取 `Blocks-Bfree`，与 `df` 同义。
- [ ] 根因测试：`used=85, free=5, total=100` → `UsagePercent=95`；同时补 root-like `free == total-used` 用例，证明该场景公式退化但 emergency 清理仍能兜底。
- [ ] 更新 `gate_test.go` / `cleaner_test.go` / `service_test.go` 中所有百分比期望值。
- [ ] 不变量测试改为断言：`usagePercent` 边界、`EmergencyStopPercent` 触发后 `emergencyTarget` 能把 usage 降到 `< high`、`minFree×1.25` 运行时封顶。

### A2. 止点抽象与两阶段阶梯顺序

- [ ] `cleaner.go`：`pruneScope` 增加 `stopWhen func(*FSStats) bool`，删除 `isAboveLowWatermark`。
- [ ] 新增 `lowWatermarkTarget(low)` 与 `emergencyTarget(cfg, totalBytes)`；后者必须同时判断 `free >= 1.25×minFree` **且** `usagePercent < HighWatermarkPercent`，并按卷容量封顶 free target。
- [ ] 进入 Phase 2 前先检查止点；删除第一个 unit 前不删任何文件。
- [ ] **Phase 1 全量超期出清，不受容量止点限制**：exports (TTL) → recordings (TTL) → snapshots (TTL)；只有 ctx 取消会中断。
- [ ] **Phase 2 容量压力阶梯**：recordings (未过期 FIFO) → snapshots (未过期容量淘汰)；每删一个 unit 后检查止点，达到即退出。
- [ ] `RunPrune` 增加 `PruneTriggerEmergency`；门槛逻辑：`manual` / `emergency` 无门槛，`watermark` 要求 `usage >= high`。
- [ ] 新增平台隔离的 `capacityProbe`：Linux 仅 `unix.Statfs` 刷新 `Blocks/Bfree/Bavail`，非 Linux 使用 fallback 合成值；**不扫 `/proc/mounts`**。若选择扩展 `PathInspector`，同步 `mockInspector` / `errorInspector` / `mockRootDevErrorInspector`。
- [ ] `CleanupSummary.StopReason`：`target_reached` / `candidates_exhausted` / `context_canceled` / `skipped_below_high_watermark`。
- [ ] 修正 `cleaner.go:173` 注释，引用 §2.2，说明「先全量超期出清；同阶段录像先删是因为单位证据价值最低、释放效率最高」。
- [ ] 测试：percent 触发且 free 已高于 1.25×minFree 时仍继续清理；Phase 1 不因低于 low 而提前停；Phase 2 进入前止点已满足时删除 0。

### A3. 快照容量候选

- [ ] `collectSnapshots` 增加 `includeUnexpired bool` + `now time.Time` 参数；返回「TTL 过期在前、容量候选在后」的列表。
- [ ] 容量候选按 mtime 升序；活跃保护改为 `now - 组内最新 mtime < activeFileGrace`（不是 `mtime < 2m`）。
- [ ] `activeFileGrace` 通过 `CleanerOptions` 可覆盖，默认 2 分钟。
- [ ] 保持 `.lock` / `_locked` / sidecar 保护逻辑不变。
- [ ] `watermark`、`emergency`、`manual` 三种触发都启用容量候选；manual 的 Phase 2 由止点检查控制，健康盘删除 0 个未过期快照。
- [ ] 测试：90 天 TTL 内的快照，`RunPrune(watermark)` 在 low watermark 前停止；`.lock` 文件字节数不变；健康盘 manual 不删未过期；活跃边界可测。

### A4. 组粒度删除

- [ ] 新增 `pruneKind`（export / recording / snapshot）与 `pruneUnit{kind, id, files, size, oldest, protected}`；分组规则采用同目录下基名前缀 `{dir}/{prefix}`（`strings.Split(base, "_")[0]`）。
- [ ] `collectSnapshots` / `collectRecordings` 返回 `[]pruneUnit`；`deleteCandidates` → `deleteUnits`。
- [ ] 只有 `unitSnapshot` 允许把 `id` 传播给 R8；录像/导出永不参与 DB 级联。
- [ ] 组内任一 `.lock` / `_locked` → 整组跳过并累计保护量；删除每个 unit 前再检查一次保护标记（扫描后到删除前的竞态）。
- [ ] TTL 判定用组内最新 mtime；容量排序用组内最早 mtime。
- [ ] 删除时逐文件 `os.Remove`，`os.IsNotExist` 视为成功；只有全部媒体文件已删除或不存在才收集 ID；任一文件仍存在则保留行并记 warn。
- [ ] 测试：主图 + crop/face 整组删除；半删不存在；带锁组零删除；非 ULID 命名的前缀组整组删除；组内一个文件删除失败时行保留且有 warn。

### A5. 联动出清 DB 悬空行（图亡行灭）

- [ ] `cleaner.go`：快照单元删除成功后，将其前缀 ID 收集进 `CleanupSummary.DeletedSnapshotIDs`；字段必须 `json:"-"`，避免 API 回传几万个 ID。
- [ ] `service.go`：扩展 `notifyAfterPrune(ctx, deletedSnapshotIDs []string)`；`SetAfterPrune` 同步改签名。
- [ ] 回调使用独立 bounded context（`context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)`），不受 `pruneRunTimeout` 取消影响。
- [ ] 新增迁移 `0000NN_create_pending_snapshot_deletes`（`id TEXT PRIMARY KEY`, `queued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`）。
- [ ] `record/store.go`：新增 `deleteRecordsByIDs(ctx, ids []string) (int64, error)`，在事务内批量删除 `captures` 与 `recognitions` 中的对应 ID 行；`pending` 的写入/读取/清除方法。
- [ ] `record/retention.go`：新增 `EnqueueSnapshotDeletes`、`DeleteBySnapshotIDs`、`ProcessPendingSnapshotDeletes`；按 200 个一组分批；删除成功后才清 pending；`Start` 与每轮 `sweep` 都重试 pending。
- [ ] `app/app.go`：`SetAfterPrune` 回调先 enqueue，再 `PruneNow`，再 process pending；`PruneNow` 失败不阻断级联。
- [ ] 测试：容量淘汰后 DB 行被同步删除；`DeleteBySnapshotIDs` 失败后 pending 保留，下一轮/重启重试成功；`PruneNow` 失败时级联仍执行；告警记录与告警图片不受影响。

### A6. Service 触发与退避

- [ ] `service.go:tick`：接受 `StatusWarning` 与 `StatusEmergencyStopped`；`CleanerEngine.IsCleaning()` 仍作为防重入。
- [ ] emergency 下使用 `PruneTriggerEmergency`；warning 使用 `PruneTriggerWatermark`。
- [ ] `backoffMu` 保护 `nextAutoPrune`、`autoPruneFailCount`、`lastAutoCleanupAudit`；**warning 与 emergency 共用退避**。
- [ ] 异步协程结束安全反哺退避状态：无进展/`candidates_exhausted` 退避（1m → 5m → 15m，上限 15m）；有进展且未达止点重置；healthy 时重置。**warning tick 不得无条件重置退避**，否则 0 进展会退化回每 15s 全盘扫描。
- [ ] 手动清理 `ManualCleanup`：执行成功后加锁重置退避计时器，支持人工打断退避抑制。
- [ ] `Stop` / `wg` 契约不变。
- [ ] 测试：mock inspector 模拟 free 跌破 minFree，tick 后验证候选被删、`Evaluate` 恢复、resume 审计产生；percent 触发且 free 已高于止点时仍继续；warning 0 进展被退避限流。

### A7. 观测

- [ ] `storage/model.go`：新增 `ActionStorageAutoCleanup = "storage.auto_cleanup"`。
- [ ] `CleanupSummary` 增加 `ProtectedFiles` / `ProtectedBytes`（JSON 向后兼容）；扫描阶段累计，审计/日志带上。
- [ ] `Service` 在自动清理后写审计（system、空 IP）：`deleted > 0` 且距上次 ≥ 5m，或 `deleted == 0` 且距上次 ≥ 1h；detail 含 trigger/deleted/freed/protected/target/stopReason/duration。
- [ ] `finished storage cleanup cycle` 日志增加 trigger / protected / target_status / stop_reason；0 进展走 warn。
- [ ] 前端：`audit-actions.ts` 增加 action；三语 locale 增加文案。
- [ ] 前端容量条：`storage-capacity-bar.tsx` 分段与标签统一用 `used + free` 作分母，`totalBytes` 仅作物理容量展示；更新测试夹具。
- [ ] 前端文案：`snapshots_days_desc` 增加「空间不足时提前回收未锁定抓拍」；`manual-cleanup-dialog.tsx` 说明会清理未过期录像/快照（容量压力时）；`storage-config-form.tsx` 同步。
- [ ] `storage/handler.go` 手动清理 swagger 描述更新为 exports/recordings/snapshots。
- [ ] 测试：`make go-check` + 前端 audit/storage 单测；三语 locale key 一致；容量条标签与分段同源。

### A8. 存量告警证据兜底

> R9 与 R3 同为上线前置，因此放进 A 批次；它不再标为可后置。

- [ ] `record` 新增 `EnsureAlarmEvidenceLocks(ctx)`：扫描 `alarms` 的 `image_path` / `crop_path`，存在且无 `.lock` 时补写；幂等；失败记 error 并上报。
- [ ] 启动顺序：确保补锁在 storage 首次自动清理前执行；若生命周期顺序无法保证，storage 侧加 `EvidenceGuard`。
- [ ] `EvidenceGuard`：`storage.Service.SetEvidenceGuard` + `record.Store.ExistingAlarmIDs(ctx, ids)`；快照 unit 删除前按 batch 查询，命中则整组跳过；guard 出错时失败关闭（跳过删除并记 error）。
- [ ] 测试：历史 alarm 行 + 无锁图片，补锁后带 `.lock`；guard 命中时整组零删除；补锁幂等。

### A9. 规范同步

- [ ] `storage-guidelines.md` §2.2 增加快照容量淘汰条目与两阶段删除顺序；明确 Phase 1 全量、Phase 2 止点。
- [ ] §3.1 补充统一口径公式、root 运行时的退化说明。
- [ ] §3.2 明确「熔断期间仍执行清理，止点 = free ≥ 1.25×minFree 且 usage < high」，并写明 TTL 是上限。
- [ ] §2.4 更新 DB 行边界：storage 不依赖 record，但新增 pending 表与按 ID 级联入口。
- [ ] `record-guidelines.md` §保留清理补充 `DeleteBySnapshotIDs` / pending 重试契约，以及「容量淘汰是 TTL 巡检的唯一例外」。
- [ ] 更新 `README` / 配置页说明：保留天数是上限。

### A10. 发布门槛与灰度

- [ ] 确认服务运行用户；root 部署必须在发布说明中标注「口径可能退化，依赖 minFree 熔断 + emergency 清理」。
- [ ] 确认 `minFree × 1.25 < total`；不满足时拒绝配置并给出稳定错误码。
- [ ] 灰度顺序：R9 补锁/guard → R1/R7 口径与观测 → R3/R5/R8 同版本 → 观察 pending 与 audit → 公开 UI 语义。
- [ ] 发布检查：A 整体 revert 时 R3/R5/R8 必须一起回退，不能只回退 R8。

---

## Batch B（P1，可后置）

### B1. 保护统计前端展示

- [ ] `CleanupSummary` 的 `ProtectedFiles` / `ProtectedBytes` 已在 A7 落地；B1 只补前端 `types.ts` 可选字段与展示（不强制）。
- [ ] 若产品需要「锁定占比 > 30%」的预算分析，另立阈值与拒绝策略。

### B2. 部分删除后的路径清理 / 前端兜底

- [ ] 确认前端在 `imagePath` 缺失时是否 fallback 到 `cropPath` / `faceImagePath`；没有则补 fallback。
- [ ] 评估新增 `record.ClearDeletedSnapshotPaths(ctx, ids, paths)` 窄接口：主图已删、crop/face 仍在时清空已删路径，避免 API 返回 404 主图。
- [ ] 测试：主图删除失败/成功混合场景，API 不返回不可用主图。

---

## 验证命令

```bash
# 包级（record 间接依赖 native engine，必须走 wrapper；storage 可直接跑）
go test -race ./internal/storage
python3 native/scripts/build.py go test -race ./internal/record

# 仓级
make go-check

# 前端 audit / storage
cd web && pnpm test -- src/features/audit src/features/settings/storage

# 收工前
make check
```

板端验证（A 合并后，使用实际媒体根）：

```bash
MEDIA_DIR=/opt/zhulong/data/media   # 按实际 [storage] mediaDirectory 替换
df -B1 "$MEDIA_DIR"
python3 - <<'EOF'
import sqlite3
c = sqlite3.connect("file:/opt/zhulong/data/zhulong.db?mode=ro", uri=True)
for r in c.execute(
    "select created_at, action, detail from audit_logs "
    "where action like 'storage.%' order by id desc limit 10"
):
    print(r)
print("pending:", c.execute("select count(*) from pending_snapshot_deletes").fetchone()[0])
EOF
```

通过标准：

- `storage.emergency_stop` 后的首个 tick 出现 `storage.auto_cleanup`（deleted > 0 或 stopReason 明确）；不是固定 15s，清理本身耗时。
- `free >= 1.25×minFree` **且** usage < high 后出现 `storage.emergency_resume`。
- `snapshots/` 最早日期目录开始减少，带 `.lock` 的文件保持不变；历史 alarm 图有 `.lock`。
- `pending_snapshot_deletes` 最终归零；失败重启后仍能继续消费。
- `df` 与 `usagePercent` 的偏差收敛到 1 个点以内（非 root 部署）。

---

## 风险文件与回滚点

| 文件 | 风险 | 回滚 |
|---|---|---|
| `internal/storage/cleaner.go` | 删除逻辑核心，误删与 I/O 放大风险最高 | A 整体 revert |
| `internal/storage/service.go` | tick 并发与退避状态竞争（需加锁保护） | A 整体 revert |
| `internal/storage/gate.go` | 口径变更影响所有状态测试 | A 整体 revert |
| `internal/record/retention.go` | DB 行批量删除事务与 pending 重试 | A 整体 revert |
| `internal/record/store.go` | 主键批量删除 SQL 效率与安全 | A 整体 revert |
| `internal/database/migrations/0000NN_create_pending_snapshot_deletes.*.sql` | 持久化变更不可跳过；回滚代码后表保留 | 保留空表，不删迁移 |
| `internal/app/app.go` | 回调 ctx/错误处理顺序 | A 整体 revert |
| `web/.../storage-capacity-bar.tsx` | 分母改错会导致 UI 百分比反向漂移 | 前端单独 revert |
| `web/.../audit-actions.ts` | 新 action 未映射会显示原始键 | 前端单独 revert |
| `internal/record/service.go`（补锁） | 历史 alarm 图漏锁会误删证据 | A 整体 revert |

删除不可逆：上板前先在测试目录用合成文件验证一遍 `RunPrune`，确认「Phase 1 全量、Phase 2 止点、锁/guard 保护、DB 级联 + pending 重试」四项都符合预期。

---

## 完成定义

- AC1–AC11 全部有对应测试或命令证据；
- storage / record 规范更新完成；
- 板端实测出现 `auto_cleanup` 与 `emergency_resume`，pending 归零，无图记录被同步出清；
- 历史 alarm 补锁/guard 生效；
- `make check` 通过。
