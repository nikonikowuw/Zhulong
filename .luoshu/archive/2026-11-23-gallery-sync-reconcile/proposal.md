# 底库同步收敛回路：有状态底库不再依赖一次性广播

## 背景与目标

人脸底库是**有状态数据**，却走了一条**无状态广播通道**，而且"送达判据"根本不是收件人给的。

- `delivered` 是 `Zhulong_engine_broadcast_ai_message` 的**写入成功客户端数**：`UnixSocketServer::broadcast`
  遍历 `client_channels` 里**全部**客户端（不区分子进程身份），逐个 `send_msg` 成功才计数
  （`native/src/algorithm/ipc/unix_socket.cpp:588-606`；`stream_id==0` 时取各 broker 最小值，
  `native/src/pipeline/engine.cpp:1273-1285`）。
- 因此当一路视频流挂了多个算法包时（板端现状：`face_recognition` 与 `ppe_helmet` 共用
  `/tmp/zhulong_broker_1.sock`），**先连上的非人脸包**就能把 `delivered` 顶到 1，
  `pushGalleryUntilDelivered` 立刻认定成功并停止重试（`internal/algorunner/supervisor.go:266-278`）。
- `FrameBroker` 对 `MSG_GALLERY_*` **没有 replay cache**，只缓存 `MSG_TASK_CONFIG` / `MSG_TASK_REGIONS`
  （`native/src/algorithm/ipc/frame_broker.cpp:941-966`），握手重放也只覆盖任务配置
  （`frame_broker.cpp:834-843`）；晚连上的人脸 runner 永远补不到快照。
- runner 回了 `MSG_GALLERY_ACK`，但宿主**没有消费方**（`internal/algoipc/protocol.go:77` 只有常量），
  于是"底库在 runner 里是空的"对宿主完全不可见。
- 全量同步只有两个触发点（`watcher.go:460` respawn、`supervisor.go:554` StartAlgorithm），
  没有任何周期性重推：**错过一次 = 该 runner 终生全员陌生人**，直到下一次重启或重提特征。

2026-11-23 板端现场（RK3568，`/opt/zhulong`）：

| 时刻（UTC） | 事件 |
| --- | --- |
| 04:08:05 | host 启动（pid 1892415） |
| 04:08:06 | `ppe_helmet` runner 连上 broker（pid 1892461） |
| 04:08:08 | `face_recognition` runner 连上 broker（pid 1892526，晚 2 秒） |
| 04:08:07.292 / .339 / 09.550 | 三次 `cold-start gallery sync` 全部 `items=48 delivered=1` |
| 04:00:24 | 重启前最后一条正常记录：`matched=1 similarity=0.527` |
| 04:09:07 起 | 连续 `matched=0 / similarity=0.0 / candidates_json='[]'` |

断档期记录的探针特征本身是健康的（2048 字节、L2 范数恰为 1.0000、与当前 48 条模板的最大余弦
0.11~0.38）——若那时 runner 里真有这 48 条模板，`Matcher::match` 必然会输出 3 个候选
（候选列表在阈值判断**之前**构造）。`candidates='[]'` 只能是 runner 内底库为空。
重提特征能立刻恢复，是因为它走逐条 `MSG_GALLERY_UPSERT`，**绕过了分片装配，也不受 delivered 判据约束**。

目标（本变更只做 Go 侧收敛回路，改的是"状态如何被持续对账"，不是协议）：

- **G1 最终收敛**：只要有 runner 存活且人脸 runner 已连上，底库快照在一个对账周期（30s）内必定送达。
- **G2 不倒退**：陈旧快照不得把运行期新增的登记照从 runner 里抹掉超过一个对账周期。
- **G3 可观测**：`delivered==0`（真的没人收）与"收件人是别的算法包"在日志里可分辨。
- **G4 零契约变更**：不动 ABI、不动 IPC 消息格式与分片语义、不重编算法包。

