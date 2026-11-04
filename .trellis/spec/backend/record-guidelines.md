# 感知记录与抓拍规范

> 适用于 `internal/record` 的识别、告警、目标抓拍记录、证据图和保留清理；不负责连续视频录像。

## 1. 数据流与职责

```text
Runner MSG_RESULT
  → camera.StreamHub 解析与区域过滤
  → record.Service 持久化 / 排队抓拍
  → EventHub 广播同一份过滤后结果
```

- 区域过滤由 `internal/region.Config.Filter` 实现，并在 `StreamHub` 向持久化 consumer 和 EventHub fan-out 前执行。ROI 是 allowlist（未配置时全放行），MASK 命中优先拒绝；检测框、人体框、车辆框分别取底部中心判定，分类没有框、不参与过滤，人体内人脸和车辆内车牌不单独判定。
- AI 流按相机共享，Hub 不得猜测结果属于哪个算法。Runner 应在结果载荷中提供 `algorithm_id`；缺失时保留为未知。
- `record.Service` 负责把推理结果映射为识别、告警和目标抓拍记录，并按 cooldown 去重。相机流管理与区域策略属于 camera/region 域；文件清理属于 storage 域。
- **目标跟踪与身份去重契约**：
  - `track_id` 归**宿主独占**：由宿主关联器在 `(camera_id, algorithm_id, kind)` 命名空间内单点生成，语义为「仅进程运行期内可比、进程内单调自增」；算法包内部的跟踪 ID 不得经 IPC 载荷出口。载荷里的 `track_id` 仅用于 `IsFaceCloseup()` 退化判据，不进库、不参与去重键、不回写进模型载荷或事件载荷。
  - **去重键格式**：统一使用 `trk:` 前缀区分身份键与回落键：
    - 识别：`rec:{camera}:trk:{id}`；有已登记人员时与 `rec:{camera}:person:{person_id}` 组成两级抑制，必须通过原子 `AllowAll` 判定。
    - 人体/近景抓拍：`cap:{camera}:person:trk:{id}`；无身份回落时近景用 `face:box`、人体用 `person:box`。
    - 车辆抓拍：`cap:{camera}:vehicle:trk:{id}`；有车牌时与 `cap:{camera}:vehicle:plate:{plate_no}` 组成两级抑制，必须通过原子 `AllowAll` 判定。
    - 通用检测：`cap:{camera}:{label}:trk:{id}`；无身份回落保持 `cap:{camera}:{label}`。
    - 告警：保持动作语义粗键 `alarm:{camera}:{label}`。
  - 匹配算法采用 IoU (≥ 0.2) 主判据 + 中心距与高度比兜底；`lastSeen` 命中更新强制单调防倒退；TTL 动态绑定 2 × CooldownWindow。

参考实现与测试：`internal/camera/stream_hub.go`、`internal/region/filter.go`、`internal/camera/stream_hub_test.go`。

## 2. 持久化与来源归属

- schema 以 `internal/database/migrations/` 中的已应用迁移为准；禁止从本文件复制 SQL 来改 schema。
- `algorithm_id` 缺失时写 SQL `NULL`，不要写空字符串。SQLite 外键对空字符串仍要求存在父行，空字符串会导致记录插入失败。
- `algorithm_name` 与 `algorithm_version` 是写入时快照。算法卸载后，外键按 `ON DELETE SET NULL` 保留记录；读取时不得 join 已卸载的算法表来“补回”快照。
- 新增记录使用推理时间戳转 UTC；时间戳缺失时才使用宿主当前 UTC 时间。
- 记录批量写入失败时记录错误，不得继续排入对应证据图任务，避免创建无数据库引用的孤儿图片。

实现/迁移：`internal/record/service.go`、`internal/record/model.go`、`internal/database/migrations/000015_algorithm_delete_set_null.up.sql`、`000016_capture_algorithm_ref.up.sql`。算法卸载保留历史记录由 `internal/record/algorithm_uninstall_test.go` 覆盖。

## 3. 证据图生命周期

