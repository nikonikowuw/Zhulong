# 审计日志去掉 NTP 周期巡检噪声

## Goal

把 `time.ntp_sync` 从「后台心跳」还原为「真实的时钟安全事件」：

1. **稳态巡检不再进审计**：`workerLoop` 每 `sync_interval_seconds`（默认 900s）成功探针只是
   `adjtimex` 微调，不改变墙钟语义、无人操作、无溯源价值 —— 改为 `logger.Debug` 运行日志。
2. **只记录状态跃迁与真正的时钟改动**：墙钟被 Step、panic 阈值复核、失败/恢复翻转。
3. **修掉 panic_review 被记成 success 的误导**：偏移 ≥ 10 分钟进入复核时，审计必须是 failed。
4. **清理历史噪声**：迁移删除存量 `username='system'` 的 `time.ntp_sync` 心跳行。

## 背景

`internal/systemtime/service.go` 的 `probeOnce` 在**每一轮**探针成功后无条件调用
`recorder.RecordSystem(ActionTimeNTPSync, ...)`，而 `workerLoop` 是按
`cfg.SyncIntervalSeconds` 定时调用它。后果：

| 问题 | 量化 |
| --- | --- |
| 审计表被心跳滚穿 | 96 条/天 对 `DefaultMaxEntries=5000` → **约 52 天清空整表** |
| 真操作记录被挤掉 | `Prune` 按 `created_at ASC` 删最旧，`camera.create` / `auth.login` / `storage.emergency_stop` 在两个月后被心跳挤走 |
| 高危状态被记成成功 | `ErrPanicReview` 分支跳过 error 记录后落到成功分支，`opErr=nil` |
| 同一 action 两种归因 | 用户手动 `POST /sync`（有 IP）与后台探针（空 IP）共用 `time.ntp_sync`，无法区分 |

`SyncStatus`（`lastSyncTime` / `offsetMs` / `rttMs`）本来就通过 `GET /api/v1/system/time` 实时暴露，
前端 `time-status-banner.tsx` 已在消费 —— 审计里再存一份是同事实双写。

## Requirements

### R1 探针结果分类（statemachine）

`TimeStateMachine.ProcessNTPResult` 从 `error` 改为 `(SyncOutcome, error)`：

```go
type ActionKind int
const (
    ActionNone ActionKind = iota // 稳态 slew，无审计价值
    ActionStepped                // 执行 SetRealtime，墙钟跳变
    ActionPanicReview            // 进入/继续高危复核
)
type SyncOutcome struct {
    Kind   ActionKind
    Step   time.Duration // 仅 ActionStepped 有效
    Review int           // 复核进度 n/PanicRequiredChecks
}
```

- 冷启动 step、moderate step、panic 三次确认后的 step → `ActionStepped`
- 稳态 slew（`|offset| < SlewThreshold`）→ `ActionNone`
- 前两次 panic → `ActionPanicReview` + `ErrPanicReview`
- 判定逻辑与阈值**不得改变**，只增加返回值。

### R2 审计落点（service）

| 场景 | 审计 | action |
| --- | --- | --- |
| 稳态 slew | ❌ | — |
| 墙钟 Step | ✅ success | `time.clock_step` |
| panic 复核 | ✅ **failed** | `time.clock_step` |
| 探针失败（**失败态首次翻转**） | ✅ failed | `time.ntp_sync_failed` |
| 探针失败（失败态内重试退避） | ❌ | — |
| 由失败恢复成功 | ✅ success | `time.ntp_recovered` |
| 冷启动 RTC 自愈 | ✅ 保留 | `time.rtc_heal` |
| 用户 `POST /sync` | ✅ 保留（中间件收口） | `time.ntp_sync` |

失败态下的 `time.ntp_sync_failed` 只允许在 `false→true` 翻转时写一次；
`time.ntp_recovered` 只在 `true→false` 翻转时写一次。用于网络长时间断连时
退避重试（30s → 60s → … → interval）不把表刷满。

### R3 运行日志

稳态巡检一律 `logger.Debug("NTP probe ok", server/offset/rtt/stratum)`；
失败与 Step 仍保留 `logger.Warn/Info`。Debug 级别默认不输出，现场用 `kill -HUP` 调级即可。

### R4 历史数据清理（迁移 000017）

新增 `000017_purge_ntp_heartbeat_audit.up.sql` / `.down.sql`：

```sql
DELETE FROM audit_logs WHERE action = 'time.ntp_sync' AND username = 'system';
```

- 只删 `username='system'`：用户手动 `POST /sync` 的记录是 `username='anonymous'` 或有真实 IP，
  属于人工操作证据，必须保留。
- `down.sql` **不可恢复**已删数据，只能写成 no-op 并在文件内注明；这是数据清理类迁移的固有性质。

### R5 前端

- `AuditTable.tsx` 的 `actionOptions` 增加 `time.clock_step` / `time.recovered` / `time.ntp_sync_failed`
- 三份 locale（`zh-Hans` / `zh-Hant` / `en`）补 `actionNames.*` 文案
- `AuditTable.test.tsx` 现有 `time.ntp_sync` 断言保持有效（该 action 仍存在，仅归因变了）

## Non-Goals

- 不改 `SyncStatus` DTO、不改 NTP 协议实现、不改 `workerLoop` 的定时/退避节奏。
- 不改动用户手动 `POST /api/v1/system/time/sync` 的审计语义（仍由 `audithttp` 中间件收口）。
- 不改 `Prune` 的 `maxEntries` 或 `audit.clear` 豁免规则。

## Acceptance Criteria

1. `TestProbeOnceSteadyStateSlewRecordsNoAudit`：mocked SNTP 返回 10ms offset，
   手动触发一轮探针 → 审计表 **0 条**，`logger` 有一条 Debug 级 `NTP probe ok`。
2. `TestProbeOnceStepRecordsClockStep`：offset=5s → 审计 1 条 `time.clock_step` / success，
   detail 含 `step=` 与 `server=`。
3. `TestProbeOncePanicReviewRecordsFailure`：offset=15min 首轮 → 审计 1 条 `time.clock_step` / **failed**，
   `error_msg` 非空且不复述原始内核错误。
4. `TestProbeOnceFailureTransitionRecordedOnce`：连续 3 轮探针失败 → 审计 **1 条**
   `time.ntp_sync_failed`；随后成功一轮 → 追加 1 条 `time.ntp_recovered`。
5. `TestStateMachine*` 六个既有用例改用双返回值，`Kind` 断言与当前 step/slew 行为一致。
6. `TestMigration000017_PurgeNTPHeartbeat`：迁移前插入 system/anonymous 两类
   `time.ntp_sync` 行，迁移后只剩 anonymous 行与其它 action。
7. `make go-check` 与 `cd web && pnpm test -- src/features/audit` 全绿。
8. 手工验收：dev 环境跑 15 分钟以上，审计页不再出现新的 `time.ntp_sync` 心跳行。

## Risks

- **审计动作语义变更**：如果现场有人按 `time.ntp_sync` 建了告警规则，清理历史后该 action
  只会来自人工操作。需要在变更说明里写清。
- **`down.sql` 不可逆**：数据清理类迁移的固有代价，文件内注明。

## 决策记录

- **清掉而非归档**（用户 2026-10-21 决定）：心跳行没有溯源价值，迁移成 failed 只会继续占用
  5000 条额度；保留它们的唯一效果是让新表从被扣的额度开始。
