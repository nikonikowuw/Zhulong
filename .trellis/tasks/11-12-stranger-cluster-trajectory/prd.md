# 陌生人聚类档案与跨摄像头轨迹

## Goal

把 `recognitions` 里**未匹配人脸**（`matched = 0`）的 512 维特征聚类成**稳定档案**（`stranger_clusters`），让每个陌生人获得一个跨相机、跨天不变的 `cluster_id`；再复用既有 `internal/spatial` 轨迹链路回答业务问题：**这个陌生人是同一个人吗、他经过哪些摄像头、每次停留多久**。

### 一句话边界

要的是「**陌生人个体化 + 轨迹还原 + 人工确认转入底库**」；不要「自动认出他是谁」（信息上不可能，只能给出候选簇 + 证据图由人确认）。

## Background & Confirmed Facts

### 已经具备的数据与链路

- **特征向量底座（`11-11-streaming-face-feature-persistence`，迁移 `000034`）**：`recognitions.feature_vector BLOB` 已持久化每张人脸的 512 维 L2 归一化小端 `float32`（2048 B），`matched = 0` 陌生人与 `matched = 1` 命中人员都写；`ListRecognitions` 用 `.Omit("feature_vector")` 保持分页轻量，`GetRecognitionByID` 读全量 BLOB。
- **最新迁移基线**：仓库当前最新迁移为 `000035_add_alert_destination_capabilities`，本任务新增迁移编号必须为 **`000036`**。
- **记录表质量分缺口**：`internal/record/service_build.go:58-81` 组装 `Recognition` 时丢弃了 `aitypes.FaceStructureItem.Quality`（`AttributesJSON` 仅存人体 `personAttrs`）；而 `recognitions.similarity` 在 `matched = 0` 时是与底库最近已知人员的余弦分（底库为空恒为 `0.0`），不能充当画质分。本任务需在 `000036` 补齐 `recognitions.face_quality` 列。
- **空间轨迹链路已就绪**：`GET /api/v1/spatial/trajectory`（`internal/spatial/handler.go:278-299`）→ `Service.QueryTrajectory`（`service.go:282-304`）→ `aggregateDetections`（`service.go:409-457`，已实现 `defaultDwellWindow = 30s` 同相机折叠）与相机标定表 `camera_canvas_positions` 均可直接复用；缺的只是 `targetType=stranger` 分支与 `Store.QueryStrangerRecognitions`。
- **抓拍裁剪与二次提取器边界**：
  - `face_image_path`（`_face.jpg`）由 `native/src/abi/snapshot.cpp:301-312` 按原始人脸框**零外扩、向内 2px 取整**紧裁生成；
  - `faceFeatureExtractorAdapter.ExtractFaceFeature`（`internal/app/adapters.go:403`）直接接收 JPEG/PNG 编码字节（内部解码转 28 B 裸帧头 RGB24，无需调用方转 PPM），其底层 `FaceExtractorContext::extract`（`packages/face_recognition/*/src/pipeline/face_recognition.cpp:664-683`）先跑 SCRFD 全图检测且在检出多张有效人脸时返回 `ERR_AMBIGUOUS`（`MULTIPLE_FACES_DETECTED`）。直接喂零边距 `_face.jpg` 易漏检，直接喂多人全幅 `image_path` 易报多脸歧义——转正时须从全幅图按 `box_json` 外扩裁剪单人特写。

### 现有资源、并发、索引与契约约束

