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
- 队列有界（当前容量 32），提交非阻塞；依赖缺失、队列满或存储写门关闭时允许丢弃抓拍任务。已接收任务在 `Stop` 时排空。需要观测丢弃时读取 `SnapshotStore.Dropped()`。
- 每个任务执行时重新读取媒体根目录，并检查 storage 写门；路径存为媒体根内的相对路径（`snapshots/...`），媒体读取仍走 `internal/mediafile`，不能自行拼接绝对路径对外暴露。
- 当前 C ABI 抓拍读取指定流的**最新帧**，不保证与产生该推理结果的帧具有相同时间戳。若业务要求逐结果帧精确取证，需要另行定义并传递帧标识/缓冲区生命周期，不能靠当前最新帧接口推断。
- 数据库记录先写入，之后才提交抓拍任务。因此当前实现中路径字段可能在图像生成前已写入；抓拍失败、存储熔断、队列满或依赖缺失时，路径字符串可能指向不存在的文件。**非空路径不代表文件存在，调用方必须容忍媒体读取返回缺失。** `snapshot.go` 的注释称失败时路径留空，但 `service.go` 目前没有异步回写清空路径的逻辑；这是实现/注释不一致，不能把“失败后路径必为空”当成已实现契约。

参考实现：`internal/record/service.go`、`internal/record/snapshot.go`、`internal/engine/engine.go`、`native/src/abi/engine.cpp`。测试：`internal/record/snapshot_test.go`。

## 4. 文件与数据库行清理

- storage 域负责按存储策略删除媒体文件；record 域不绕过 storage 清理器直接删除证据文件。
- `RetentionPruner` 只考虑超过配置保留期的 `recognitions` 与 `captures`。只有当一行引用的所有非空快照路径均确认不存在时才删除该行；任一文件仍存在则保留整行。文件检查出错时应中止本轮，不得按“文件不存在”处理。
- 告警行不由自动保留清理删除。高价值告警证据文件由 storage 使用保护标记防止自动淘汰；清理策略详见 [Storage 规范](./storage-guidelines.md)。
- 清理器按批次执行并支持取消；没有有效保留策略或文件检查器时不删除记录。清理是幂等的，测试需覆盖文件先删、数据库行后删的顺序。

实现/测试：`internal/record/retention.go`、`internal/record/retention_test.go`、`internal/storage/cleaner.go`。

## 5. 验证

```bash
python3 native/scripts/build.py go test -race ./internal/record ./internal/camera ./internal/region
```

涉及跨层生命周期、迁移或原生抓拍改动时，再按项目要求运行 `make check`；具体 Native / SQLite 验证边界以对应层规范为准。
