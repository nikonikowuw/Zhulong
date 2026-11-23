# Design: 2026-11-23-gallery-sync-reconcile

## 架构与实现方案

### 0. 不变量（改动前已成立，改动后必须仍然成立）

| 编号 | 不变量 | 依据 |
| --- | --- | --- |
| I1 | 同一时刻只允许一次全量快照推送在途 | `gallerySyncMu` 的注释：两个阶梯交错会把不同 `sync_id` 的分片塞进同一 runner，runner 每次换 id 就重置装配上下文，快照永远收不齐，而两边都看到非零写入 |
| I2 | 一次推送的分片必须连续发出 | runner 侧 5s 组装窗口（`native/sdk/src/internal/gallery_index.hpp`）；`BroadcastSync` 同步串行发完所有分片 |
| I3 | 快照只在成功取到时才广播 | `pushGalleryOnce` 在 `provider` 出错时直接返回，绝不用空切片覆盖 runner 里的底库 |
| I4 | 停机不得被后台推送阻塞 | `StopAll` 从不获取 `gallerySyncMu`；阶梯取到锁后再查一次 `stopCh` |

新增不变量：

- **I5（收敛）**：只要有 `RUNNING`/`STARTING`/`ROLLING_BACK` 的 runner，且 provider/broadcaster 已装配，
  底库在 `galleryReconcileInterval`（30s）内至少被完整广播一次，与"上一次是否 delivered 成功"无关。
- **I6（不倒退）**：一次成功广播若跨越了运行期增量（`GalleryBroadcaster.Generation()` 前进），
  必须用新快照立即补推一次。

### 1. `internal/algorunner/gallery.go`——内容代数

```go
type GalleryBroadcaster struct {
	broadcaster AIMessageBroadcaster
	logger      *zap.Logger
	generation  atomic.Uint64
}

// Generation 是"底库内容代数"：每次运行期增量（登记/删除）成功后前进一格。
// 它衡量的是"宿主真源变过几次"，不是"runner 收到过几次"，因此只增不减、
// 进程重启归零；跨进程的语义由下一次全量快照重新建立。
func (b *GalleryBroadcaster) Generation() uint64 { return b.generation.Load() }
```

维护点（顺序很重要）：

```go
func (b *GalleryBroadcaster) BroadcastUpsert(ctx context.Context, item GalleryUpsertPayload) error {
	if b.broadcaster == nil {
		return nil
	}
	// 先推进代数再发布：即使 marshal 或 IPC 失败，这一代也已经"脏"了，
	// 让对账多推一次是可接受的代价，漏推才是本变更要消灭的故障。
	b.generation.Add(1)
	...
```

`BroadcastDelete` 同样处理；`BroadcastSync` **不**推进（它不改真源）。

### 2. `internal/algorunner/supervisor.go`——共享快照推送（代际守卫）

阶梯与对账走同一条推送路径，避免两套"我推过了"的判断：

```go
// pushGallerySnapshot 取一次快照并广播，若广播期间底库真源发生了变化
// （Generation 前进），立刻用新快照补推，最多 followUps 次。
//
// 只补推、不回滚：连续登记时宁可把剩余窗口交给下一轮对账，也不在这条
// 路径上无界自旋——30s 后的那一轮一定会带最新内容。
func (s *Supervisor) pushGallerySnapshot(
	provider GallerySyncProvider,
	broadcaster *GalleryBroadcaster,
	followUps int,
) (items []GallerySyncItem, syncID string, delivered int, err error) {
	for republish := 0; ; republish++ {
		generation := broadcaster.Generation()
		items, syncID, delivered, err = s.pushGalleryOnce(provider, broadcaster)
		if err != nil || delivered == 0 {
			// 没人收时补推没有意义：下一次 tick 会重来。
			return items, syncID, delivered, err
		}
		if republish >= followUps || broadcaster.Generation() == generation {
			return items, syncID, delivered, nil
		}
		s.logger.Debug("gallery snapshot raced a gallery mutation; republishing",
			zap.String("sync_id", syncID),
			zap.Uint64("generation", generation),
		)
	}
}
```

`pushGalleryUntilDelivered` 的每次 attempt 改为
`items, syncID, delivered, err = s.pushGallerySnapshot(provider, broadcaster, gallerySnapshotFollowUps)`，
其余语义（换新 `sync_id`、`delivered>0` 即成功、预算耗尽记 Error）保持不变。

### 3. 对账回路

