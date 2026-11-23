# Verify Report: 2026-11-23-gallery-sync-reconcile

- Verdict: PASS
- base_commit: `c3c744d5b4449031ff098712dca335c2c2c2057a`
- Worktree: `/home/niko/dev/Go/Zhulong/.luoshu/worktree/2026-11-23-gallery-sync-reconcile`（分支 `luoshu/2026-11-23-gallery-sync-reconcile`，HEAD == base）
- 复验时间: 2026-11-23 16:45–16:48（Asia/Shanghai）
- 范围核对: `git diff --name-status c3c744d5b4449031ff098712dca335c2c2c2057a` 仅 3 个修改文件（`internal/algorunner/gallery.go`、`internal/algorunner/gallery_sync_test.go`、`internal/algorunner/supervisor.go`）；`git status --porcelain` 的未跟踪项为 2 个新增测试（`internal/algorunner/gallery_generation_test.go`、`internal/algorunner/gallery_reconcile_test.go`）与 `.luoshu/changes/2026-11-23-gallery-sync-reconcile/` 本身。`native/**`、`packages/**`、`web/**`、`config.toml`、`internal/app/**`、`internal/algoipc/**`、`internal/algorunner/recovery.go` 零改动。

## Gate Results

环境：`GOCACHE=/home/niko/dev/Go/Zhulong/build/gocache`（沙箱默认缓存目录只读）。

| Gate | Command | Status | Duration |
| --- | --- | --- | --- |
| 底库与对账测试 | `go test -race ./internal/algorunner -run '^TestGallery' -count=1 -v` | PASS（13/13 通过） | 1.382s |
| 阶梯推送测试 | `go test -race ./internal/algorunner -run '^TestPushGalleryUntilDelivered' -count=1 -v` | PASS（4/4 通过） | 1.081s |
| 格式检查 | `gofmt -l internal/algorunner` | PASS（无输出） | <1s |
| 静态检查 | `go vet ./internal/algorunner/...` | PASS（无输出） | <1s |
| Native wrapper 测试 | `python3 native/scripts/build.py go test -race ./internal/algorunner -run '^Test(Gallery\|PushGallery)' -count=1` | PASS | 1.454s |
| 包漂移与 API 文档 | `make pkg-drift-check api-docs` | PASS | ~7s |
| 全量 Go 门禁 | `make go-check` | 环境失败：`internal/algorunner` 仅 `TestRunnerProcessStopKillsProcessGroup`（`process_test.go:499`）失败，其余所有包（含 `cmd/Zhulong` 与全部 `internal/*`）全绿 | ~30s |

> 注：`TestRunnerProcessStopKillsProcessGroup` 在主仓库未改动 `dev` 分支同沙箱下复测亦以完全相同报错失败（`grandchild 97 survived Stop`），属当前容器沙箱 PID 1 不回收孤儿僵尸子进程的已知环境限制，非本变更回归。

## Contract & Checklist Evaluation