| 项 | 仓库客观事实 |
|---|---|
| 推理消费并发模型 | `internal/camera/stream_hub.go:1189` 按**每路相机独立 goroutine** 并发调用 `record.Service.ProcessInferenceResult`，**并非单写者串行路径**；聚类状态更新必须自行串行化保护 |
| SQLite 连接池 | `MaxOpenConns(1)`（`database-guidelines.md` §4.3），`db.Transaction` 闭包内严禁调用自取连接的 Store 方法，否则永久死锁 |
| 相关索引 | `idx_recognitions_camera_captured_id(camera_id, captured_at DESC, id DESC)`、`idx_recognitions_captured_at_id`（迁移 `000021`）；无 `ANALYZE` 统计时，带 `camera_id` 与 `cluster_id` 双条件的查询可能误选 `camera_captured_id`，需用 `INDEXED BY` 锁定 |
| 异步抓拍时序 | `record.Service.processPersonStructure` 先 `CreateRecognitionBatch` 再异步入队 `SnapshotStore`；若队列满/关闭，`clearJobPath` 会清空 `recognitions` 快照路径，写簇时不能假定图片已落盘 |
| 保留清理路径 | `record.RetentionPruner` 既按 `snapshotsRetentionDays` 做 TTL 清理，也会经 `pending_snapshot_deletes` 在容量淘汰时提前删未过期 `recognitions` 行 |
| 冷却抑制 | `record.DefaultCooldown = 3s`（`cooldown.go:10`），同目标每秒最多约 0.33 条记录 |
| 模型特征阈值锚点 | `packages/face_recognition/*/src/common/match_config.hpp:29,80`（`facelivtv2-l`）：底库默认命中阈值 `0.40`，**同相机 3 s 内连续帧融合阈值 `0.62`**；跨相机/跨天同人余弦相似度落在更低区间，初始阈值定为 `0.55` 并以 AC3 实测分布校准 |
| 人员入册契约 | `person.Service.CreatePerson` + `AddFaceWithReader` 并非单一原子事务（`AddFaceWithReader` 失败会留下 0 人脸 `Person`），且 `RequirePersonIDForEnrollment` 在 `AddFaceWithReader` 校验；`person` 域 JSON `personId` 特指对外业务键（`persons.person_id`） |
| 错误与审计字典门禁 | 业务错误码注册在各领域 `locale.go`（`error-handling.md` §4）；`audit/action_locales_test.go` 强制校验所有 `Action*` 常量存在于 `web/src/features/audit/locales/*.json` |
| 前端路由既有参数 | `web/src/features/records/data/schema.ts:198` 已占用 `view`（`'table' \| 'grid'`）；`web/src/features/spatial/index.tsx:221` 现有深链接硬编码 `'today'` 且等待 `personsData` |

### 规模估算（10 路相机，用于定容量与“要不要 ANN”）

- 记录速率：10 路 × 0.5 条/s ≈ **43 万/天**；每天新增陌生面孔 300–800 人；30 天跨天合并后约 **1–2 万簇**。
- 在线活跃簇内存索引（逐向量动态 scale int8，`516 B/簇`；或精确 `float32`，`2048 B/簇`）：5 千–1 万活跃簇仅 **2.5–20 MB**，RK3568 单次扫描 < 2–4 ms。
- R3 周期合并加载全量未过期 `candidate` 质心（2 万簇 `float32` ≈ 40 MB，每 15 分钟批量计算一次）：线性矩阵扫耗时 < 50 ms。
- **明确不上 ANN**：簇数 < 10 万时内存线性扫描足够，不引入 HNSW/IVF 复杂度。

### 已确认产品与工程决策

1. **簇保留期与空壳清理**：候选簇 `last_seen_at` 超过保留期（默认 `90` 天）标为 `discarded`；当簇内剩余 `recognitions` 行因 `RetentionPruner`（TTL 或容量淘汰）已归零时同步标为 `discarded`；超过 `max(retention_days, snapshotsRetentionDays) + 30d` 且确认 `recognitions` 表已无任何关联成员行的 `discarded` / `merged` 空壳簇，在 R3 巡检中按小批次物理删除，防止长期累积死行。
2. **转正证据过期回落策略**：代表帧图片若已随 TTL/水位清理缺失，按 `face_quality DESC, box_area DESC` 顺延选取簇内快照文件仍存在的次优成员帧；若全簇已无任何可用快照文件，返回 **`409 STRANGER_EVIDENCE_EXPIRED`**（fail-closed，禁止向底库写入无头像幽灵模板）。
3. **前端入口形态**：在记录页「识别记录」下通过独立 URL 参数 `recMode`（`details` / `strangers`，避免碰撞既有表格/网格 `view` 参数）切换「识别明细」与「陌生人档案」聚合卡片，不新增一级侧边栏路由；卡片提供「查看轨迹」深链与「转入底库」操作。
4. **空间轨迹契约扩展**：`GET /api/v1/spatial/trajectory` 的 `targetType` 新增枚举值 **`stranger`**，搭配查询参数 **`clusterId`**（与 `vehicle`+`plate`、`person`+`personId` 三路对称隔离）。

## Requirements

### R1 — 数据模型（迁移 `000036`）与质量分补齐

新增成对迁移 `internal/database/migrations/000036_add_stranger_clusters.{up,down}.sql`：