```go
const (
	// galleryReconcileInterval 是底库周期对账的间隔。
	//
	// 底库是状态型数据，广播是瞬时投递：Broker 对 MSG_GALLERY_* 没有 replay
	// cache，晚连上的 runner 只能等下一次全量推送。以前唯一的"下一次"是
	// 进程重启，于是投递失败 = 该 runner 终生空底库。30s 是收敛窗口与
	// 空载开销（48 人约 330KB JSON、一次分页查询）之间的折中：ARM 上
	// 一次推送的 marshalling 是毫秒级，30s 一次对 CPU 无感。
	galleryReconcileInterval = 30 * time.Second

	// gallerySnapshotFollowUps 是代际守卫允许的补推次数。
	gallerySnapshotFollowUps = 1
)
```

```go
// SetGallerySync 在装配时启动唯一的对账协程。
//
// 不引入新的 Start/Stop 生命周期方法：内部协程由既有 stopCh 收口，
// 避免 internal/app 的 stopSequence 为一个后台回路再排一次序。
// 用受 mu 保护的 bool 而不是 sync.Once：装配与停机都在同一把锁下读写，
// 且 StopAll 需要知道"回路到底起没起"来决定要不要 join。
func (s *Supervisor) SetGallerySync(provider GallerySyncProvider, broadcaster *GalleryBroadcaster) {
	s.mu.Lock()
	s.gallerySyncProvider = provider
	s.galleryBroadcaster = broadcaster
	startLoop := !s.galleryReconcileStarted && provider != nil && broadcaster != nil
	if startLoop {
		if s.galleryReconcileDone == nil {
			s.galleryReconcileDone = make(chan struct{})
		}
		s.galleryReconcileStarted = true
	}
	s.mu.Unlock()

	// 解锁后再起协程，保证回路不会读到半装配状态。
	if startLoop {
		go s.galleryReconcileLoop(galleryReconcileInterval, s.countLiveRunners)
	}
}

// galleryReconcileLoop 的间隔与存活判据是参数而不是常量读取，
// 让白盒测试能用 10ms 的间隔和桩函数驱动收敛，而不用等 30 秒。
func (s *Supervisor) galleryReconcileLoop(interval time.Duration, liveRunners func() int) {
	if interval <= 0 || liveRunners == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
		s.reconcileGallery(liveRunners)
	}
}
```

```go
func (s *Supervisor) reconcileGallery(liveRunners func() int) {
	s.mu.RLock()
	provider := s.gallerySyncProvider
	broadcaster := s.galleryBroadcaster
	s.mu.RUnlock()
	if provider == nil || broadcaster == nil {
		return
	}
	// 没有 runner 时连快照都不取：空载也每 30s 查一遍分页 DB、marshal 330KB
	// 纯属浪费，而且会把"没人可推"刷成日志噪音。
	if alive := liveRunners(); alive == 0 {
		return
	}
	// 阶梯正在推同一份状态时让路。TryLock 而不是 Lock：在这里排队会把
	// 一次 30s 的阶梯叠成背靠背的两次全量推送，且让 tick 漂移。
	if !s.gallerySyncMu.TryLock() {
		s.logger.Debug("gallery reconcile skipped: a snapshot push is already in flight")
		return
	}
	defer s.gallerySyncMu.Unlock()
	select {
	case <-s.stopCh:
		return
	default:
	}

	items, syncID, delivered, err := s.pushGallerySnapshot(provider, broadcaster, gallerySnapshotFollowUps)
	alive := liveRunners()
	switch {
	case err != nil:
		s.logger.Warn("gallery reconcile failed",
			zap.String("sync_id", syncID), zap.Int("alive_runners", alive), zap.Error(err))
	case delivered == 0:
		// 关键信号：真有 runner 在跑却一个都没收下——和"收件人是别的算法包"
		// 一起构成这次事故在旧日志里完全看不出来的原因。
		s.logger.Warn("gallery reconcile reached no runner",
			zap.String("sync_id", syncID), zap.Int("items_count", len(items)),
			zap.Int("alive_runners", alive), zap.Uint64("generation", broadcaster.Generation()))
	default:
		// 成功必须是 Debug：30s 一次，Info 会淹没真正的告警。
		s.logger.Debug("gallery reconcile delivered",
			zap.String("sync_id", syncID), zap.Int("items_count", len(items)),
			zap.Int("delivered", delivered), zap.Int("alive_runners", alive),
			zap.Uint64("generation", broadcaster.Generation()))
	}
}
```

