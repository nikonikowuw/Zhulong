# 实现感知记录（抓拍/识别）与告警持久化存储

## Goal

在 Go 宿主端建立完整的算法感知与告警持久化机制，实现三大核心数据域的落库存储与介质管理：
1. **识别记录 (`recognitions`)**：沉淀人脸/人员比对流水，落实**方案 B（精确携带匹配底库照片 `face_id`）**，存储 Top-5 候选人名单，并支持人体结构化扩展属性与现场抓拍/底库快照 1:1 双图核验。
2. **告警记录 (`alarms`)**：检出违规目标即触发告警，保存现场全景大图及可选的违规目标特写抠图 (`crop_path`)，享受存储高价值铁律保护（免杀）。
3. **抓拍记录 (`captures`)**：针对过人、过车、过物的卡口视觉建档，保存现场大图与目标特写抠图 (`crop_path`)，支持通用结构化扩展属性，按短期高频生命周期随双水位淘汰。
4. **时间戳铁律**：事件时间严格采用**「抓拍那一帧发生的时间戳（Frame Capture Time / `captured_at`）」**，坚决不使用检测完成或数据库落库的墙上时间，彻底杜绝排队推理延迟导致的时序漂移。

---

## 业务需求 (Requirements)

### R1. Native 与 C ABI 升级（方案 B 与帧抓拍时间透传）
- 在 `native/src/algorithm/ipc/ipc_protocol.hpp` 中：
  - `FrameDispatchMsg` 结构体扩充 `int64_t capture_time_ms`（帧捕获绝对 UTC 毫秒时间戳），维持 96 字节严格 8 字节对齐；
- 在 `native/sdk/include/zhulong/plugin.h` 中：
  - 为 `zhulong_face_candidate` 补充 `face_id`（所属底库单张照片 ID）；
  - 为 `zhulong_face_item` 补充 `matched_face_id`（Top-1 胜出底库人脸 ID）；
- 在 `native/src/algorithm/runner/runner.cpp` 中：
  - 特征比对胜出后，保留对应的 `FaceTemplate.face_id`；
  - 序列化 `MSG_RESULT` 时输出 `capture_time_ms`、`matched_face_id` 及候选人各自的 `face_id`；
- 在 Go 侧 `internal/algorithm/result.go` 中：
  - `RawInferencePayload` 增加 `CaptureTimeMs int64`，将 `InferenceResult.Timestamp` 精确锚定为抓拍帧时间；
  - 更新 `FaceCandidate` 补充 `FaceID string `json:"face_id"``；
  - 更新 `FaceSubItem` 补充 `MatchedFaceID string `json:"matched_face_id"``；
  - 更新 `PersonItem` 与 `DetectionItem` 补充结构化属性字段。

### R2. 数据库版本化迁移 (SQLite Schema 000011)
- 新增 `000011_create_perception_and_alarm_tables.up.sql` 与 `.down.sql`；
- 遵循 `.trellis/spec/backend/database-guidelines.md`：
  - 严禁 `AutoMigrate`；
  - 主键一律使用 ULID (`TEXT PRIMARY KEY NOT NULL`)；
  - **核心事件时间**：显式定义 `captured_at DATETIME NOT NULL`（以帧抓拍瞬间为准，毫秒精度），所有时序索引基于 `(camera_id, captured_at DESC)` 建立；
  - 物理落库时间：`created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP` 仅作为内部记账；
  - 外部引用弱外键设计：`person_id` 指向 `persons(id) ON DELETE SET NULL`，同时冗余 `person_name`，保证底库人员被注销后历史记录与证据不丢失；
  - 路径统一存相对路径，首段必须以 `snapshots` 开头，杜绝绝对路径与裸 URL；
- **三表规格**：
  - `recognitions`：包含 `id`, `camera_id`, `algorithm_id`, `pts`, `track_id`, `person_id`, `person_name`, `matched_face_id`, `matched`, `similarity`, `candidates_json`, `face_image_path`, `gallery_image_path`, `image_path`, `box_json`, `body_box_json`, `attributes_json`, `captured_at`, `created_at`；
  - `alarms`：包含 `id`, `camera_id`, `algorithm_id`, `alarm_type`, `score`, `box_json`, `image_path`, `crop_path`, `extra_json`, `pts`, `captured_at`, `created_at`；
  - `captures`：包含 `id`, `camera_id`, `target_type`, `track_id`, `score`, `box_json`, `crop_path`, `image_path`, `attributes_json`, `pts`, `captured_at`, `created_at`。