```sql
CREATE TABLE IF NOT EXISTS stranger_clusters (
    id                       TEXT PRIMARY KEY NOT NULL,                      -- 稳定 ID（ULID），出生即定，永不重编号
    centroid                 BLOB NOT NULL,                                  -- float32[512] 小端，L2 归一化单位向量（2048 B）
    sim_p10                  REAL NOT NULL DEFAULT 0.0,                      -- R3 计算的成员到质心余弦相似度 P10（用于离散度诊断与污染冻结，不作高纯度簇拒收门限）
    member_count             INTEGER NOT NULL DEFAULT 0,
    camera_count             INTEGER NOT NULL DEFAULT 1,                     -- 经过的不重复相机数（派生缓存，免除列表 COUNT(DISTINCT) 扫表）
    first_seen_at            DATETIME NOT NULL,
    last_seen_at             DATETIME NOT NULL,
    representative_id        TEXT,                                           -- 代表帧 recognitions.id（逻辑弱引用）
    representative_face_path TEXT NOT NULL DEFAULT '',                       -- 代表帧裁剪图路径缓存（避免列表 JOIN recognitions）
    person_id                TEXT REFERENCES persons(id) ON DELETE SET NULL, -- 转正后关联的内部人员主键
    person_name              TEXT NOT NULL DEFAULT '',                       -- 转正时人员姓名快照
    merged_into_id           TEXT,                                           -- status='merged' 时指向合并目标老簇 ID
    status                   TEXT NOT NULL DEFAULT 'candidate',              -- candidate|confirmed|merged|discarded
    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_stranger_clusters_status_last_seen
    ON stranger_clusters(status, last_seen_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS stranger_cluster_merges (
    from_id   TEXT PRIMARY KEY NOT NULL,
    to_id     TEXT NOT NULL,
    merged_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_stranger_cluster_merges_to
    ON stranger_cluster_merges(to_id);

ALTER TABLE recognitions ADD COLUMN face_quality REAL NOT NULL DEFAULT 0.0;
-- 故意不加 REFERENCES 外键：避免簇合并或空壳清理时在千万行 recognitions 上触发全表扫描锁
ALTER TABLE recognitions ADD COLUMN cluster_id TEXT;

CREATE INDEX IF NOT EXISTS idx_recognitions_cluster_captured_id
    ON recognitions(cluster_id, captured_at DESC, id DESC)
    WHERE cluster_id IS NOT NULL;

-- R3 补偿扫描专用部分索引：常态下仅包含失败回退的未归簇行（接近 0 行），避免扫描 24h 全表 40 万+行
CREATE INDEX IF NOT EXISTS idx_recognitions_unclustered_captured
    ON recognitions(captured_at ASC, id ASC)
    WHERE matched = 0 AND cluster_id IS NULL AND feature_vector IS NOT NULL;
```

- `down.sql` 严格按依赖逆序回滚：先 `DROP INDEX idx_recognitions_unclustered_captured` 与 `DROP INDEX idx_recognitions_cluster_captured_id` → `ALTER TABLE recognitions DROP COLUMN cluster_id` 与 `face_quality` → 再删两张簇表及其索引。
- `internal/record/service_build.go` 的 `buildRecognitionRecordAndJob` 同步将有限值 `face.Quality`（非 `NaN`/`Inf`，负值归零）写入 `Recognition.FaceQuality`。
- `recognitions.feature_vector` 保持权威证据地位：聚类**只写 `cluster_id`，永不改写原始向量、相机与抓拍时间**；质心是可从成员重建的派生索引。

### R2 — 在线赋值（多路并发安全 + 单次批写）

- **模块边界**：新增独立业务包 `internal/stranger`；`record.Service` 通过 setter 注入窄接口 `ClusterAssigner`（与 `SnapshotStore` / `AlarmListener` 同构），`stranger` 与 `person` / `mediafile` / `storage` 之间全部通过窄接口在 `internal/app` 装配，严禁包间循环依赖。
- **并发、锁序与事务安全**：
  - 赋值器内部通过 `mu sync.Mutex` 串行化内存活跃簇状态与在线簇分配，承受多路相机 `ProcessInferenceResult` 并发调用，`-race` 零竞争；
  - **锁序铁律**：锁序恒为 `ClusterAssigner.mu -> DB`；`db.Transaction` 闭包内严禁反向获取 `mu`，且闭包内只使用传入的 `tx`，严禁重入获取连接触发 `MaxOpenConns=1` 死锁；
  - 在 `record.Service` 批量入库阶段，先为本批 `matched = 0` 且通过校验的有效 2048 B `FeatureVector` 记录完成簇分配与簇表持久化，将得到的 `ClusterID` 直接填入 `Recognition.ClusterID` 随 `CreateRecognitionBatch` **一次性 INSERT**（避免落库后再逐行 `UPDATE`）；
  - 对既有簇的持久化更新必须带状态保护 **`WHERE id = ? AND status = 'candidate'`**，防止与并发转正或合并冲突；
  - **代表帧异步安全**：仅当抓拍通道可用（`SnapshotStore != nil && SnapshotStore.Available()`）时才在在线建簇/更新时记录候选 `representative_face_path`（若抓拍后续被队列丢弃，由 R3 保活巡检修正，前端同时具备裂图占位容错）；
  - **Fail-open**：若簇分配或簇表写库失败，记录 Warn 日志并将 `Recognition.ClusterID` 置 `nil` 后照常执行 `CreateRecognitionBatch`，绝不阻断识别记录落库（漏赋值的行由 R3 补偿扫描补齐）。