存活判据复用 `SummaryCounts` 的口径（`watcher.currentStatus()`，只取
`StatusRunning` / `StatusStarting` / `StatusRollingBack`），先做一次 watcher 快照再在锁外逐个取值：

```go
func (s *Supervisor) countLiveRunners() int
```

### 4. 装配点

`internal/app/app.go:760` 不变：`SetGallerySync` 内除原有赋值外，按受锁保护的 `startLoop` 判断起一次
`go s.galleryReconcileLoop(galleryReconcileInterval, s.countLiveRunners)`；`Supervisor` 新增
`galleryReconcileStarted bool` 与 `galleryReconcileDone chan struct{}` 两个字段。

`StopAll` 在 `close(s.stopCh)` 之后调用 `stopGalleryReconcile()`：回路收到 `stopCh` 关闭 `done` 通道，
`stopGalleryReconcile` 在 `stopAlgorithmTimeout` 预算内等它收敛。之所以要 join 而不是"close 就撒手"，
是 `go-host/concurrency-and-errors` 的硬性要求（Stop 必须让内部协程收敛后再返回），同时也让测试能确定性地
断言"停机后不再推送"。预算小于单次推送上限（`gallerySyncTimeout`）：停机不该被一次在途推送拖住，
超时只记 `Debug`，那条在途快照发往的 runner 本来就在被回收。

### 5. 数据流

```
person CRUD ──► BroadcastUpsert/Delete ──► generation++ ──► MSG_GALLERY_UPSERT(11)/DELETE(12)
                          │
spawn/respawn ──► triggerGallerySyncAsync ──► ladder(500ms×60) ──► pushGallerySnapshot(followUps=1)
                          │                                              │
ticker(30s) ──► reconcileGallery ──► TryLock(gallerySyncMu) ─────────────┤
                                                                         ▼
                                        GetGallerySyncData(ctx) ──► BroadcastSync ──► shards(MSG_GALLERY_SYNC=10)
                                                                         │
                                            generation 前进? ──是──► 立即重取快照补推一次
```

### 外部技术调研与参考

无新增依赖。只用标准库：

- `sync/atomic.Uint64`（Go 1.19+）、`sync.Mutex.TryLock`（Go 1.18+）——`go.mod` 声明 `go 1.27.1`，均可用。
- `time.Ticker` 的语义：接收方慢时丢 tick 而不是排队（`time` 文档，"the ticker will drop ticks to make up for slow receivers"），
  正好符合"对账不需要补课，只需要看到最新状态"。
- 参考 `internal/algorunner/supervisor.go` 既有注释确立的约束：`gallerySyncMu` 不得被 `StopAll` 获取。

### 风险与审查重点（Review Focus）

1. **补推自旋**：`pushGallerySnapshot` 的 `followUps` 上限必须真正生效——连续登记场景下
   `Generation()` 每轮都变，若无上限会退化成"边登记边无限全量推送"，把 IPC 打满。审查点：
   循环条件里 `republish >= followUps` 在 `Generation` 比较**之前**短路。
2. **陈旧快照覆盖**：守卫窗口必须覆盖"取快照 → 广播完成"整段，捕获 `Generation()` 的时机在
   `pushGalleryOnce` 之**前**。审查点：`generation := broadcaster.Generation()` 不得被挪到广播之后。
3. **锁与停机**：对账路径不得在持 `gallerySyncMu` 时做 IO 之外的阻塞操作，且取到锁后必须复查
   `stopCh`（否则 `StopAll` 之后还会写一次 IPC）。审查点：`TryLock` 之后紧跟 `defer Unlock` 与 `select stopCh`。
4. **测试竞态**：`gallerySyncFakeBroadcaster` 现在是单 goroutine 假设（裸 `append`），
   对账回路会从另一个 goroutine 调它；不线程安全化会让 `-race` 直接失败。审查点：所有跨 goroutine
   读写的 fake 字段都有锁或原子。
5. **空底库语义**：底库真的为空时对账会把 runner 里的残留模板清掉，这是刻意的（宿主是唯一真源）；
   但 `GetGallerySyncData` 出错时**不得**退化成 `(nil, nil)`——否则一次 DB 抖动就能清空所有 runner 的底库。
   审查点：`pushGalleryOnce` 在 `err != nil` 时不广播，且 `internal/app/adapters.go` 的适配器不吞错。