### R3. Go 业务领域持久化组件 (`internal/record`)
- 按照规范建立独立的 `internal/record` 包：
  - `model.go`：定义 GORM Entity 及 API DTO；
  - `store.go`：基于 `database.DBProvider` 提供只读查询、条件过滤（相机、`captured_at` 时间范围、人员、类型、分页等）与批量插入；
  - `service.go`：
    - 订阅并接收 `StreamHub` 的 `InferenceResult`；
    - 执行**防抖削峰（Tracking Cooldown）**：同一目标在设定冷却时间（如 3 秒）内不重复写库；
    - 抓拍快照对账：根据 `matched_face_id` 反查并快照其 `person_faces.image_path` 写入 `gallery_image_path`；
    - 抓拍与抠图存储管理：按 `snapshots/{camera_id}/{YYYY-MM-DD}/{id}.jpg` 和 `{id}_crop.jpg` 规划路径；
    - 入库时精确将 `InferenceResult.Timestamp`（即抓拍帧发生时刻）赋值给 `captured_at`；
  - `handler.go`：实现并暴露 RESTful HTTP 端点，统一使用 `internal/httputil` 信封：
    - `GET /api/v1/records/recognitions`；
    - `GET /api/v1/records/alarms`；
    - `GET /api/v1/records/captures`；
  - `module.go`：通过 Fx 提供依赖注入，并将端点挂入 `protected_routes`。

### R4. 存储生命周期规约协同
- 遵循 `.trellis/spec/backend/storage-guidelines.md`：
  - 媒体服务 (`internal/mediafile`) 原生支持根据记录中的 `image_path` / `crop_path` / `face_image_path` 通过 `/api/v1/media/content/{path}` 直读字节；
  - 存储双水位清理协程针对 `snapshots/` 实施生命周期治理，但不得误杀高价值告警关联的抓拍证据。

---

## 验收标准 (Acceptance Criteria)

- [ ] **AC-1 (C ABI & Runner 兼容性)**：
  - `zhulong_face_candidate` 与 `zhulong_face_item` 结构体内存对齐正确（8 字节对齐）；
  - `FrameDispatchMsg` 携带 `capture_time_ms`，Mock/集成测试验证 `MSG_RESULT` 正确输出 `capture_time_ms`、`matched_face_id` 及候选集 `face_id`。
- [ ] **AC-2 (时间戳保真性)**：
  - 数据库记录的 `captured_at` 严格等于视频帧捕获物理时间，断言不受推理排队耗时（`cost_ms`）影响；
  - 回放联动与时序排序严格按照 `captured_at DESC`。
- [ ] **AC-3 (数据库迁移安全)**：
  - `000011_create_perception_and_alarm_tables.up.sql` 与 `.down.sql` 正常升降级；
  - GORM 单元测试验证各表 CRUD 及索引命中（单测覆盖空值、弱外键置空、JSON 列解析）。
- [ ] **AC-4 (方案 B 双图对账与 Top-5 完整性)**：
  - 识别成功时，记录中保存准确的 `matched_face_id` 和对应的 `gallery_image_path`；
  - `candidates_json` 正确存储 Top-5 候选人名单；
  - 底库人员或底照被删除后，历史识别记录依然保持原样且不报错（快照不可变保护）。
- [ ] **AC-5 (告警与抓拍特写/结构化支持)**：
  - 告警记录包含现场大图与目标特写 `crop_path`（场景类告警允许为空）；
  - 抓拍记录包含通用 `attributes_json`，并支持按属性进行 JSON 筛选。
- [ ] **AC-6 (门禁与测试质量)**：
  - `make go-check`（含 `-race`）全绿；
  - `make native-test` 全绿；
  - `make api-docs` 自动生成 Swagger 文档并更新。

---

## 范围外 (Non-Goals)
- 前端界面的完整交互页面重构（本任务交付后端及 API 闭环，前端页面切片在后续独立任务承接）。
- 离线人脸特征向量聚类计算。