- **向量校验、冷启动恢复与比对判据**：
  - 向量有效性门禁：长度必须为 `2048` 字节（512 × `float32` LE），且所有维度均为有限值（非 `NaN`/`Inf`）并满足 `‖v‖₂ > 1e-6`（先内部归一化后再参与点积与质心融合），拦截损坏/全零向量；
  - 在线候选集：内存维护最近 `active_window`（默认 `7d`，可配置上限保护如最多 `MaxActiveClusters = 10000` 按 `last_seen_at DESC`）内 `status = 'candidate'` 活跃簇；**进程启动时除加载簇行外，必须分批查询 `SELECT DISTINCT cluster_id, camera_id FROM recognitions WHERE cluster_id IN (...)` 恢复各活跃簇的 `seenCameras` 集合**，防止重启后同相机重抓重复累加 `camera_count`；
  - 若使用 int8 加速粗筛，必须采用**逐向量对称动态量化**（`scale = max(|x_i|) / 127`，`int8[512] + float32 scale`），粗筛 Top-K 后与精确 L2 归一化 `float32` 质心复核点积；
  - 命中条件：最高质心余弦相似度 `sim ≥ θ_assign`（默认 `0.55`）；命中则以有界样本权重 `w = min(member_count, 32)` 按 `centroid = normalize(w * centroid + x)` 更新质心并刷新 `last_seen_at`、`member_count`、`camera_count` 及更优代表帧（若该簇 `sim_p10 > 0 && sim_p10 < θ_assign - 0.10`，则判定簇已离散污染，仅归入成员而不更新质心以防继续漂移；绝不使用 `sim >= sim_p10 - 0.05` 拒收跨视角同人）；否则生成新 ULID 簇。
- **配置项**：`config.toml`、`cmd/Zhulong/config.go`（严格 `UnmarshalExact`）与 `internal/app/config.go`（`DefaultConfig` + 范围校验）新增 `[stranger]` 段：
  - `enabled`（默认 `true`）
  - `assign_threshold`（默认 `0.55`，范围 `(0, 1]`）
  - `merge_threshold`（默认 `0.58`，范围 `(0, 1]` 且 `>= assign_threshold - 0.05`）
  - `active_window`（默认 `"7d"`，支持 Go duration 与 `"d"` 天后缀，范围 `1h..720h`）
  - `retention_days`（默认 `90`，范围 `1..730`）

### R3 — 周期整合（补偿扫描 / 跨天稀疏合并 / 代表帧保活 / 退役清理）

- **调度与生命周期**：后台单实例定时任务（默认每 15 min），接入 `internal/app` 的 `lifecycleRuntime`（`stopSequence`）优雅取消与排空，不挂无序 Fx 钩子；重度 SQLite 读/写分批进行、批间主动让渡（20 ms），且**重度 DB I/O 期间绝不长期持有 `ClusterAssigner.mu`**（仅在最终原子切换内存索引时短持锁）。
- **补偿扫描**：依托 `idx_recognitions_unclustered_captured` 部分索引扫描最近 `active_window` 内 `matched = 0 AND cluster_id IS NULL AND feature_vector IS NOT NULL` 的记录小批补做归簇，严禁全表扫描历史行。
- **跨天稀疏成员级合并（防 BLOB I/O 爆炸与防链式粘连）**：
  - 从 `stranger_clusters` 加载全部未过期 `status = 'candidate'` 簇质心（覆盖跨天周期，而不限于内存热窗口），以质心余弦筛出 `centroid_sim ≥ θ_merge - 0.05` 的稀疏候选对；
  - 仅按主键读取候选簇各自 Top-K（按 `face_quality DESC`，每簇最多 16 条）成员的 `feature_vector` 做成员级核验：要求至少 2 对跨簇成员相似度 `≥ θ_merge`（单帧簇退化为该唯一帧与对方至少 2 名成员 `≥ θ_merge + 0.03`），且合并后双方代表成员的最小交叉相似度不低于防粘连下界（`θ_assign - 0.05`）；
  - 合并保留 `first_seen_at` 较早（同时间取 ULID 较小）的老簇为 `to_id`：写入 `stranger_cluster_merges(from_id, to_id)` 并将历史上 `stranger_cluster_merges.to_id = from_id` 与 `stranger_clusters.merged_into_id = from_id` 一并扁平更新至 `to_id`；被并簇置 `status = 'merged'`、`merged_into_id = to_id`；分批将成员 `recognitions.cluster_id` 改写为 `to_id`，从成员重算 L2 归一化质心、`sim_p10`、`member_count` 与 `camera_count`；最后通知 `ClusterAssigner` 从内存驱逐 `from_id` 并更新 `to_id`（若在活跃窗内）。
