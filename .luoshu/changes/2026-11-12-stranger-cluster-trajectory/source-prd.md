# 陌生人疑似人员档案与跨摄像头轨迹

## Goal

把 `recognitions` 里**未匹配人脸**（`matched = 0`）的 512 维特征聚成**疑似人员档案**（`stranger_clusters`），让同一个陌生人的多次到访获得一个跨相机、跨天稳定的 `cluster_id`；再复用既有 `internal/spatial` 轨迹链路回答三个业务问题：**这个人来过几次、走过哪几台相机、每次的抓拍和录像在哪**。

### 产品承诺（本次唯一需要产品侧确认的改动）

> 对**反复出现且正脸可辨**的陌生人，系统把其到访串成一个档案：出现频次、首次/最近时间、涉及相机、每次抓拍与可回放轨迹。
> **一次路过、背身、夜间、远景的抓拍，只作为单条识别记录保留，不参与身份聚合。**

这条承诺是本次交付的验收基线。原 PRD 隐含的「识别每一个陌生人」在任何阈值下都不可达：`packages/face_recognition/onnx/src/common/match_config.hpp:93-99` 明确写着「异人余弦通常接近 0，而同人侧脸/模糊帧常落在 0.4~0.6」，说明同人困难帧与人脸分布的**可分边界在 0.3~0.4 一带**，把线画在 0.55 等于画在同人分布中段——精度收益极小、召回损失过半。

### 一句话边界

要的是「**疑似个体化 + 轨迹还原 + 人工确认与纠错 + 转入底库**」；不要「自动认出他是谁」。
**`cluster_id` 是聚类编号（logical archive number），不是身份。** 它随时可能被合并或拆分，UI 与 API 文案必须体现"疑似"，禁止表述为"某个人"。

## Background & Confirmed Facts

### 已具备的底座

- **特征向量**：`recognitions.feature_vector BLOB`（迁移 `000034`）已持久化每张人脸的 512 维 L2 归一化小端 `float32`（2048 B），`matched = 0/1` 都写；`ListRecognitions` 用 `.Omit("feature_vector")` 保持分页轻量，`GetRecognitionByID` 读全量。
- **迁移基线**：仓库当前最新迁移为 **`000038_create_event_clips`**，本任务新增迁移编号为 **`000039`**（详见下方勘误表）。
- **质量分已现成，只是没落库**：`packages/face_recognition/onnx/src/common/face_quality.hpp` 是纯几何质量评估器，输出 `quality_score ∈ [0,1]`（`yaw_score` / `pitch_score` / `aspect_score` / `size_score` 四分量），文件头自述「算力防护卫士：在昂贵的 512 维特征抽取前置门禁拦截低质目标，杜绝误识和算力浪费」。该分数经 `aitypes.FaceStructureItem.Quality`（`internal/aitypes/model.go:104`）到达 Go 层，但组装 `Recognition` 时被丢弃（`internal/record/service_build.go:62-87` 无对应字段）。插件侧门禁默认 `kDefaultMinFaceQuality = 0.28f`（`match_config.hpp:56`，消费点 `pipeline/face_recognition.cpp:322` 与 `:1037`）——那是**识别**路径的门槛（宁可放过低质帧也不漏人），**聚档路径的门槛必须显著更高**。
- **轨迹身份可信**：`recognitions.track_id` 由宿主维护，算法载荷里的 `track_id` 被显式忽略（`internal/record/service.go:1047` `debugIgnoredTrackID`，日志 "payload track_id ignored; host maintains exclusive tracking identities"；`trackIDAt` `:441`、`resolveIdentities` `:449`/`:463` 以 `(camera, algorithm, kind)` 为键做关联）；抓拍冷却同样按 `(camera_id, track_id)` 收敛（`internal/record/cooldown_keys.go:22`、`:31`）。**因此同一 `(camera_id, track_id)` 在轨迹存活期内是同一物理人，可直接作为零误判的聚合单位。**
- **空间轨迹链路已就绪**：`GET /api/v1/spatial/trajectory`（`internal/spatial/handler.go:278-295` godoc，函数体 `:296`）→ `Service.QueryTrajectory`（`internal/spatial/service.go:287-310`）→ `aggregateDetections`（`internal/spatial/service.go:410`，`defaultDwellWindow = 30s` 于 `:31`）；相机标定表 `camera_canvas_positions` 可直接复用。缺的只是 `targetType=stranger` 分支与 `Store.QueryStrangerRecognitions`。
- **转正链路**：`person.MaxFacesPerPerson = 10`（`internal/person/service.go:40`），限额在单事务内校验（`internal/person/store.go:552-597`）；`RequirePersonIDForEnrollment` 位于 `AddFaceWithReader`（`internal/person/service.go:736`，校验 `:750-753`）；`CreatePerson`（`:262-294`）与入册**不在同一事务**，入册失败会留下 0 人脸 `Person`。
- **运维前置条件**：算法包配置项 `filter_unmatched`（`packages/face_recognition/onnx/config.schema.json`，描述"开启后仅记录已登记人员，未比中人员不产生抓拍和记录"）必须为 `false`，否则本功能无数据。需在 `GET /api/v1/strangers` 空列表的 EMPTY 态提示中检测并说明该配置。

### 锚点勘误（原文引用位置与实际不符，实现时以本表为准）

| 原文锚点 | 实际位置 | 说明 |
|---|---|---|
| 「最新迁移 `000035`，新编号 `000036`」 | 最新为 `000038_create_event_clips`，新编号 **`000039`** | `000036_add_landmarks_to_recognitions` / `000037_add_landmarks_to_captures` 已占用 |
| `stream_hub.go:1189` 并发调用 | `internal/camera/stream_hub.go:1365`（`consumeAIResults` 定义于 `:1286`，协程在 `:845` 起） | `:1189` 是 `if !enabled || preMs <= 0` |
| `service_build.go:58-81` | `internal/record/service_build.go:62-87`；组装函数 `buildRecognitionRecordAndJob` 定义于 `:40-46` | |
| `internal/app/adapters.go:403` 提取器 | `:445`（类型定义 `:441`） | `:403` 是 `supervisorGallerySyncAdapter.GetGallerySyncData` |
| `face_recognition.cpp:664-683` | `FaceExtractorContext::extract` 定义于 `:909`；多脸门禁 `:967-977`（`embeddable_faces > 1` → `ZHULONG_PLUGIN_ERR_AMBIGUOUS`） | |
| `snapshot.cpp:301-312`「零外扩、向内 2px 取整」 | `native/src/abi/snapshot.cpp:310-335` → `native/src/nodes/snapshot/jpeg_encoder.cpp:214-251`；`floor_even(v) = v & ~1`（`:38-40`）、`ceil_even(v) = (v+1) & ~1`（`:43-45`）为**向外偶数对齐**，抖动 ≤2 px | `:301-312` 是全幅编码回退分支 |
| `match_config.hpp:29,80` | `:28` `kDefaultMatchThreshold = 0.40f`；`:83` `kDefaultFusionSimilarityThreshold = 0.62f`；`:84` `kMinFusionSimilarityThreshold = 0.45f`；`:56` `kDefaultMinFaceQuality = 0.28f` | |
| `spatial/service.go:282-304` | `:287-310` | |
| `spatial/service.go:409-457` | `aggregateDetections` 定义于 `:410` | |
| `schema.ts:198` 已占用 `view` | 实际在 `:244`（`recordsSearchSchema` 为 `242-263`） | 该文件所有字段均带 `.catch()`，非法值静默降级 |
| `spatial/index.tsx:221` 深链接、等待 `personsData` | 实际 `:151-172`，硬编码 `resolveTimeRange('today')` 在 `:159`；`personsData` 已迁至 `components/toolbar/trajectory-search-toolbar.tsx:76` | 结构性结论（时间窗硬编码为 today）仍成立 |
| `internal/spatial/requests.go` 未提及 | `:37` `TargetType string \`form:"targetType" binding:"required,oneof=vehicle person"\`` | **必改**：不改 `oneof` 时 gin binding 会在进 Service 前直接 422，`INVALID_TARGET_TYPE` 分支永远走不到 |

### 工业界对照（用于校准产品形态，不作为实现依据）

#### 商用产品