## In Scope（本次包含）

- `internal/algorunner/gallery.go`：`GalleryBroadcaster` 增加内容代数（`atomic.Uint64`）与
  `Generation()` 访问器；`BroadcastUpsert` / `BroadcastDelete` 在发布前自增。
- `internal/algorunner/supervisor.go`：新增周期对账回路（`galleryReconcileInterval = 30s`），
  由 `SetGallerySync` 启动一次、由 `StopAll` 的 `stopCh` 退出；与既有重试阶梯共用 `gallerySyncMu`，
  用 `TryLock` 让对账**跳过**而非排队；抽出"取快照 + 广播 + 代际守卫补推"的共享辅助函数，
  阶梯与对账共用同一条推送路径。
- 日志：对账成功降为 `Debug`（每 30 秒一次，用 `Info` 会淹掉真正需要人看的告警）；
  `delivered==0` 且有存活 runner 时 `Warn`，字段为 `sync_id / items_count / alive_runners / generation`
  （不记 `delivered`，它在这个分支恒为 0）；错误路径 `Warn` 并带 `zap.Error`。日志不含特征向量、姓名或登记照内容。
- 测试：`internal/algorunner/gallery_sync_test.go` 的 fake 线程安全化（后台 goroutine 与断言共享状态，
  `-race` 必跑）；新增 `internal/algorunner/gallery_reconcile_test.go` 覆盖收敛、跳过、补推、停机。
- 规格对齐：`.luoshu/specs/native/algo-plugin-contract.md` §7.3 的"可靠送达判据是投递计数"
  与 §234-241 的"底库靠有界重试"结论需要在 **Archive** 阶段改写（本变更不改 spec 正文）。

## Out of Scope（明确不做）

- **native 侧 replay cache、`ClientInfo.algorithm_id`、按算法定向投递、消费 `MSG_GALLERY_ACK`**：
  需要消息载荷或 ABI 变更，且要让 `packages/**` 重编，收益（消除 ≤30s 窗口）与本变更的回归面不成比例。二期单开。
- 算法包 / SDK 改动（`packages/**`、`native/sdk/**`）：本变更不触碰 C++ 源码。
- 记录侧"空候选"启发式自愈（`record` 反向依赖 `algorunner`）：周期对账已把最坏窗口压到 30s，
  不为此引入跨层依赖。
- 配置面：不为 `galleryReconcileInterval` 增加 `config.toml` 键（viper 严格模式，配置面越大越容易炸启动）。
- 识别语义（阈值、Top-K、融合、轨迹投票）与 `GalleryRegistry` / `Matcher` 的任何逻辑。
- 已存在的两个 active 变更（`2026-11-12-stranger-cluster-trajectory`、
  `2026-11-15-face-track-fusion-identity`）的 `context.yaml` 诊断错误，不在本变更内顺手修。

## 影响范围（模块 / 接口契约 / Specs & Rules）

- **模块**：`internal/algorunner`（`supervisor.go`、`gallery.go` 与测试）。`internal/app/app.go:760`
  的唯一装配点签名不变；`GalleryBroadcaster` 只新增方法，不破坏既有调用。
- **契约**：`MSG_GALLERY_SYNC(10)` 分片 + 5s 组装窗口 + `stream=0` 广播语义全部保持不变；
  `person_id` 仍取业务键原值；`BroadcastSync` 仍返回各分片 `delivered` 的最小值。
- **Specs**：`native/algo-plugin-contract.md`（§7.3、§7.4、§234-241）、`backend/algorithm-guidelines.md`
  （`internal/algorunner` 的"看护与自愈"职责边界）、`architecture.md`（宿主侧自愈约定的表述）。
- **Rules**：`algorithm/algorithm-worker-ipc`、`algorithm/algorithm-runner-process`、
  `go-host/concurrency-and-errors`、`go-host/code-style`、`go-host/test-wiring`、`go-host/sensitive-logging`。