- **代表帧选举、保活与孤儿簇收敛**：按 `face_quality` 为主、归一化人脸框面积为辅挑选最优且快照文件（`SnapshotFileChecker`）仍存在的成员帧，更新 `representative_id` 与 `representative_face_path`；当原代表帧随抓拍 TTL / 容量淘汰失效时自动降级至次优存活帧，若全簇快照均已清理则清空 `representative_face_path`；**若校验发现簇内现存 `recognitions` 行数已为 `0`（已被 `RetentionPruner` 清理光）**，同步将该 `candidate` 簇标为 `discarded`（归零 `member_count`/`camera_count` 并清空代表帧字段）且从在线内存驱逐，杜绝幽灵档案。
- **退役与空壳物理清理**：
  - `last_seen_at` 早于 `retention_days`（默认 90 天）的 `candidate` 簇标为 `discarded` 并从在线内存驱逐；
  - `last_seen_at` 早于 `max(retention_days, snapshotsRetentionDays) + 30d` 的 `discarded` / `merged` 簇，在校验 `EXISTS (SELECT 1 FROM recognitions WHERE cluster_id = ?)` 为假后分批物理删除（同步清理对应的 `stranger_cluster_merges` 记录）。
- **明确禁止**：夜间全量重新聚类（防止簇 ID 漂移打断用户确认与外链）。

### R4 — 轨迹查询扩展（复用 `internal/spatial`）

- `GET /api/v1/spatial/trajectory` 的 `TrajectoryQuery` 新增 `targetType=stranger` 枚举值与 `clusterId` 查询参数（缺失返回 `422 CLUSTER_ID_REQUIRED`、非合法 ULID 返回 `422 INVALID_CLUSTER_ID`；同步更新 `internal/spatial/locale.go` 中 `INVALID_TARGET_TYPE` 与新增错误码三语文案）。
- 若请求的 `clusterId` 在 `stranger_clusters` 中处于 `status = 'merged'` 且 `merged_into_id` 非空，在服务端按有界跳数（`maxHops = 8`）沿合并链解析至最终 `to_id` 查询（簇不存在返回 `404 STRANGER_CLUSTER_NOT_FOUND` 或空节点需与 `person` 一致，Swagger 明确说明：簇不存在返回 `404 STRANGER_CLUSTER_NOT_FOUND`）。
- 新增 `Store.QueryStrangerRecognitions(ctx, filter)`：
  `FROM recognitions INDEXED BY idx_recognitions_cluster_captured_id WHERE cluster_id = ? AND matched = 0 AND camera_id IN (?) AND captured_at >= ? AND captured_at <= ? ORDER BY captured_at ASC`
  显式 `INDEXED BY` 保证即使地图仅标定 1 台相机也不会误选 `idx_recognitions_camera_captured_id`，`EXPLAIN QUERY PLAN` 严禁出现 `TEMP B-TREE`。
- 完整复用 `aggregateDetections` 的 dwell window 同相机折叠与未标定相机跳过逻辑；时间窗必填，倒序/空窗按现有 `INVALID_TIME_RANGE` 返回。

### R5 — 查询 API

遵循 `.trellis/spec/backend/http-api-guidelines.md`（`*Request` / `*Response` / `*Info`，禁用 `*DTO`），统一走 `httputil` 信封，同步把 `./internal/stranger` 加入 `Makefile` 的 `api-docs --dir` 白名单，并在 **`internal/stranger/locale.go`**（模块内 `init()` 注册 `httputil.RegisterMessages`，不修改 `internal/httputil/locale.go`）注册三语错误文案：