- 海康威视把该能力称 **人员档案 / 路人档案（Personnel Archive / Passerby Archive）**：抓拍图与名单库比对，命中入**实名档案**，未命中入**路人档案**；档案记录**出现频次、出现时长、抓拍图**，每人一个独立档案。客户端支持**用一张人脸图检索相似档案**，并支持把陌生人抓拍**一键加入指定人脸分组**；平台侧支持展示陌生人的**抓拍事件路线**。[[1]](https://enpinfo.hikvision.com/unzip/20200804112255_76519_doc/GUID-F4F7BFEE-A817-459C-8652-9C06053C9B41.html) [[2]](https://enpinfo.hikvision.com/hkwsen/unzip/20230830144955_12918_doc/GUID-B952E00C-75C8-4693-9604-0A0FD65D4BCB.html)
- 专利 `CN110750670B`（陌生人监控方法）给出关键架构取舍：**「陌生人底库中只存储其一张或少数几张人脸图像，但在集合中可以具有其多张人脸图像」**，理由是「避免在底库中存储过多或质量较低的人脸图像而影响系统性能或降低人脸识别准确率」。同时明确「档案分裂」（同人分到多档）与「杂类」（不同人混入同档）是固有缺陷，故整合是主干操作。[[3]](https://patents.google.com/patent/CN110750670B/zh)
- 阿里云 IMM 人脸聚类给出可直接参照的入档门槛：同一人物 ≥3 张，其中**至少 3 张**满足人脸尺寸 >75×75 px、HeadPose 三分量绝对值 <30°、`FaceQuality > 0.8`；且「分组生成后，不符合上述高清人脸标准的图片，仍有可能被划分为同一组人脸」。[[4]](https://help.aliyun.com/zh/imm/user-guide/face-clustering-3)

#### 开源实现（只借结构，不借代码——理由见 Out of Scope）

| 实现 | 已验证的机制 | 落到本设计的对应项 |
|---|---|---|
| **Immich** [[5]](https://github.com/immich-app/immich/blob/a838167f/docs/docs/features/facial-recognition.md) | 自述「DBSCAN 的变体」：新点先寻找已归属的近邻并跟随其所属 person；无归属近邻则判定自己是否 **core point**（半径内 ≥N 张，默认 3，对应「Minimum Recognized Faces」）；不足则挂起为 outlier，**由 nightly job 对残留补扫**；并明确「**队列越大，聚类结果越好**」「对全量重跑时才退化为标准 DBSCAN」 | `R3` 在线赋值 + `R4` 补偿扫描与之同构；「队列越大越好」是 `consolidate_interval` 不宜取得过短（15 min，非 1 min）的依据 |
| **PhotoPrism** [[6]](https://docs.photoprism.app/developer-guide/vision/face-recognition) | 把聚类拆成**四个互不替代的参数**：`FACE_CLUSTER_CORE`（成核所需人脸数，默认 4）、`FACE_CLUSTER_DIST`（成核距离 0.64）、`FACE_MATCH_DIST`（归入已有簇距离 0.46，**比成核更严**）、`FACE_CLUSTER_SIZE`/`_SCORE`（最小尺寸与最小质量分）；**第二遍聚类只处理第一遍的残留**，且明确「与其降低全局 `CORE`，不如对残留另用更低成核门槛——降低全局门槛会把照片多的人拆碎」；`markers.embed_detail < 100`（即向量依赖插值放大像素）**一律不参与聚类** | `R2` 的「建档门禁 / 归属不设下限」解耦；`R4` 补偿扫描只扫残留；**论证了为什么不能靠调低 `min_member_count` 提升召回**；「不许用插值像素成簇」= 本设计的质量门禁同义 |
| **dlib / learn-to-cluster** [[7]](https://github.com/davisking/dlib/blob/master/python_examples/face_clustering.py) [[8]](https://github.com/yl-1993/learn-to-cluster) | `chinese_whispers_clustering(descriptors, 0.5)` 是「离线全量 + 无需预设簇数」的代表；MS-Celeb 基准上 BCubed F：Chinese Whispers 70.55、FastHAC 87.98、KNN-DBSCAN 89.34、**有监督 GCN-D 91.33** | 全量重排名为 v2 的候选形态；**BCubed 天花板 ≈90%** 是 `R7` 人工纠错必需、以及文案必须写「疑似」的定量依据 |

**六条结论直接落进本设计**：①入档走质量门禁，归属与建档解耦（阿里云 + PhotoPrism）；②档案只保留代表帧，全量抓拍留在成员表（专利 `CN110750670B`）；③整合与人工纠错是主干，不是兜底（专利 + Immich nightly）；④聚类参数必须分离且各有独立语义，不能用一个相似度阈值兼任（PhotoPrism 四参数）；⑤「在线赋值 + 残留补偿扫描」是工业共识形态，不是本设计的自创（Immich）；⑥纯无监督方法的 BCubed 上限约 90%，故必须承诺「可纠正」而非「准确」（learn-to-cluster 基准）。

### 并发、存储与索引约束

| 项 | 仓库客观事实 |
|---|---|
| 推理消费并发 | `internal/camera/stream_hub.go:1365` 每路相机独立 goroutine 调用 `record.Service.ProcessInferenceResult`，非单写者；`record.Service` 自身只有 `pendingMu`，无写锁 |
| SQLite 连接池 | `MaxOpenConns(1)`；`db.Transaction` 闭包内严禁调用自取连接的 Store 方法，否则永久死锁 |
| 相关索引 | `idx_recognitions_camera_captured_id(camera_id, captured_at DESC, id DESC)`、`idx_recognitions_captured_at_id`（迁移 `000021`）；无 `ANALYZE` 统计时双条件（`camera_id` + `cluster_id`）可能误选，需 `INDEXED BY` 锁定 |
| 异步抓拍时序 | `processPersonStructure` 先 `CreateRecognitionBatch` 再异步入队 `SnapshotStore`；队列满/关闭时 `clearJobPath` 会清空 `recognitions` 快照路径 —— 写簇时不能假定图片已落盘 |
| 保留清理路径 | `record.RetentionPruner.PruneNow`（`internal/record/retention.go:209`）按 TTL 清理，并经 `pending_snapshot_deletes` 在容量淘汰时提前删未过期 `recognitions` 行 |
| 冷却抑制 | `record.DefaultCooldown = 3s`（`internal/record/cooldown.go:10`），同 `(camera, track)` 每秒最多约 0.33 条 |
| 错误码与审计门禁 | 错误码在各领域 `locale.go` 经 `httputil.RegisterMessages` 注册（`internal/httputil/locale.go:137`）；`internal/audit/action_locales_test.go:27` 强制校验所有 Go `Action*` 常量存在于三语 `actionNames` |
| 时长解析 | `internal/app/config.go:161` `ParseSessionTTL` 已实现「Go duration + `d` 天后缀」 |
| ID 生成 | `internal/idutil`：`New()` 单调 ULID、`Parse()`/`IsValid()` 严格校验 |

### 规模估算（10 路相机）

- 记录速率：10 路 × 0.5 条/s ≈ 43 万/天；每天新增陌生面孔 300–800 人；30 天后约 1–2 万个候选簇。
- 在线活跃簇内存索引（`float32` 精确质心 2048 B/簇）：1 万簇约 20 MB；单条向量对活跃集的全量点积 = 1 万 × 512 维 ≈ 5.1 MFLOP，@5 GFLOP/s ≈ 1 ms，P95 < 2 ms 可达。
- **R3 全量两两合并不可行**：2 万簇两两 ≈ 2×10⁸ 对 × 1024 flop ≈ 204 GFLOP ⇒ 单核 20–40 s，与原 PRD「< 50 ms」差 3 个数量级。必须改增量架构（见 R4）。
- 明确不上 ANN：簇数 < 10 万时内存线性扫描足够。

## 分期

本任务交付 **v1**；v2 另开任务，不在本次范围。

- **v1（本次）**：质量门禁入档 · 轨迹段聚合 · 在线赋值 · 增量整合（保守自动合并，可被拆分撤销）· 残留补偿扫描 · 人工合并/拆分 · 轨迹查询 · 转正 · 最小前端。
- **v2（另开任务）**：`θ(N)` 自适应阈值 · 激进跨天自动合并 · 以图搜簇 · 人体 ReID 融合 · ANN 索引（若簇数超预期）· **宿主侧正脸度门禁**（由 `landmarks_json` 的 `inter_eye_x / box_w` 派生并落列，作为独立于 `face_quality` 的第二道门禁——v1 只把它用在 AC4 的离线标定里，不上热路径）。

## Requirements

### R1 — 数据模型（迁移 `000039`）

新增成对迁移 `internal/database/migrations/000039_add_stranger_clusters.{up,down}.sql`：

```sql
CREATE TABLE IF NOT EXISTS stranger_clusters (
    id                   TEXT PRIMARY KEY NOT NULL,          -- 单调 ULID，出生即定，永不重编号
    centroid             BLOB NOT NULL,                      -- float32[512] 小端，L2 归一化单位向量（2048 B）
    p10_similarity       REAL NOT NULL DEFAULT 0.0,          -- R3 由成员重算的到质心余弦 P10，用于可信度与污染诊断
    member_count         INTEGER NOT NULL DEFAULT 0,         -- 已归簇成员数（含低质归属帧）
    quality_count        INTEGER NOT NULL DEFAULT 0,         -- 参与质心的高质量成员数（>= min_face_quality）
    camera_count         INTEGER NOT NULL DEFAULT 0,         -- 派生缓存，权威来源为 stranger_cluster_cameras
    first_seen_at        DATETIME NOT NULL,
    last_seen_at         DATETIME NOT NULL,
    representative_id    TEXT,                               -- 代表帧 recognitions.id（逻辑弱引用）
    representative_path  TEXT NOT NULL DEFAULT '',           -- 代表帧裁剪图路径快照，避免列表 JOIN
    person_id            TEXT REFERENCES persons(id) ON DELETE SET NULL,
    person_name          TEXT NOT NULL DEFAULT '',           -- 转正时姓名快照
    merged_into_id       TEXT,                               -- status='merged' 时指向合并目标
    status               TEXT NOT NULL DEFAULT 'candidate'
                         CHECK (status IN ('candidate','confirmed','merged','discarded')),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (merged_into_id IS NULL OR merged_into_id <> id)
);

CREATE INDEX IF NOT EXISTS idx_stranger_clusters_status_last_seen
    ON stranger_clusters(status, last_seen_at DESC, id DESC);

-- 每簇每相机一行：cameraCount、详情相机列表、重启恢复 seenCameras 全部走这张小表，
-- 既避免 COUNT(DISTINCT)/GROUP BY 触发 TEMP B-TREE，也不需要把相机集合塞进 JSON 列。
CREATE TABLE IF NOT EXISTS stranger_cluster_cameras (
    cluster_id    TEXT NOT NULL REFERENCES stranger_clusters(id) ON DELETE CASCADE,
    camera_id     TEXT NOT NULL,
    first_seen_at DATETIME NOT NULL,
    last_seen_at  DATETIME NOT NULL,
    member_count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (cluster_id, camera_id)
);

CREATE TABLE IF NOT EXISTS stranger_cluster_merges (
    id         TEXT PRIMARY KEY NOT NULL,
    from_id    TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'auto' CHECK (source IN ('auto','manual')),
    operator   TEXT NOT NULL DEFAULT '',
    similarity REAL NOT NULL DEFAULT 0.0,
    evidence   TEXT NOT NULL DEFAULT '',                    -- JSON：命中证据摘要，供人工复核
    merged_at  DATETIME NOT NULL,
    CHECK (from_id <> to_id)
);

CREATE INDEX IF NOT EXISTS idx_stranger_cluster_merges_from ON stranger_cluster_merges(from_id);
CREATE INDEX IF NOT EXISTS idx_stranger_cluster_merges_to   ON stranger_cluster_merges(to_id);

-- 拆分是"撤销合并"的唯一手段，必须独立留痕（一个簇可能被合并、拆分、再合并）
CREATE TABLE IF NOT EXISTS stranger_cluster_splits (
    id             TEXT PRIMARY KEY NOT NULL,
    from_id        TEXT NOT NULL,
    new_cluster_id TEXT NOT NULL,
    operator       TEXT NOT NULL DEFAULT '',
    member_count   INTEGER NOT NULL DEFAULT 0,
    split_at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_stranger_cluster_splits_from ON stranger_cluster_splits(from_id);

ALTER TABLE recognitions ADD COLUMN face_quality REAL NOT NULL DEFAULT 0.0;
-- 故意不加 REFERENCES：避免簇合并/空壳清理时在千万行 recognitions 上触发外键全表校验与锁
ALTER TABLE recognitions ADD COLUMN cluster_id TEXT;

CREATE INDEX IF NOT EXISTS idx_recognitions_cluster_captured_id
    ON recognitions(cluster_id, captured_at DESC, id DESC)
    WHERE cluster_id IS NOT NULL;

-- R3 补偿扫描专用部分索引：常态下仅含失败回退的未归簇行（接近 0 行）
CREATE INDEX IF NOT EXISTS idx_recognitions_unclustered_captured
    ON recognitions(captured_at ASC, id ASC)
    WHERE matched = 0 AND cluster_id IS NULL AND feature_vector IS NOT NULL;
```

- `down.sql` 严格按依赖逆序回滚：两个 `idx_recognitions_*` 部分索引 → `ALTER TABLE recognitions DROP COLUMN cluster_id` / `face_quality` → `stranger_cluster_splits`、`stranger_cluster_merges`、`stranger_cluster_cameras`、`stranger_clusters` 及其索引。
- `CHECK` 约束是本仓库既有惯例（见 `000020`/`000022`/`000030`），用于防未来代码写入未知状态。
- `recognitions.feature_vector` 保持权威证据地位：聚类**只写 `cluster_id`，永不改写原始向量、相机与抓拍时间**；质心是可从成员重建的派生索引。
- **不新增** `(cluster_id, face_quality)` 索引：代表帧选举对单簇成员做 Go 侧排序（簇成员有上界），不值得给热表加第三个索引。
- `internal/record/service_build.go` 的 `buildRecognitionRecordAndJob` 同步把有限值 `face.Quality`（非 `NaN`/`Inf`，负值归零）写入 `Recognition.FaceQuality`；`RecognitionResponse` 增补 `faceQuality` 与 `clusterId`。

### R2 — 入档质量门禁（建档与归属解耦）

这是本版与原设计的分歧点，也是准确率的地板。

```
建档（create cluster）     : face_quality >= θ_quality
更新质心（centroid update）: face_quality >= θ_quality
成为代表帧               : face_quality >= θ_quality 且快照文件存在
归属（assign cluster_id）  : 不设质量下限，只要向量有效
```

#### `face_quality` 到底是什么（决定 θ_quality 能取多少）

`face_quality` 不是"正脸程度"，而是**检测置信度乘上一个几何加权分**（`packages/face_recognition/onnx/src/common/face_quality.hpp`）：

```
geom_score    = 0.35·yaw_score + 0.20·pitch_score + 0.15·aspect_score + 0.30·size_score   // :235
det_factor    = clamp(detection.face_score, 0, 1)                                        // :237
quality_score = clamp(det_factor × (0.35 + 0.65 × geom_score), 0, 1)                     // :238
```

两个必须写进 `design.md` 的推论：

1. **`quality_score` 的上界就是 `det_factor`**——几何质量只能把分数**拉低**，永远不能抬高。检测置信度 0.9 的好帧天花板就是 0.9。
2. **`yaw_score` 只占几何分的 35%，而几何分只占综合分的 65%**，故 yaw 从 1.0 掉到 0 最多只把 `quality_score` 压低 `0.9 × 0.65 × 0.35 ≈ 0.20`。**一个尺寸、宽高比、俯仰都正常的大侧脸，在 `det = 0.9` 时 `quality_score ≈ 0.695`**（`0.9 × (0.35 + 0.65×0.65)`）。

结论（**原 PRD 的 `min_face_quality = 0.50` 是错的**）：阈值 0.50 在 `det ≈ 0.9` 时只要求 `geom_score ≥ 0.32`，等于「检测置信度够高就放行」——**它几乎不能挡住侧脸**，会把侧脸样本继续喂进质心。要真正挡住 `yaw = 0` 的帧需要 `θ_quality ≥ 0.70`，但那会连"夜间逆光但正脸"的帧一起丢掉（那些帧 `det ≈ 0.65`，天花板就只有 0.65）。

因此 R2 的正确表述是——**质量门禁不是正脸门禁**：

- 门禁的目标是剔除「检测置信度低」与「几何崩坏（宽高比 / 俯仰 / 尺寸异常）」的帧，**不承担正脸识别职责**；
- 正脸程度由三层共同压制：**R3 轨迹段加权均值**（压低单帧姿态方差）、**R4 代表帧选举**（只让最优帧代表人）、**R7 人工拆分**（把混进来的侧脸挪走）；
- `θ_quality` 默认 **0.65**（在 `det ≈ 0.9` 时要求 `geom_score ≥ 0.46`，即至少两个几何分量明显受损才拒绝），既压掉塌陷帧，又不至于让夜间场次产出零档案。**默认值只是起点，必须由 AC4 用现场库的 `quality_score` × 正脸度代理量（`inter_eye_x / box_w`）二维分布重新标定**；本 PRD 不再声称该阈值能筛正脸。

#### 其余约束

- 低质帧**照常落库并归属** `cluster_id`，因此仍能在「该陌生人的识别记录」里查到——查询完整性不受影响；但它**不参与质心、不能当选代表帧、其出现也不计入 `quality_count`**。
- 因此 R3 的「代表帧快照全过期」判据只看 `quality_count` 的成员，避免用低质帧勉强凑数。
- `θ_quality` 与插件自身的 `min_face_quality = 0.28`（`match_config.hpp:56`）**互不替代**：前者是宿主侧聚档门禁，后者是插件侧特征抽取门禁。两者都要满足，宿主的必须更高。
- **不需要额外的「人脸像素下限」参数**：`size_score` 已占几何分 30%（`face_quality.hpp:107-113`），且插件侧 `min_face_size`（`match_config.hpp:51` 默认 32 px，`config.schema.json` 可配 16–512）是**硬门槛**——低于阈值直接 `size_score = 0` / `quality_score = 0` / `pass = false`（`face_quality.hpp:96-100`）。阿里云 IMM 与 PhotoPrism 的「最小尺寸」门槛在此已被 `face_quality` 覆盖，**不要**在 `[stranger]` 里重复设一个像素阈值，否则同一件事有两个互相不知道的开关。
- 单一记录无法成档：新簇首次创建即要求质量达标；**连续 N 次（默认 `min_member_count = 2`）达标出现之前，簇在默认列表（`minMemberCount` 默认值）中不展示**，仅可通过成员页与记录页看到。
- **禁止**通过调低 `min_member_count` 来提升召回：PhotoPrism 的实测结论是"降低全局成核门槛会把样本多的人拆碎、整体召回反而下降"，正确做法是 R4 对**残留**单独放宽（见 R4）。

### R3 — 在线赋值（轨迹段为原子单位）

- **聚合单位是轨迹段，不是单条记录。** 在 `record.Service` 批量入库阶段，先把本批 `matched = 0` 且向量有效的记录按 `(camera_id, track_id)` 分组，并在组内按 `captured_at` 做时间邻接切段（间隔 > `segment_gap`，默认 30 s 切段），把每段内成员按 `face_quality²` 加权平均成一个 `float32[512]` 后 L2 归一化，作为**一个样本**参与赋值。
  - 依据：同一 `(camera_id, track_id)` 在轨迹存活期内是同一物理人（`internal/record/service.go:1047`），段内聚合零误判；均值同时压低侧脸/模糊帧方差，直接缓解「同人困难帧落在 0.4~0.6」的可分性问题。
  - 收益：赋值次数降 10–100 倍，`mu` 预算宽裕；段内成员天然获得同一 `cluster_id`，从结构上消灭「同轨迹被拆成多簇」这一类碎片。
  - **加权取用哪几帧必须与 R2 对齐**：段向量只用段内 `face_quality >= θ_quality` 的帧做加权均值（否则低质帧会绕开 R2 的门禁去污染质心，因为段向量正是质心更新的输入）；若整段无达标帧，则退化为用全部帧（向量有效即可），使该段仍能拿到 `cluster_id`，符合 R2「归属不设下限」；此退化路径**不得**更新质心、**不得**当选代表帧。
  - `track_id` 会在轨迹老化后被复用，切段必须带时间邻接条件（**禁止裸 `GROUP BY camera_id, track_id` 到底**），否则会跨越数小时把人串成一个簇。
- **模块边界**：新增独立业务包 `internal/stranger`；`record.Service` 通过 setter 注入窄接口 `ClusterAssigner`（与 `SnapshotStore` / `AlarmListener` 同构）。**接口类型必须定义在 `internal/record` 内**，实现由 `internal/stranger` 提供并在 `internal/app` 装配，否则 `record → stranger` 与 `stranger → record` 构成循环依赖。
- **并发、锁序与事务**：
  - 赋值器内部 `mu sync.Mutex` 串行化内存活跃簇状态与簇分配，承受多路并发调用，`-race` 零竞争；
  - **锁序铁律**：恒为 `ClusterAssigner.mu -> DB`；`db.Transaction` 闭包内严禁获取 `mu`，闭包内只用传入 `tx`，严禁重入取连接（`MaxOpenConns=1` 会永久死锁）；
  - 先完成簇分配与簇表持久化，把 `ClusterID` 直接填入 `Recognition.ClusterID` 随 `CreateRecognitionBatch` **一次性 INSERT**，绝不落库后逐行 `UPDATE`；
  - 既有簇更新必须带状态保护 `WHERE id = ? AND status = 'candidate'`；
  - `stranger_cluster_cameras` 用 `INSERT ... ON CONFLICT(cluster_id, camera_id) DO UPDATE` 维护，批内先按 `camera_id` 去重；
  - **Fail-open**：簇分配或簇表写库失败时记 Warn、`ClusterID = nil`，照常 `CreateRecognitionBatch`，绝不阻断识别落库；漏赋值行由 R4 补偿扫描补齐。
- **向量校验**：长度必须 2048 B，所有维度有限（非 `NaN`/`Inf`），`‖v‖₂ > 1e-6`；先内部归一化再参与点积。
- **判据与更新**：
  - 活跃候选集：内存维护 `active_window`（默认 `7d`，上限 `max_active_clusters = 10000`，按 `last_seen_at DESC` 裁剪）内的 `candidate` 簇；进程启动时除加载簇行外，从 `stranger_cluster_cameras` 直接恢复各活跃簇的相机集合。
  - 命中条件：最高质心余弦 `sim >= θ_assign`（默认 `0.45`，见 R11 标定要求）。命中则按有界样本权重 `w = min(quality_count, 32)` 更新质心 `centroid = normalize(w·centroid + x)`，刷新 `last_seen_at`/`member_count`/`quality_count`/`camera_count`/相机行/代表帧候选；未命中则生成新 ULID 簇（要求 `face_quality >= θ_quality`）。
  - **质心污染冻结必须可解除**：当 `p10_similarity > 0 && p10_similarity < θ_assign - 0.10` 时，在线路径暂停该簇的质心增量更新（成员仍可归属），但 **R4 每轮从成员精确重算质心时会自动解除冻结**。冻结只作用于在线增量，不是永久状态，无需额外解冻接口。
  - **禁止**使用 `sim >= sim_p10 - 0.05` 作为拒收条件（会把跨视角同人挡在簇外）。

### R4 — 周期整合（增量合并 / 补偿扫描 / 代表帧保活 / 退役）

- **调度与生命周期**：后台单实例定时任务（默认每 `consolidate_interval` = 15 min），接入 `internal/app` 的 `lifecycleRuntime`（`stopSequence`）优雅取消与排空，不挂无序 Fx 钩子；重度 SQLite 读写分批执行、批间让渡（20 ms）；**重度 I/O 期间绝不长持 `mu`**，仅在最终原子切换内存索引时短持。
  - 窗口取 15 min 而非秒级：Immich 明确记录「聚类开始时喂进去的队列越大，结果越好」——每轮只处理很小的增量会让"先到但当时还孤立"的样本过早定型。15 min 与 `DefaultCooldown = 3s` × 10 路 ≈ 数千条/轮的批量规模相匹配，同时保证「刚抓拍完到能查档案」的延迟对运维可接受。
- **补偿扫描（只扫残留）**：先于合并执行，用 R1 的部分索引 `idx_recognitions_unclustered_captured` 按 `captured_at ASC` 取 `matched = 0 AND cluster_id IS NULL AND feature_vector IS NOT NULL` 的行，逐条重走 R3 的赋值流程；每轮 `LIMIT` 有界并在下轮续扫（游标用 `(captured_at, id)`）。**该扫描只处理残留，绝不作为常态主路径**——这与 Immich「nightly job 只补扫未归属者」、PhotoPrism「第二遍聚类只跑残留」是同一形态。R3 因 fail-open 漏赋值的行、以及赋值时簇表写库失败的行，都在这里收敛。
- **合并必须增量，禁止每轮全量两两**（成本测算见「规模估算」）：
  - **每轮主路径**：只把「本轮新建簇 + 上一轮未参与合并的新簇」与全量 `candidate` 质心比对，复杂度 `O(新增 × 存量)`。以 15 min 一轮、约 100 个新簇、2 万存量簇计 ≈ 2×10⁶ 对 ≈ 2 GFLOP，单核 0.2–0.4 s，符合预算。
  - **低频全量兜底**：每 `merge_full_sweep_interval`（默认 `168h`）做一次全量扫描，且**按 ULID 时间前缀分片**，一轮只处理一个分片；分片游标持久化在 `stranger_clusters` 之外的任务状态中，保证可续跑。
  - **硬预算**：每轮比较对数上限 `max_pairs_per_round`（默认 5×10⁶），超限即让出并在下一轮续算；严禁在单轮内跑满全量。
- **成员级证据（防质心搭桥）**：质心粗筛 `centroid_sim >= θ_merge` 得到候选对后，仅按主键读取候选簇各自 Top-K（`face_quality DESC`，每簇最多 16 条且 `face_quality >= θ_quality`）成员的 `feature_vector` 做核验：
  - 要求至少 2 对跨簇成员 `sim >= θ_merge`；单帧簇退化为该唯一帧与对方至少 2 名成员 `sim >= θ_merge + 0.03`；
  - 合并后双方代表成员的最小交叉相似度不得低于 `θ_assign - 0.10`；
  - 跨天合并受 `max_merge_span`（默认 `168h`）约束：两簇 `last_seen_at` 间隔超限时默认不自动合并，交由人工合并处理。理由是跨天自动合并风险最高而人工兜底成本最低。
- **`θ_merge <= θ_assign` 是硬约束**（配置校验层直接拒绝）：合并不可逆（只能靠拆分修正），而拆分需要人介入，因此**自动合并必须比在线赋值更激进而不是更保守**，不对称性只通过「成员对数证据 + 防粘连下界 + 跨度上限」体现，不通过抬高阈值体现。原设计 `θ_assign = 0.55 / θ_merge = 0.58` 会在同一相似度区间既产生碎片又拒绝修复碎片（`sim = 0.52` 的同一人：赋值失败被拆、合并也失败），两头落空。
- **合并执行**：保留 `first_seen_at` 较早（同时间取 ULID 较小）者为 `to_id`，写入 `stranger_cluster_merges`；把历史上指向 `from_id` 的 `stranger_cluster_merges.to_id` 与 `stranger_clusters.merged_into_id` **一并扁平更新至 `to_id`**，使合并链长度恒为 ≤1（`maxHops = 8` 仅作防御性上限）；被并簇置 `status='merged'`、`merged_into_id=to_id`；分批改写成员 `recognitions.cluster_id`；合并 `stranger_cluster_cameras` 行（`ON CONFLICT` 取 min/max/sum）；从成员精确重算质心、`p10_similarity`、`member_count`、`quality_count`、`camera_count`；最后通知 `ClusterAssigner` 驱逐 `from_id` 并刷新 `to_id`。
- **代表帧选举与保活**：按 `face_quality` 为主、归一化人脸框面积为辅，从 `quality_count` 成员中挑选最优且快照文件（`SnapshotFileChecker`）仍存在的帧；原代表帧随 TTL/容量淘汰失效时降级至次优存活帧；全簇快照均已清理则清空 `representative_path`。若校验发现簇内现存 `recognitions` 行数已为 0，直接把该簇标为 `discarded` 并归零计数、从内存驱逐，杜绝幽灵档案。
- **退役与空壳物理清理**：
  - `last_seen_at` 早于 `retention_days`（默认 90 天）的 `candidate` 簇标 `discarded` 并从内存驱逐；
  - `last_seen_at` 早于 `max(retention_days, snapshotsRetentionDays) + 30d` 的 `discarded`/`merged` 簇，在校验 `EXISTS (SELECT 1 FROM recognitions WHERE cluster_id = ?)` 为假后分批物理删除（`stranger_cluster_cameras` 由外键级联清理，`merges`/`splits` 显式清理）。
- **明确禁止**：夜间全量重新聚类（防止簇 ID 漂移打断用户确认与外链）。

### R5 — 轨迹查询扩展（复用 `internal/spatial`）

- `TrajectoryQuery` 新增 `targetType=stranger` 与 `clusterId` 参数（缺失 `422 SPATIAL_CLUSTER_ID_REQUIRED`、非 ULID `422 SPATIAL_INVALID_CLUSTER_ID`）。
- **必须同步修改 `internal/spatial/requests.go:37` 的 `binding:"required,oneof=vehicle person"` 为 `oneof=vehicle person stranger`**，否则 gin binding 在进 Service 前即 422，`INVALID_TARGET_TYPE` 分支不可达。
- 错误码沿用本包既有 `SPATIAL_` 前缀（既有 `SPATIAL_PERSON_ID_REQUIRED`、`PLATE_REQUIRED`、`INVALID_TARGET_TYPE`），新增码在 `internal/spatial/locale.go` 注册三语；同步更新 `INVALID_TARGET_TYPE` 文案为 `targetType must be vehicle, person or stranger`（三语）。
- 若 `clusterId` 指向 `status='merged'` 且 `merged_into_id` 非空，按有界跳数（`maxHops = 8`）解析至最终 `to_id`；簇不存在返回 `404 STRANGER_CLUSTER_NOT_FOUND`。
- 新增 `Store.QueryStrangerRecognitions(ctx, filter)`：
  `FROM recognitions INDEXED BY idx_recognitions_cluster_captured_id WHERE cluster_id = ? AND matched = 0 AND camera_id IN (?) AND captured_at >= ? AND captured_at <= ? ORDER BY captured_at ASC`
  显式 `INDEXED BY` 保证即使地图只标定 1 台相机也不误选 `idx_recognitions_camera_captured_id`，`EXPLAIN QUERY PLAN` 严禁出现 `TEMP B-TREE`。
- 完整复用 `aggregateDetections` 的 dwell window 同相机折叠与未标定相机跳过逻辑；时间窗必填，倒序/空窗按既有 `INVALID_TIME_RANGE` 返回。

### R6 — 查询 API

遵循 `.trellis/spec/backend/http-api-guidelines.md`（`*Request` / `*Response` / `*Info`，禁用 `*DTO`），统一 `httputil` 信封，把 `./internal/stranger` 加入 `Makefile` 的 `api-docs --dir` 白名单，错误文案在 `internal/stranger/locale.go` 内 `init()` 注册（不改 `internal/httputil/locale.go`）：

| 端点 | 契约 |
|---|---|
| `GET /api/v1/strangers` | 分页列表，默认 `status=candidate` 且 `minMemberCount=2`；支持 `?status=`、`?minMemberCount=`、`?cameraId=`、`?startTime=`/`?endTime=`；返回 `id / firstSeenAt / lastSeenAt / memberCount / qualityCount / cameraCount / representativeId / representativePath / trustLevel / status / personId / personName / mergedIntoId`；严禁读取 `centroid` BLOB。`trustLevel` 由 `memberCount`/`qualityCount`/`cameraCount`/`p10Similarity` 派生（`low`/`medium`/`high`），供 UI 呈"疑似"强度 |
| `GET /api/v1/strangers/:id` | 详情：上述元数据 + `cameras: []StrangerCameraStatsInfo`（直接读 `stranger_cluster_cameras`：`cameraId / cameraName / firstSeenAt / lastSeenAt / memberCount`） |
| `GET /api/v1/strangers/:id/recognitions` | 成员帧分页；`merged` 状态透明解析到最终 `to_id`；复用 `RecognitionResponse`（已增补 `faceQuality`/`clusterId`），`.Omit("feature_vector")` |
| `DELETE /api/v1/strangers/:id` | 软保留：置 `discarded` 并从内存驱逐（供运维清掉明显噪声档案，不做物理删除） |
| `GET /api/v1/records/recognitions?clusterId=` | 既有识别记录列表扩展可选 `clusterId`；`clusterId` 非空时强制 `INDEXED BY idx_recognitions_cluster_captured_id`，保持 `.Omit("feature_vector")` 且无 `TEMP B-TREE` |

- `:id` / `clusterId` 非 ULID 返回 `422`；簇不存在返回 `404 STRANGER_CLUSTER_NOT_FOUND`。

### R7 — 人工合并 / 拆分（准确率地板）

自动聚类不可能 100% 正确：`档案分裂`（同人多档）与`杂类`（异人混档）是这类系统的固有缺陷。人工纠错不是兜底，是**准确率的地板**，同时解决冷启动（运维勾选几张脸即得一个可信档案）。

这个"地板"不是谦辞，有定量依据：`yl-1993/learn-to-cluster`（CVPR'19/'20）在 MS-Celeb 上的 BCubed F-score 为 Chinese Whispers 70.55、FastHAC 87.98、KNN-DBSCAN 89.34、**有监督 GCN-D 91.33**（[基准表](https://github.com/yl-1993/learn-to-cluster)）。即：**纯无监督方法的天花板就在 90% 上下，且这里的 90% 是"每条归属都对"的比例，不是"每个人的档案大体对"**。本系统在边缘盒子上跑单模态、无 GPU 训练，只会更差。因此：

- 产品文案只承诺「**可纠正**」，不承诺「准确归档」；
- `merge` / `split` 是 v1 交付项而非后续优化，且 `split` 必须与 `merge` 同时上线（只有 merge 的系统，一次错并就永久不可撤销）。

- `POST /api/v1/strangers/:id/merge`，body `{ "sourceIds": ["<ulid>", ...] }`：把若干簇并入 `:id`。目标必须是 `candidate`，源不得为 `confirmed`；复用 R4 的合并执行路径（写 `merges` 时 `source='manual'`、`operator` 取当前会话主体、`evidence` 记人工操作）。**`source='manual'` 的合并不受 `θ_merge`/`max_merge_span` 约束**——人已经看过图了。
- `POST /api/v1/strangers/:id/split`，body `{ "memberIds": ["<recognition id>", ...] }`：把指定成员从簇中拆出，新建一个 ULID 簇（`status='candidate'`），从成员重算双方质心与计数，写 `stranger_cluster_splits`；若原簇因此 `quality_count < 1` 则整簇置 `discarded`。
- 两者的共同后置：驱逐/刷新 `ClusterAssigner` 内存态、刷新 `stranger_cluster_cameras`、必要时重算代表帧。
- 审计：`ActionStrangerMerge = "stranger.merge"`、`ActionStrangerSplit = "stranger.split"`、`ActionStrangerPromote = "stranger.promote"`、`ActionStrangerDiscard = "stranger.discard"` 定义在 `internal/stranger/model.go`，在 HTTP Handler 层记审计（Service 层不引 Gin/审计包），同步补齐 `web/src/features/audit/locales/{en,zh-Hans,zh-Hant}.json` 的 `actionNames` 与 `web/src/features/audit/data/audit-actions.ts:14` 的 `AUDIT_ACTION_KEYS`。

### R8 — 转正（人工确认转入人员底库）

- `POST /api/v1/strangers/:id/promote`，二选一明确语义（同时传或都不传返回 `422`）：
  1. `{ "name": "张三", "personId": "EMP-001" }` —— 新建人员并入册（`personId` 为可选**对外业务键**）；宿主开启 `[person].require_person_id_for_face_enrollment = true` 且未传时，**在创建 `Person` 之前**返回 `409 PERSON_ID_REQUIRED_FOR_ENROLLMENT`；
  2. `{ "targetPersonId": "<内部 persons.id>" }` —— 追加到已有人员（受 `MaxFacesPerPerson = 10` 约束）。
- **原子性、补偿与并发**：
  - per-cluster `inFlight` 互斥锁防同一簇并发双点转正（不阻塞其它簇或在线聚类）；
  - 新建人员路径：`CreatePerson` 成功后若人脸提取/入册失败，**必须立即补偿删除该 0 人脸 `Person`**，严禁留下幽灵人员导致重试 `personId` 冲突；
  - 入册成功后条件更新 `UPDATE stranger_clusters SET status='confirmed', person_id=?, person_name=?, updated_at=? WHERE id=? AND status='candidate'`（校验 `RowsAffected == 1`），并立即 `ClusterAssigner.Evict(id)`；后续该人员新抓拍由识别链路正常产出 `matched = 1`；
  - 已关联 `person` 后续被删除时，因外键 `ON DELETE SET NULL`，簇保持 `confirmed`、`personId = null`、`personName` 快照不变，前端正常展示。
- **证据图挑选与外扩裁剪**：
  - 优先 `representative_id`；记录或文件不存在时按 `face_quality DESC` 回落到 `quality_count` 成员中快照仍存在的次优帧；全簇无可用图片返回 **`409 STRANGER_EVIDENCE_EXPIRED`**（fail-closed，禁止向底库写无头像幽灵模板）；
  - 优先读全幅 `image_path` 与 `box_json` `[x1,y1,x2,y2]`：框有效（`bw>0 && bh>0`）时以框中心向四周各外扩 `0.60 × bw`/`0.60 × bh`（总裁剪框 2.2×，clamp 至 `[0,1]`）裁剪单人特写 JPEG。理由：`_face.jpg` 是偶数对齐紧裁（`jpeg_encoder.cpp:214-251`），零边距易触发 `NO_FACE_DETECTED`；多人全幅图易触发 `MULTIPLE_FACES_DETECTED`（插件侧 `embeddable_faces > 1` → `ZHULONG_PLUGIN_ERR_AMBIGUOUS`，`face_recognition.cpp:967-977`）。若紧邻双人仍检出多脸，透传 `422 MULTIPLE_FACES_DETECTED` 由人工改选；全幅图缺失或 `box_json` 退化时，对 `face_image_path` 四周补中性色边扩至 2.2× 兜底；
  - 经 `internal/stranger` 窄接口调用 `person` 域标准入册通道完成**单次** `ExtractFaceFeature`、写 `facesDir` 与 `person_faces`、并经 `MSG_GALLERY_UPSERT` 实时广播到算法 Runner。
- **状态校验**：已 `confirmed` 重复调用返回 `409 STRANGER_ALREADY_PROMOTED`；`merged`/`discarded` 返回 `409 STRANGER_INVALID_STATE`。

### R9 — 实时广播与隐私隔离

- 在线簇赋值位于持久化路径，**绝不回填**已广播的实时预览帧：SSE `event: ai_result` 载荷保持现状（无 `cluster_id`、无向量）。
- 所有 HTTP 响应严禁暴露 `stranger_clusters.centroid` 与 `recognitions.feature_vector` 二进制特征；`evidence` 审计字段只存摘要不存向量。
- 本期不做实时簇事件流，前端全部走 REST。

### R10 — 最小前端（`web/`）

- **记录页（`src/features/records`）**：在「识别记录」标签页增加视图切换（「识别明细」/「陌生人档案」），用独立 URL 参数 **`recMode`**（`'details' | 'strangers'`），不复用控制表格/卡片布局的 `view`。改动点仅在 `web/src/features/records/data/schema.ts` 的 `recordsSearchSchema`（路由文件 `routes/_authenticated/records/index.tsx:6` 已 `validateSearch: recordsSearchSchema.parse`，**无需改路由，`routeTree.gen.ts` 不动**）。
  - 档案卡片：代表帧缩略图（含文件缺失占位）、首末出现时间、抓拍次数、经过相机数、**可信度徽标**（`trustLevel`，用 `@/lib/status-palette` 的 Dot-Indicator Outline 四档调色板 + `dark:` 配对）；操作：「查看轨迹」「转入底库」「合并到…」「拆出选中帧」。
  - 三态齐备：Loading 骨架、Error（含 `filter_unmatched` 被开启时的配置提示）、Empty（"暂无反复出现的陌生人"，并说明"仅出现一次的抓拍不会入档"）。
- **空间页（`src/features/spatial`）**：
  - `'stranger'` 枚举需改 **4 处**：`web/src/features/spatial/data/schema.ts` 的 `:64`、`:73`、`:95`、`:143`；
  - `components/toolbar/trajectory-search-toolbar.tsx`：Tabs 增加第三项（`:130-153` 现为硬编码两项）；`:160` 的二元三元表达式改为三元分支，新增 `clusterId` 输入；`superRefine`（`:100-123`）增加 `stranger` 必填校验；`TrajectoryQueryParams`（`:71-78`）增加字段；
  - `index.tsx` 深链接 effect（`:151-172`）必须修：`:159` 硬编码 `resolveTimeRange('today')` 会导致昨天/跨天档案查出空轨迹 —— 改为优先读 URL 时间窗，缺省用 `7d`；同时**不要**在 `deps` 里等待 `personsData`（`personsData` 已迁至 toolbar 的 `:76`）。
- **前端规范门禁**：所有新增文案覆盖 `en` / `zh-Hans` / `zh-Hant` 三语（`web/src/lib/i18n.test.ts:23-45` 强制三语扁平键集完全一致），含 `records`、`spatial`、`audit` 的四个 `stranger.*` 审计词条；输入经 Zod 校验（列表 schema 现有字段均带 `.catch()`，新增字段同样需要处理非法值）；遵循 OKLCH 语义色、逻辑方向类（`ps-`/`pe-`）与 RTL 交付矩阵。

### R11 — 配置项

`config.toml`、`cmd/Zhulong/config.go`（严格 `UnmarshalExact`）、`internal/app/config.go`（`DefaultConfig` + 范围校验）新增 `[stranger]` 段：

```toml
[stranger]
enabled = true
# 在线赋值阈值。默认值依据：模型底库命中阈值为 0.40（match_config.hpp:28），
# 同人困难帧落在 0.4~0.6、异人接近 0（match_config.hpp:93-99），且 1:N 检索的
# 极端值效应会把异人最大分随候选数上抬，故取略高于 0.40 的值。必须经 AC4 标定。
assign_threshold = 0.45
# 自动合并阈值。硬约束 merge_threshold <= assign_threshold（见 R4）。
merge_threshold = 0.45
# 入档质量门禁：低于此值不建档、不更新质心、不参与代表帧选举，但仍归属簇。
# 依据 face_quality.hpp:238 —— quality = det_factor × (0.35 + 0.65 × geom_score)。
# 取 0.65 时，在 det≈0.9 下要求 geom_score ≥ 0.46（至少两个几何分量明显受损才拒绝）；
# 取 0.50 等价于"检测置信度够高就放行"，几乎挡不住侧脸（见 R2）。必须经 AC4 重标定。
min_face_quality = 0.65
# 在线候选簇活跃窗口 / 簇保留期 / 相机轨迹段切分间隔 / 跨天自动合并跨度上限
active_window = "7d"
retention_days = 90
segment_gap = "30s"
max_merge_span = "168h"
# 资源上限与调度
max_active_clusters = 10000
max_pairs_per_round = 5000000
consolidate_interval = "15m"
merge_full_sweep_interval = "168h"
# 默认列表只展示至少出现 N 次的档案，过滤单人噪声
min_member_count = 2
```

- 时长项复用「Go duration + `d` 天后缀」解析：把 `internal/app/config.go:161 ParseSessionTTL` 的解析逻辑抽为通用 `ParseDurationWithDays`，`ParseSessionTTL` 保留为薄封装（不破坏既有调用与测试），`[stranger]` 的四个时长项统一复用。
- 校验硬规则：`merge_threshold <= assign_threshold`（违反即启动失败并给出明确文案）、`min_face_quality ∈ (0,1]`、`active_window ∈ [1h, 720h]`、`retention_days ∈ [1,730]`、`max_active_clusters ∈ [100, 100000]`。
- `min_face_quality` 必须可配置而非写死：`quality_score` 的分布是场景相关的（夜间/逆光整体下移），常数会导致「全空档案」或「全噪声」两个极端。

## Acceptance Criteria

- [ ] **AC0（流程门禁）**：`design.md` 与 `implement.md` 已产出并通过评审（`.trellis/workflow.md` 复杂任务要求 `prd.md` + `design.md` + `implement.md` 齐备后才可 `task.py start`）；`prd.md` 的迁移编号、锚点与模块清单与最终实现一致。
- [ ] **AC1（迁移与查询计划）**：`000039` up/down 可逆；三个 `recognitions` 索引与 `idx_stranger_clusters_status_last_seen` 生效；簇分页、簇成员分页、`ListRecognitions(clusterId)`（含同时传 `cameraId`）、R4 补偿扫描、陌生人轨迹查询（含地图仅标定 1 台相机）的 `EXPLAIN QUERY PLAN` 均**无 `TEMP B-TREE` 与全表扫描**；列表与详情接口均不加载 `feature_vector` / `centroid` BLOB；`stranger_cluster_cameras` 的 `camera_count` 与簇行缓存值一致。
- [ ] **AC2（入档质量门禁）**：`face_quality < min_face_quality` 的记录**不**建档、**不**更新质心、**不**参与代表帧选举，但**仍**获得 `cluster_id` 且出现在 `/:id/recognitions` 与 `?clusterId=` 查询结果中；`quality_count` 只统计达标成员；`representative_id` 恒指向达标且快照存在的帧；把 `min_face_quality` 调高后同一批样本产出的簇数单调增加（门禁确实生效）；落库的 `face_quality` 与插件 `quality` 一致且经有限性归一（`NaN`/`Inf` → 0，负值 → 0）；`[stranger]` 配置段中**不存在**独立的像素尺寸阈值（像素门槛由插件 `min_face_size` 与 `size_score` 承担，禁止双开关）。
- [ ] **AC3（并发与轨迹段赋值）**：`-race` 覆盖——多路并发 `ProcessInferenceResult` 无数据竞争；**同一 `(camera_id, track_id)` 段内所有记录获得同一 `cluster_id`**（结构性保证，含跨批次）；间隔 > `segment_gap` 的同一 `track_id` 被切成两段并允许进入不同簇（防止轨迹 ID 复用串人）；近邻向量归入同一簇且质心保持 `‖c‖₂ ≈ 1.0`；正交向量（`sim ≈ 0`）必成 2 簇；`feature_vector` 为 NULL/全零/`NaN` 安全跳过且不污染质心；簇写库失败时 `recognitions` 仍成功落库（`cluster_id IS NULL`）并被 R4 补偿扫描补齐；重启后活跃簇与相机集合从 SQLite 正确恢复；**全部成员 `face_quality < θ_quality` 的轨迹段仍获得 `cluster_id`，但不改变质心、不成为代表帧**（验证低质帧不能借段均值绕过 R2 门禁）。
- [ ] **AC4（阈值标定报告，替代原 dry-run）**：提供只读校准工具（不改库），对真实 `recognitions.feature_vector` 样本按「同轨迹段 / 跨相机制 / 异人」三类计算余弦分布，并输出可复算的 `θ(N)` 曲线：给定目标单条记录误并率（默认 `1e-4`）与活跃簇数 `N`，求出满足 `N · Φ̄((θ−μ_imp)/σ_imp) = 1e-4` 的 `θ`。报告必须写明样本量、模型名与量化后端、实测 `μ_imp`/`σ_imp` 拟合结果，并给出 `assign_threshold`/`min_face_quality` 的取值建议。仅给"跑一次看看"式的 dry-run 结论不合格。
  - **必须额外产出的两张表**（这是 R2 结论的验证，缺一即为不合格）：
    1. `face_quality` × 正脸度代理量的**二维分布**。代理量用 `landmarks_json` 的双眼 x 跨度除以 `box_json` 的框宽（`inter_eye_x / box_w`，纯 x 轴比值，对画幅宽高比不变，无需图像尺寸）。正脸约 0.45–0.55，侧脸随偏航塌陷。
    2. 「取 `θ_quality = 0.50 / 0.65 / 0.70 / 0.75` 各自损失多少**低正脸度样本**与多少**夜间/逆光正脸样本**（按 `captured_at` 小时分桶）」的对照表。若 0.65 的收益与 0.50 无显著差异，说明门禁确实只在按检测置信度筛人，须上调。
  - 报告须能正面回答一个问题：**`θ_quality` 到底在筛"检测置信度"还是在筛"正脸度"？** 回答不了即视为未完成 AC4。
- [ ] **AC5（跨相机轨迹与合并重定向）**：同一簇在 ≥2 台标定相机的记录（含 R4 合并产生的跨相机簇）经 `GET /api/v1/spatial/trajectory?targetType=stranger&clusterId=...` 返回时间升序节点，跨相机不折叠、同相机 30 s 内按 dwell window 折叠、未标定相机自动跳过；被合并的旧 `from_id` 查询轨迹与 `/:id/recognitions` 均正确解析到 `to_id`；`targetType=stranger` 在 `binding` 层被接受（不返回 `oneof` 校验错误）。
- [ ] **AC6（人工合并 / 拆分闭环）**：合并两簇后成员 `cluster_id` 全部改写、`camera_count` 与 `stranger_cluster_cameras` 正确合并、`merges` 留痕 `source='manual'`、源簇从默认列表消失、在 `max_merge_span` 之外的跨天簇也能人工合并；对合并结果执行拆分后成员回到新簇、双方质心与计数重算正确、`splits` 留痕、拆分出的成员不再出现在原簇轨迹中；`quality_count` 归零的簇被置 `discarded`；三个 `stranger.*` 以外的审计动作齐备且 `TestAuditActionLocalesCoverBackendActions` 通过。
- [ ] **AC7（转正闭环）**：转正（外扩裁剪路径）成功后 `person_faces` 新增 2048 B 特征、`MSG_GALLERY_UPSERT` 已广播、簇置 `confirmed` 并记录 `person_id`、立即从内存驱逐并从默认列表消失、写 `stranger.promote` 审计；人脸提取失败不留 0 人脸幽灵 `Person`；并发转正同一簇仅成功一次；代表帧失效时回落次优存活帧，全簇过期返回 `409 STRANGER_EVIDENCE_EXPIRED`；重复转正返回 `409 STRANGER_ALREADY_PROMOTED`。
- [ ] **AC8（性能与预算）**：1 万活跃簇下在线赋值（含轨迹段聚合）单段 P95 < 2 ms；单轮 R4 增量合并（新增 ~100 簇 × 存量 2 万簇）wall time < 2 s 且比较对数 ≤ `max_pairs_per_round`；24 h 时间窗单簇轨迹查询 P95 < 200 ms；测试报告注明运行硬件（Host CPU 或 RK3568 实测）、活跃簇数与成员行数，并**给出全量两两合并的实测成本**用于佐证增量架构的必要性。
- [ ] **AC9（全仓门禁与文档同步）**：`Makefile` `api-docs --dir` 已加入 `./internal/stranger`；`make pkg-drift-check`、`make native-test`、`make api-docs`、`make frontend-build`（同步 `internal/webui/dist`）与 `make check` 全部通过；新增契约同步更新至 `.trellis/spec/backend/record-guidelines.md`、**`.trellis/spec/backend/spatial-guidelines.md`**（R5 改变了该契约）与 `.trellis/spec/backend/directory-structure.md`。

## Out of Scope

- **引入任何开源聚类运行时**。已评估：`openvigil`（YOLO11 + InsightFace + OSNet body re-ID，Python + GPU + PostgreSQL）、`Shelly-NAS-hub`（FastAPI + SQLite，表结构 `face_clusters`/`face_crops`）、CompreFace（Apache-2.0，但只做已知 subject 的 1:N，**没有陌生人聚类**）、`learn-to-cluster`（需训练 GCN + GPU）。全部与 `AGENTS.md` 的交付模型冲突：**单个 Go 二进制 + 运行时零 Node/Python 依赖 + 静态链接 C++17 引擎 + `make check` 门禁**，接进来等于在 RK3568 上多养一个进程。而 DBSCAN 的增量变体在 512 维上只有几十行纯 Go，无依赖收益。**只借结构（见「工业界对照/开源实现」），不借代码。** `Shelly-NAS-hub` 的三表结构可作 `design.md` 的字段完整性交叉检查样本。
- **人体 ReID**：本任务只用人脸 512 维特征，不覆盖无正脸的背身/俯拍跨镜跟踪。这是产品承诺被限定为"正脸可辨"的直接原因。
- **`θ(N)` 自适应阈值在线生效**：AC4 只产出标定方法与曲线，v1 仍用静态配置值；在线自适应留 v2。
- **以图搜陌生人簇**：上传图片检索 `stranger_clusters` 留作独立任务（本期 `SearchByImage` 仍只搜 `person_faces`）。
- **ANN / HNSW / IVF**：簇数 < 10 万时无收益。
- **跨镜实时接力预测**、**夜间全量重聚类**、**簇合并的图形化编辑器**、**陌生人热力图**、**实时簇事件流**。
- 不修改 `captures` / `alarms` 表结构，不改实时 SSE `ai_result` 载荷契约。

## Risks

| 风险 | 处置 |
|---|---|
| 阈值偏高致碎片化、偏低致误并污染档案 | `assign_threshold` 锚定模型底库阈值 0.40 与其困难帧分布（`match_config.hpp:28`、`:93-99`），上线前用 AC4 的 `θ(N)` 曲线标定；`θ_merge <= θ_assign` 硬约束 + 成员级多对证据 + 防粘连下界；**拆分能力保证误并可撤销** |
| 低质帧污染质心（侧脸搭桥） | 建档与质心更新要求 `face_quality >= min_face_quality`；低质帧只归属不改心；代表帧只从达标成员选举；污染冻结仅限在线增量且由 R4 精确重算自动解除 |
| **`face_quality` 被误当正脸门禁 → 门禁形同虚设** | 已量化：`quality = det × (0.35 + 0.65·geom)`（`face_quality.hpp:238`），`det ≈ 0.9` 时 `θ = 0.50` 只要求 `geom ≥ 0.32`，**大侧脸仍会通过**。处置：默认值上调至 `0.65`；R2 明确声明门禁不承担正脸职责；正脸由 R3 段内加权均值 + R4 代表帧选举 + R7 人工拆分三层压制；AC4 强制产出 `face_quality` × `inter_eye_x/box_w` 二维分布以验证门禁的真实作用对象 |
| 追求"更准"而引入开源聚类运行时 | `openvigil` / `Shelly-NAS-hub` / CompreFace / `learn-to-cluster` 均为 Python + GPU（部分需 PostgreSQL），与 `AGENTS.md` 单二进制交付模型冲突。只借结构不借代码；增量变体在 512 维上是几十行纯 Go。若评审要求"用成熟方案"，以其三表结构作字段交叉检查即可回应 |
| 用调低 `min_member_count` 提升召回 | PhotoPrism 实测结论：降低全局成核门槛会把样本多的人拆碎、整体召回下降。正确做法是 R2 保持建档门槛、R4 对**残留**单独放宽 |
| 帧级赋值导致同轨迹碎片化 | 以轨迹段为聚合单位（段内均值 + 同段同簇），从结构上消灭该类碎片；切段带 30 s 时间邻接，防 `track_id` 复用串人 |
| R4 全量两两合并 O(n²) 超时 | 主路径改为「新增簇 × 存量簇」增量比对（0.2–0.4 s/轮），低频全量按 ULID 分片 + `max_pairs_per_round` 硬预算；AC8 要求实测全量成本佐证 |
| 转正时紧裁图二次检测失败，或失败后遗留孤儿人员 | 从全幅 `image_path` 按 `box_json` 外扩 60%/边（2.2×）裁剪；失败补偿删除新建的 0 人脸 `Person`；快照全过期显式 `409` 拒绝无图入库 |
| 多路并发与 `MaxOpenConns=1` 锁争用 | `mu` 仅保护内存态与短库写，严守 `mu -> DB` 锁序、事务内零重入；R4 批间让渡，不在重度 I/O 期间长持 `mu` |
| 查询计划误选索引或全表扫描 | R4 补偿扫描专用部分索引；按 `cluster_id` 查询显式 `INDEXED BY`；相机聚合走 `stranger_cluster_cameras` 小表；单测锁定 `EXPLAIN QUERY PLAN` |
| 产品预期被拉高（UI 呈现为"识别某人"） | 一律使用"疑似档案 / 疑似同一人"+ `trustLevel` 徽标；文档与 UI 明确"cluster 是聚类编号不是身份"；空态说明单位落档规则 |
| 算法包 `filter_unmatched = true` 导致无数据 | `GET /api/v1/strangers` 空态的 EMPTY 文案检测该配置并给出开启指引 |

## 交付物与流程门禁

1. `prd.md`（本文件，已按评审修订）。
2. `design.md`：模块划分与依赖方向（`record` 定义 `ClusterAssigner`、`stranger` 实现）、`mu -> DB` 锁序时序图、增量合并的分片与游标算法、轨迹段聚合的数据流、`θ(N)` 标定方法、前端 4 处枚举改动清单；**另须含两节**：①「工业形态映射」——把 Immich 的 core-point/残留补扫、PhotoPrism 的四参数分离与"残留另用低成核门槛"、`learn-to-cluster` 的 BCubed 天花板，逐条映射到 R2/R3/R4/R7，并对每一处**同构或有意的偏离**给出理由（用于挡"为什么不直接套用成熟方案"的质疑）；②「`face_quality` 语义与门禁作用域」——抄录 `face_quality.hpp:235-238` 的合成公式、给出 `θ_quality` 与 `(det, geom)` 的换算表、明确声明该门禁不承担正脸职责。
3. `implement.md`：按 R1→R11 的可执行步骤序列，含每步的验收命令与回滚点。
4. 复杂任务门禁：三份文档齐备并过评审后才 `task.py start`（`.trellis/workflow.md`）。
