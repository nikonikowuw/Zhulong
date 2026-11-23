# Tasks: 2026-11-23-gallery-sync-reconcile

> 自底向上的依赖拓扑，无前向依赖。所有命令在 worktree 根目录执行；
> `internal/algorunner` 不依赖 `internal/engine`，因此可直接用裸 `go test`（AGENTS.md 允许）。

## 1. 实施任务

- [x] 1.1 (deps: none) `GalleryBroadcaster` 增加内容代数与访问器，并在两处运行期增量上自增 —
  `internal/algorunner/gallery.go`（`generation atomic.Uint64` 字段、`Generation() uint64`、
  `BroadcastUpsert`/`BroadcastDelete` 在 marshal 前 `b.generation.Add(1)`；`BroadcastSync` 不推进），
  测试 `internal/algorunner/gallery_generation_test.go`（起 0；Upsert 与 Delete 各推进一次；
  `BroadcastSync` 不推进；`broadcaster == nil` 的早退不推进）
  （check: `go test -race ./internal/algorunner -run '^TestGalleryBroadcasterGeneration' -count=1`）

- [x] 1.2 (deps: 1.1) 抽出共享的快照推送路径 `pushGallerySnapshot(provider, broadcaster, followUps)`（代际守卫，
  见 design.md §2），并把 `pushGalleryUntilDelivered` 的每次 attempt 切换到它 —
  `internal/algorunner/supervisor.go`（新增函数 + `gallerySnapshotFollowUps = 1` 常量；
  阶梯的换 `sync_id`、`delivered>0` 即成功、预算耗尽记 Error 的语义保持不变）
  （check: `go test -race ./internal/algorunner -run '^TestPushGalleryUntilDelivered' -count=1`）

- [x] 1.3 (deps: 1.2) 新增周期对账回路并接上装配点 —
  `internal/algorunner/supervisor.go`（`galleryReconcileInterval = 30 * time.Second`、
  `galleryReconcileStarted bool` + `galleryReconcileDone chan struct{}` 字段、`galleryReconcileLoop(interval, liveRunners)`、
  `reconcileGallery(liveRunners)`、`countLiveRunners()`、`stopGalleryReconcile()`；`SetGallerySync` 解锁后启动回路；
  `StopAll` 在 `close(stopCh)` 之后 join 回路，保证协程收敛）
  （check: `go build ./internal/algorunner && go vet ./internal/algorunner`）

- [x] 1.4 (deps: 1.3) 线程安全化既有测试替身，供后台回路使用 —
  `internal/algorunner/gallery_sync_test.go`（`gallerySyncFakeBroadcaster` 加 `sync.Mutex` 与
  `records()`/`callCount()` 访问器；`gallerySyncFakeProvider` 的 `calls` 改 `atomic.Int64` 并提供 `callCount()`；
  现有断言改为走访问器，不改变被测语义）
  （check: `go test -race ./internal/algorunner -run '^TestPushGalleryUntilDelivered' -count=1`）

- [x] 1.5 (deps: 1.4) 对账回路测试 — 新增 `internal/algorunner/gallery_reconcile_test.go`：
  ① `TestGalleryReconcileConverges`：`liveRunners=1`、间隔 10ms、broadcaster 固定 `delivered=1`，
  100ms 内 provider 被调用 ≥ 3 次（证明是周期推送而非一次性）；
  ② `TestGalleryReconcileHealsLateRunner`：前 3 次 `delivered=0` 之后返回 1，断言最终出现成功推送且
  没有走 60 次阶梯；
  ③ `TestGalleryReconcileSkipsWithoutRunners`：`liveRunners=0` 时 provider 调用次数为 0；
  ④ `TestGalleryReconcileRepublishesAfterMutation`：在 fake 的广播回调里调用 `BroadcastUpsert` 推进代数，
  断言一次对账内 provider 被调用 2 次（补推 1 次，无自旋）；
  ⑤ `TestGalleryReconcileStopsOnShutdown`：`StopAll` 后用 `done` 通道确认回路退出且不再调用 provider
  （check: `go test -race ./internal/algorunner -run '^TestGalleryReconcile' -count=1`）