| 端点 | 用途与契约 |
|---|---|
| `GET /api/v1/strangers` | 簇分页列表：默认 `status=candidate`，支持 `?status=candidate\|confirmed\|merged\|discarded`、`?minMemberCount=`；返回 `id / firstSeenAt / lastSeenAt / memberCount / cameraCount / representativeId / representativeFacePath / status / personId / personName / mergedIntoId`；严禁读取 `centroid` BLOB |
| `GET /api/v1/strangers/:id` | 簇详情：上述元数据（不含 `centroid`）+ 经过的相机统计列表 `cameras: []StrangerCameraStatsInfo`（`cameraId / cameraName / firstSeenAt / lastSeenAt / count`；单簇只读 `(camera_id, captured_at)` 后在 Go 侧聚合，避免 SQL `GROUP BY` 触发 `TEMP B-TREE`） |
| `GET /api/v1/strangers/:id/recognitions` | 簇成员帧分页：若簇处于 `merged` 状态，透明解析至最终 `merged_into_id` 后返回分页结果；复用 `RecognitionResponse` 投影（增补 `faceQuality` 与 `clusterId`），`.Omit("feature_vector")` |
| `GET /api/v1/records/recognitions?clusterId=` | 既有识别记录列表扩展可选 `clusterId` 过滤参数（`clusterId` 非空时强制使用 `INDEXED BY idx_recognitions_cluster_captured_id`，防止与 `cameraId` 组合时退化；保持 `.Omit("feature_vector")` 且无 `TEMP B-TREE`） |

- `:id` / `clusterId` 非合法 ULID 返回 `422`；簇不存在返回 `404 STRANGER_CLUSTER_NOT_FOUND`。

### R6 — 转正（人工确认转入人员底库）

- `POST /api/v1/strangers/:id/promote`，支持二选一明确语义（同时传或都不传返回 `422`）：
  1. **新建人员并入册**：`{ "name": "张三", "personId": "EMP-001" }`（与 `person` 域一致，`personId` 为可选**对外业务键**；宿主开启 `[person].require_person_id_for_face_enrollment = true` 且未传 `personId` 时**在创建 `Person` 前**返回 `409 PERSON_ID_REQUIRED_FOR_ENROLLMENT`）；
  2. **追加到已有人员**：`{ "targetPersonId": "<内部 persons.id>" }`（受 `person.MaxFacesPerPerson = 10` 约束，超限返回对应冲突错误）。
- **原子性、补偿与并发防护**：
  - 通过 per-cluster `inFlight` 互斥锁防止同一簇并发双点转正（不阻塞其它簇或在线聚类）；
  - 新建人员路径：若 `CreatePerson` 成功后人脸提取/入册（`AddFaceWithReader`）因任何原因失败（含 `NO_FACE_DETECTED` / `MULTIPLE_FACES_DETECTED`），**必须立即补偿删除该新建的 0 人脸 `Person`**，严禁留下幽灵人员导致重试 `personId` 冲突；
  - 入册成功后，以条件更新 **`UPDATE stranger_clusters SET status='confirmed', person_id=?, person_name=?, updated_at=? WHERE id=? AND status='candidate'`** 确认状态（校验 `RowsAffected == 1`），并**立即通知 `ClusterAssigner.Evict(id)` 从内存活跃簇移除该簇**；后续该人员新抓拍由识别链路正常产出 `matched = 1`；
  - 若已关联 `person` 后续在人员管理中被删除，因外键 `ON DELETE SET NULL`，簇保持 `status = 'confirmed'`、`personId = null`、`personName` 快照不变，前端需正常展示。
- **证据图挑选与外扩裁剪**：
  - 优先取 `representative_id` 对应识别记录；若记录或图片文件已不存在，按 `face_quality DESC` 回落到簇内仍存在快照文件的次优成员帧；若全簇无任何可用图片文件，返回 **`409 STRANGER_EVIDENCE_EXPIRED`**；
  - 优先读取该帧全幅图 `image_path` 与 `box_json` `[x1, y1, x2, y2]`：当框有效（`bw > 0 && bh > 0`）时，以框中心向四周各外扩 `0.60 × bw` / `0.60 × bh`（总裁剪框宽高 `2.2×`，clamp 至画面边界 `[0, 1]`）裁剪出单人特写 JPEG，避免紧裁 `_face.jpg` 触发 `NO_FACE_DETECTED`、也避免多人全幅图触发 `MULTIPLE_FACES_DETECTED`；若紧邻双人仍检出多脸，透传 `422 MULTIPLE_FACES_DETECTED` 由人工改选；当全幅图缺失或 `box_json` 退化且 `face_image_path` 存在时，对 `face_image_path` 四周补纯色中性边（扩展至 2.2×）兜底送入；
  - 经 `internal/stranger` 窄接口调用 `person` 域标准入册通道完成**单次** `ExtractFaceFeature`、写入 `facesDir` 与 `person_faces`、并经 `MSG_GALLERY_UPSERT` 实时广播到算法 Runner。