- 抓拍是记录的补充，不是记录写入的前置条件；图像生成和写盘在 `SnapshotStore` 后台 worker 执行，不阻塞推理结果消费路径。
- 队列有界（当前容量 32），提交非阻塞；依赖缺失、队列满或存储写门关闭时允许丢弃抓拍任务。已接收任务在 `Stop` 时排空。需要观测丢弃时读取 `SnapshotStore.Dropped()`。感知层不设单帧人工截断（杜绝冷却饥饿），多目标瞬时并发直接由队列容量与 `Dropped()` 观测承接。
- 每个任务执行时重新读取媒体根目录，并检查 storage 写门；路径存为媒体根内的相对路径（`snapshots/...`），媒体读取仍走 `internal/mediafile`，不能自行拼接绝对路径对外暴露。
- C ABI 抓拍支持透传目标帧 `PTS` 时间戳，Native 侧历史环形缓存（4 帧容量）优先精确匹配目标帧；若历史已淘汰或未指定有效 PTS，退回读取当前最新帧。图像生成和写盘在 `SnapshotStore` 后台 worker 执行，不阻塞推理结果消费路径。
- 数据库记录先写入，之后才提交抓拍任务。因此路径字段可能在图像生成前已写入，非空路径不保证文件已存在。任务队列拒绝时，调用方会尝试清空对应记录路径；后台遇到写门关闭、媒体根解析失败、抓拍失败或零张图片写入时，也会尝试清空该记录的抓拍路径。**这些清理是尽力而为，可能异步失败；若一个任务部分写入成功，当前实现不会逐输出项清理未写入路径。**依赖缺失导致 `SnapshotStore` 不可用时，服务也可能无法执行队列后的路径清理。调用方必须容忍媒体读取返回缺失。

参考实现：`internal/record/service.go`、`internal/record/snapshot.go`、`internal/engine/engine.go`、`native/src/abi/engine.cpp`。测试：`internal/record/snapshot_test.go`。

## 4. 文件与数据库行清理

- storage 域负责按存储策略删除媒体文件；record 域不绕过 storage 清理器直接删除证据文件。
- `RetentionPruner` 的 TTL 巡检只考虑超过配置保留期的 `recognitions` 与 `captures`。只有当一行引用的所有非空快照路径均确认不存在时才删除该行；任一文件仍存在则保留整行。文件检查出错时应中止本轮，不得按“文件不存在”处理。
- **容量淘汰是 TTL 巡检的唯一例外**：storage 删除未过期快照后，app 层把已删除的基名 ID 写入 `pending_snapshot_deletes` outbox，再由 `RetentionPruner` 按主键批量删除 `captures` / `recognitions`。正常路径必须先落 pending 再删行；删除成功后才清 pending。失败/进程重启后由 `Start` 与每轮 `sweep` 重试。若 enqueue 本身失败（SQLite 短暂不可用），app 可退化为直接按主键删除并记录 error；直接删除仍失败时由 TTL 巡检兜底。禁止用「未过期全表扫描 + `os.Stat`」代替，因为记录先落库、图异步写盘，扫描会误删正在写的新抓拍。
- `DeleteBySnapshotIDs` 只删 `captures` / `recognitions`，绝不删 `alarms`；`alarms` 是告警元数据，其图片由 storage 的 `.lock`/`EvidenceGuard` 保护。
- **历史告警补锁**：升级时 `EnsureAlarmEvidenceLocks` 扫描 `alarms.image_path` / `crop_path`，对存在的图片幂等补写 `.lock`；storage 另以 `EvidenceGuard` 按 alarm ID 查询兜底，guard 出错时失败关闭（跳过删除）。
- 告警行不由自动保留清理删除。高价值告警证据文件由 storage 使用保护标记防止自动淘汰；清理策略详见 [Storage 规范](./storage-guidelines.md)。
- 清理器按批次执行并支持取消；没有有效保留策略或文件检查器时不删除记录。清理是幂等的，测试需覆盖文件先删、数据库行后删的顺序，以及 pending 失败/取消后重试。

实现/测试：`internal/record/retention.go`、`internal/record/retention_test.go`、`internal/storage/cleaner.go`。

## 5. 验证

```bash
python3 native/scripts/build.py go test -race ./internal/record ./internal/camera ./internal/region
```

涉及跨层生命周期、迁移或原生抓拍改动时，再按项目要求运行 `make check`；具体 Native / SQLite 验证边界以对应层规范为准。
