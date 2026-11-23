# Verify Contract: 2026-11-23-gallery-sync-reconcile

> Propose 阶段冻结的静态验收契约。Verify 阶段只对照本契约与 `base_commit`
> （`c3c744d5b4449031ff098712dca335c2c2c2057a`）的完整 diff 输出结论到 `verify-report.md`，
> 不修改本契约，也不修改业务源码。

## 验收标准

- [ ] **V1 周期收敛（与上一次结果无关）**：diff 中存在由 `time.Ticker` 驱动的全量快照推送，
  其触发条件不读取"上一次是否 `delivered>0`"；`TestGalleryReconcileConverges` 在 `interval=10ms`、
  `liveRunners=1`、恒定 `delivered=1` 的桩下，100ms 内 `GetGallerySyncData` 被调用 **≥3** 次。
  旧实现在同样的桩下只会调用 1 次（阶梯成功即 return）——这是本变更与旧行为的判别式。

- [ ] **V2 晚到 runner 自愈**：`TestGalleryReconcileHealsLateRunner` 中前 3 次广播返回 `delivered=0`
  （模拟人脸 runner 尚未连上），随后返回 `delivered=1`；断言在 `5 × interval` 内出现成功推送，
  且**没有**走"60 次 × 500ms"的冷启动阶梯（阶梯只服务于 spawn 快路径）。

- [ ] **V3 空载不空转**：`liveRunners()==0` 时对账不得取快照——`TestGalleryReconcileSkipsWithoutRunners`
  断言 `GetGallerySyncData` 调用次数为 **0**，且不产生 `Warn` 级日志。

- [ ] **V4 代际守卫有界且方向正确**：`TestGalleryReconcileRepublishesAfterMutation` 在广播回调内推进
  `GalleryBroadcaster.Generation()`，断言一次对账内 `GetGallerySyncData` 调用 **恰为 2** 次（补推 1 次、
  无自旋）；源码层面 `generation := broadcaster.Generation()` 出现在 `pushGalleryOnce` **之前**，
  且循环短路条件里 `republish >= followUps` 先于 `Generation()` 比较。

- [ ] **V5 停机收口**：`TestGalleryReconcileStopsOnShutdown` 在 `StopAll` 后经 `done` 通道确认回路已退出，
  且之后不再调用 provider；源码层面 `StopAll` **未**获取 `gallerySyncMu`，对账路径在 `TryLock` 成功后
  复查 `stopCh`。

- [ ] **V6 失败可见、成功不刷屏**：`delivered==0` 且有存活 runner 时输出 `Warn`（消息含
  `sync_id`/`items_count`/`alive_runners`/`generation`）；成功推送为 `Debug`；错误路径为 `Warn` 且带
  `zap.Error`。日志中不得出现底库特征向量、姓名或任何登记照内容（`go-host/sensitive-logging`）。

- [ ] **V7 出错不得广播**：`pushGalleryOnce` 在 `provider` 返回错误时直接返回，绝不调用 `BroadcastSync`
  （否则一次 DB 抖动会以空快照清空所有 runner 的底库）。

- [ ] **V8 契约零变更（负向边界）**：
  - `native/**`、`packages/**`、`web/**` 在本 diff 中**零改动**；`native/include/Zhulong/engine.h` 零改动。
  - IPC 消息常量保持 `MSG_GALLERY_SYNC=10` / `UPSERT=11` / `DELETE=12` / `ACK=13`；
    `GallerySyncItem`、`GallerySyncShardPayload`、`GalleryUpsertPayload`、`GalleryDeletePayload` 的
    JSON 字段名与 `shardBudget`（`90% × MaxIpcPayloadSize`）不变。
  - 不新增 `config.toml` / viper 键；`RecoveryOptions` 字段不变（不把对账间隔塞进恢复参数）。
  - 识别语义零改动：阈值、Top-K、`min_match_margin`、融合与轨迹投票参数、`GalleryRegistry`/`Matcher` 逻辑不变。
  - `internal/app/app.go` 的 `SetGallerySync` 调用签名不变；`internal/algorunner` 对外导出 API 只增不改。

## 门禁命令（必须全绿，不得跳测或放宽断言）

```bash
go test -race ./internal/algorunner/... -count=1
gofmt -l internal/algorunner        # 期望无输出
go vet ./internal/algorunner/...
python3 native/scripts/build.py go test -race ./internal/algorunner/... -count=1
make go-check
```

收尾门禁（仓库要求，收工前执行一次）：

```bash
make check
```

## 不在本契约内

- 板端 RK3568 真机验证（多算法共 broker 场景下 `candidates_json` 恢复非空）属于部署验证，
  不计入静态门禁；如需登记，另附现场记录而不改写本契约。
- `.luoshu/specs/native/algo-plugin-contract.md` §7.3/§234-241 的措辞更新属于 Archive 阶段动作。