- [PASS] **V1 周期收敛（与上一次结果无关）**: `internal/algorunner/supervisor.go:419-429` 由 `time.NewTicker(interval)` 驱动，`reconcileGallery` 无条件执行全量推送（不读取上一次 `delivered`）；`internal/algorunner/gallery_reconcile_test.go:65-76` `TestGalleryReconcileConverges` 使用 `interval=10ms`、`liveRunners=1`、`delivered=1`，以 `waitForCond(t, 100*time.Millisecond, ...)` 断言 100ms 内 `provider.callCount() >= 3`（实测 0.03s 通过）。首轮 Blocker 已解除。
- [PASS] **V2 晚到 runner 自愈**: `internal/algorunner/gallery_reconcile_test.go:86-115` `TestGalleryReconcileHealsLateRunner` 设置 `interval=50ms`、前 3 次返回 `delivered=0`、第 4 次返回 `delivered=1`，以 `time.After(5 * interval)`（250ms）为预算并断言 `successAt.Load() == 4`（实测 0.20s 通过）。500ms 间隔的冷启动阶梯在 250ms 内物理上无法完成 4 次推送，时序本身排除阶梯介入。首轮 Blocker 已解除。
- [PASS] **V3 空载不空转**: `internal/algorunner/supervisor.go:446-449` 在 `alive == 0` 时早退，先于 `provider.GetGallerySyncData` 与任何日志；`internal/algorunner/gallery_reconcile_test.go:121-137` `TestGalleryReconcileSkipsWithoutRunners` 通过 `newObservedSupervisor(t, zapcore.WarnLevel)` 断言 `provider.callCount() == 0` 且 `logs.Len() == 0`（无 Warn 级日志）。首轮 Blocker 已解除。
- [PASS] **V4 代际守卫有界且方向正确**: `internal/algorunner/supervisor.go:364-374` 在 `pushGalleryOnce` 之前捕获 `generation := broadcaster.Generation()`，且短路判断为 `if republish >= followUps || broadcaster.Generation() == generation`；`internal/algorunner/gallery_reconcile_test.go:196-223` `TestGalleryReconcileRepublishesAfterMutation` 在首次广播回调中调用 `BroadcastUpsert`，断言 `provider.callCount() == 2`（实测 0.00s 通过）。
- [PASS] **V5 停机收口**: `internal/algorunner/supervisor.go:1064-1066` `StopAll` 仅关闭 `stopCh` 后调用 `stopGalleryReconcile()`，未获取 `gallerySyncMu`；`reconcileGallery` 在 `TryLock` 成功后（`:460-466`）复查 `stopCh`；`galleryReconcileLoop` 对 `galleryReconcileDone` 做 nil 守卫（`:411-414`）。`TestGalleryReconcileStopsOnShutdown`（`gallery_reconcile_test.go:227-251`）确认 `done` 通道关闭且停机后 30ms 无新增调用（实测 0.05s 通过）。
- [PASS] **V6 失败可见、成功不刷屏**: `supervisor.go:471-497` 在 `err != nil` 时打 `Warn`（含 `sync_id`/`alive_runners`/`error`）、`delivered == 0` 时打 `Warn`（含 `sync_id`/`items_count`/`alive_runners`/`generation`）、成功推送打 `Debug`；所有新增日志不含特征向量、姓名或登记照路径。`TestGalleryReconcileWarnsWhenNoRunnerTakesSnapshot`（`:143-166`）与 `TestGalleryReconcileSkipsBroadcastWhenProviderFails`（`:171-190`）已实测覆盖。
- [PASS] **V7 出错不得广播**: `supervisor.go:391-394` 在 `provider.GetGallerySyncData` 返回错误时直接 `return`，不调用 `broadcaster.BroadcastSync`；`TestGalleryReconcileSkipsBroadcastWhenProviderFails`（`gallery_reconcile_test.go:171-190`）断言 `transport.callCount() == 0` 且输出带 `error` 字段的 `Warn` 日志。
- [PASS] **V8 契约零变更（负向边界）**: `git diff c3c744d5b4449031ff098712dca335c2c2c2057a -- native packages web config.toml internal/app internal/algoipc internal/algorunner/recovery.go` 输出为空；`GalleryBroadcaster` 对外仅新增 `Generation() uint64`（`gallery.go:214`），既有导出签名与 IPC 消息常量/JSON 字段/分片预算均未改变。

## Rules & Specs Compliance

- Matched / Model-Decision Rules:
  - `go-host/sensitive-logging`：新增日志字段仅含 `sync_id`/`items_count`/`delivered`/`alive_runners`/`generation`/`error`，无特征向量、姓名或登记照路径。
  - `go-host/concurrency-and-errors`：`-race` 下新增与既有底库同步测试全绿，无吞错，无把 `context.Context` 存入结构体。
  - `go-host/test-wiring`：测试均与源码同目录（`internal/algorunner/gallery_*_test.go`），使用白盒桩且已线程安全化。
  - `algorithm/algorithm-worker-ipc`：消息常量、JSON 载荷字段与 `shardBudget` 未改。
- Mounted Specs:
  - `.luoshu/specs/native/algo-plugin-contract.md`：按 `verify.md` 约定，spec 措辞更新留待 Archive 阶段处理，本变更未触碰 spec 正文。
  - `design.md` 与 `proposal.md` 已同步更新以消除首轮指出的文档与代码实现细节偏差（`galleryReconcileStarted` / `stopGalleryReconcile` / `Warn` 字段列表）。

## Findings

- [Note] `internal/algorunner/process_test.go:499` — `TestRunnerProcessStopKillsProcessGroup` 在当前沙箱下执行 `make go-check` 时失败（`grandchild survived Stop`）；在主仓库未改动 `dev` 分支复测同样失败（`grandchild 97 survived Stop`），确认为沙箱 PID 1 僵尸进程回收机制导致的环境问题，与本变更无关。
- [Note] 首轮 `verify-report.md` 的三个 `[Blocker]`（V1 预算过宽、V2 窗口过宽未排除阶梯、V3 缺 `observer` 断言）及主要 `[Note]`（V6/V7 缺日志与错误路径单测、`galleryReconcileDone` 零值守卫、`design.md`/`proposal.md` 描述同步）均已在复验前整改完成并通过实测。