- [x] 1.6 (deps: 1.5) 包级收口校验与装配点回归 —
  `internal/algorunner` 全量测试、格式与 vet；确认 `internal/app/app.go:760` 的装配调用签名未变
  （check: `go test -race ./internal/algorunner/... -count=1 && gofmt -l internal/algorunner && go vet ./internal/algorunner/...`）

## 2. 验收门禁

- [x] 2.1 (deps: 1.6) `python3 native/scripts/build.py go test -race ./internal/algorunner/... -count=1`（经 native wrapper 跑一次，
  证明没有意外的跨包原生依赖差异）
- [x] 2.2 (deps: 2.1) `make go-check`（`./cmd/... ./internal/...` 的 build/vet/test 全量）
- [x] 2.3 (deps: 2.2) 收尾统一跑一次 `make check`（含前端与 native 测试；AGENTS.md 要求收工前过一次）

## 3. 执行环境备注（非验收项）

- `internal/algorunner` 是本变更唯一改动的 Go 包；`internal/app` 的装配调用
  `p.Supervisor.SetGallerySync(&supervisorGallerySyncAdapter{service: comp.Service}, p.Broadcaster)`
  （internal/app/app.go:760）签名未变，无需同步修改。
- 该工作树是全新 checkout：`build/deps` 的 FFmpeg/ONNX 前缀通过符号链接复用主检出的已校验产物，
  否则 wrapper 会要求联网 `make native-deps`。
- 沙箱内 pnpm store 只读（`ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK`），`frontend-install` 无法执行；
  2.3 以 `make -o frontend-install frontend-check`（format:check / lint / test 233 通过 / build）+
  `make pkg-drift-check native-test pkg-test api-docs go-check` 执行，退出码 0。
- `internal/webui/dist` 是 gitignored 构建产物，工作树内从主检出复制了一份，否则 `internal/webui` 的
  embed 测试会失败（与本变更无关）。

## 4. Verify 首轮结论与整改（Blocker 逐条）

首轮 `/luoshu-verify`（独立 subagent）判 FAIL，三个 Blocker 都是"测试比契约弱"，不是实现错：

- V1：`TestGalleryReconcileConverges` 原来用 1s 预算，契约要 100ms 内 ≥3 次 → 改成 100ms 预算。
- V2：`TestGalleryReconcileHealsLateRunner` 原来用 2s 窗口，契约要 5×interval，且阶梯（500ms/次）
  同样能在 ~1.5s 通过、断言无法排除它 → 改成 50ms 间隔（5×interval=250ms 窗口）、断言成功落在第 4 次推送；
  阶梯重试间隔 500ms，物理上不可能在 250ms 内产出成功，时序本身即判别式。
- V3：`TestGalleryReconcileSkipsWithoutRunners` 原来只断言 `callCount()==0`，契约还要求"不产生 Warn 日志"
  → 改用 `zaptest/observer` 抓 Warn+ 日志并断言为空。

顺带补强（首轮 Notes）：

- 新增 `TestGalleryReconcileWarnsWhenNoRunnerTakesSnapshot`（V6：Warn 字段 `sync_id/items_count/alive_runners/generation` 齐全）。
- 新增 `TestGalleryReconcileSkipsBroadcastWhenProviderFails`（V7：读取失败绝不广播空快照，Warn 带 `error`）。
- `galleryReconcileLoop` 对 `galleryReconcileDone` 做 nil 守卫，`SetGallerySync` 在 `startLoop` 分支内按需建通道：
  没人能在零值 `Supervisor` 上把停机变成 nil channel panic。
- `design.md` 的 `sync.Once`/`startGalleryReconcileLoop`/「StopAll 不变」与 `proposal.md` 的 Warn 字段表
  已按实际实现改正（`verify.md` 未改动，仍是冻结契约）。