- **状态校验与审计**：
  - 对已 `confirmed` 的簇重复调用返回 `409 STRANGER_ALREADY_PROMOTED`；对 `merged` / `discarded` 簇调用返回 `409 STRANGER_INVALID_STATE`；
  - 在 `internal/stranger/model.go` 定义 `ActionStrangerPromote = "stranger.promote"`，在 HTTP Handler 层记录操作审计日志（遵循 `audit` 规范不在 Service 层引 Gin/审计包），并同步补齐 `web/src/features/audit/locales/{en,zh-Hans,zh-Hant}.json` 中 `actionNames["stranger.promote"]` 词条。

### R7 — 实时广播与隐私隔离

- 在线簇赋值位于持久化路径，**绝不回填**到已广播的实时预览帧：SSE `event: ai_result` 载荷保持现状（`aitypes.InferenceResult`，无 `cluster_id`、无向量）。
- 所有 HTTP 响应严禁暴露 `stranger_clusters.centroid` 与 `recognitions.feature_vector` 二进制特征。
- 本期不做实时簇事件流，前端全部走 REST 查询。

### R8 — 最小前端（`web/`）

- **记录页（`src/features/records`）**：在「识别记录」标签页增加视图模式切换（「识别明细」/「陌生人档案」），使用独立 URL Search 参数 **`recMode`**（`'details' | 'strangers'`，不复用既有控制表格/卡片布局的 `view` 参数）；档案卡片展示代表帧缩略图（含文件缺失占位容错）、首次/最近出现时间、抓拍次数、经过相机数，支持展开成员帧、点击「查看轨迹」跳转 `/spatial?targetType=stranger&clusterId=<id>&timeRange=7d`（或覆盖簇时间的合理范围）、点击「转入底库」弹窗（新建人员或通过 `@/features/persons/contract` `useAllPersons` 选择已有人员）。
- **空间页（`src/features/spatial`）**：`TrajectorySearchToolbar` 与 `spatialSearchSchema` 目标类型新增 `'stranger'` 选项（输入 `clusterId`），并在 `Spatial` 页面支持 `?targetType=stranger&clusterId=<id>` 深链自动检索（不等待 `personsData`，且支持 URL 携带或默认 `7d` 时间窗，防止昨天/跨天陌生人按 `today` 查出空轨迹）与画布轨迹回放（复用既有 `SpatialCanvasViewport`）。
- **前端规范门禁**：全部新增文案覆盖 `en` / `zh-Hans` / `zh-Hant` 三语字典（含 `records`、`spatial` 及 `audit` 的 `stranger.promote`），API 输入经 Zod 校验，具备完备 Loading / Error / Empty 三态，遵循 OKLCH 语义色与 RTL 逻辑边距。

## Acceptance Criteria

- [ ] **AC1（迁移与查询计划）**：`000036` up/down 可逆；`idx_recognitions_cluster_captured_id`、`idx_recognitions_unclustered_captured` 与 `idx_stranger_clusters_status_last_seen` 生效；簇分页、簇成员分页、`ListRecognitions(clusterId)`（含同时传 `cameraId` 组合）、R3 补偿扫描与陌生人轨迹查询（含单相机地图）的 `EXPLAIN QUERY PLAN` 均**无 `TEMP B-TREE` 与全表扫描**；列表与详情接口均不加载 `feature_vector` / `centroid` BLOB。
- [ ] **AC2（并发与在线赋值正确性）**：`-race` 测试覆盖——多路相机并发 `ProcessInferenceResult` 无数据竞争；同人近邻向量归入同一簇且质心保持单位模长（`‖c‖₂ ≈ 1.0`）；正交向量（`sim ≈ 0`）必成 2 簇；`feature_vector = NULL` / 全零 / `NaN` 记录安全跳过且不污染质心；模拟簇写库失败时 `recognitions` 行仍成功落库（`cluster_id IS NULL`）并可被 R3 补偿扫描补齐；重启后内存活跃簇及 `seenCameras` 集合从 SQLite 恢复并正确维护 `camera_count`；高纯度单相机簇（10+ 帧）跨相机（`sim = 0.65`）不被错误拒收。
- [ ] **AC3（阈值校准工具与实测报告）**：提供只读校准测试/工具（不改库），支持对真实 SQLite `recognitions.feature_vector` 样本计算同轨迹/跨相机/异类余弦相似度分布，输出候选 `θ_assign` / `θ_merge` 下的预估簇数与误并率；报告明确区分实测数据与合成数据，并注明模型名（如 `facelivtv2-l`）与量化后端。
- [ ] **AC4（跨相机轨迹与合并重定向）**：同一簇在 ≥ 2 台标定相机的记录（含跨天出现由 R3 合并的簇），经 `GET /api/v1/spatial/trajectory?targetType=stranger&clusterId=...` 返回按时间升序节点，跨相机不折叠、同相机 30s 内按 dwell window 折叠、未标定相机自动跳过；被 R3 合并的旧 `from_id` 查询轨迹与 `:id/recognitions` 均可正确解析到合并后的 `to_id`。
- [ ] **AC5（转正闭环与边界防护）**：转正（外扩裁剪路径）成功后 `person_faces` 新增 2048 B 特征、`MSG_GALLERY_UPSERT` 已广播、簇置 `status = 'confirmed'` 并记录 `person_id`、立即从内存活跃簇驱逐并从 `GET /api/v1/strangers` 默认列表消失、写 `stranger.promote` 审计日志且 `TestAuditActionLocalesCoverBackendActions` 通过；若人脸提取失败不留 0 人脸幽灵 `Person`；并发转正同一簇仅成功一次；代表帧文件缺失时自动回落次优存活帧，全簇图片过期缺失时返回 `409 STRANGER_EVIDENCE_EXPIRED`；重复转正返回 `409 STRANGER_ALREADY_PROMOTED`。
- [ ] **AC6（性能）**：5 千活跃簇规模下在线赋值单次比对 P95 < 2 ms；24 h 时间窗单簇轨迹查询 P95 < 200 ms；测试报告注明运行硬件（Host CPU 或 RK3568 实测）、活跃簇数与成员行数。
- [ ] **AC7（全仓门禁与文档同步）**：`Makefile` `api-docs --dir` 已加入 `./internal/stranger`；`make pkg-drift-check`、`make native-test`、`make api-docs`、`make frontend-build`（同步 `internal/webui/dist`）与 `make check` 全部通过；新增契约同步更新至 `.trellis/spec/backend/record-guidelines.md` 与 `.trellis/spec/backend/directory-structure.md`。

## Out of Scope

- **ANN / HNSW / IVF 向量索引**：簇数 < 10 万时无收益，明确不做。
- **人体 ReID**：本任务仅使用人脸 512 维特征，不覆盖无正脸的背身/俯拍跨镜跟踪。
- **跨镜实时接力预测**、**夜间全量重聚类**、**前端手动合并/拆分簇图形编辑器**、**陌生人热力图**。
- **以图搜陌生人簇**：上传图片检索 `stranger_clusters` 留作后续独立任务（本期 `SearchByImage` 仍只搜 `person_faces`）。
- 不修改 `captures` / `alarms` 表结构，不改实时 SSE `ai_result` 载荷契约。

## Risks

| 风险 | 处置 |
|---|---|
| 阈值偏高致同人碎片化、偏低致**误并污染档案** | 默认值锚定 `facelivtv2-l` 特征分布（`0.55`），上线前用 AC3 在现场库跑 dry-run 校准；R3 合并面向全量未过期候选簇并强制多对成员级证据 + 防粘连下界，保留 `stranger_cluster_merges` 审计轨迹 |
| 尾部侧脸帧把质心拽偏（搭桥误并）或 `NaN`/零向量毒化质心 | 质心采用有界样本均值（`w = min(n, 32)`）+ 每次重做 L2 归一化，入簇校验有限非零向量，`sim_p10` 跌破污染下界时冻结质心更新，R3 合并后从成员精确重算 |
| 转正时紧裁人脸图二次检测失败，或失败后残留孤儿人员 | 从全幅 `image_path` 按 `box_json` 外扩 60%/边（2.2×）裁剪单人特写送检；失败自动补偿删除新建的 0 人脸 `Person`；快照均过期时显式 `409` 拒绝无图入库 |
| 多路流并发与 `MaxOpenConns=1` 锁争用 | `ClusterAssigner.mu` 仅保护内存态与短库写，严守 `mu -> DB` 锁序、事务内零重入查询；R3 BLOB 读/改写分批让渡，不在重度 I/O 期间长持 `mu` |
| SQLite 查询计划误选索引或扫描全表 | 为 R3 补偿扫描建专用部分索引；单簇/轨迹按 `cluster_id` 查时显式 `INDEXED BY idx_recognitions_cluster_captured_id`，单测锁定 `EXPLAIN QUERY PLAN` |
