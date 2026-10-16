# 实施计划：感知记录（抓拍/识别）与告警持久化存储

> 任务：`10-16-perception-records-and-alarms`
> 验证命令统一要求：所有 Go 单测必须带 `-race`；所有 C++ 原生测试走 `make native-test`。

---

## 检查清单

### 步骤 1：C ABI、IPC 协议与 Native Runner 升级（方案 B 与抓拍时间透传）
- [x] 在 `native/src/algorithm/ipc/ipc_protocol.hpp` 中为 `FrameDispatchMsg` 添加 `int64_t capture_time_ms`，断言 96 字节并保持 8 字节严格对齐。
- [x] 在 `native/src/algorithm/ipc/frame_broker.cpp` 的 `publish_frame` 中将帧抓拍时间戳（物理采样时刻）填入 `FrameDispatchMsg.capture_time_ms`。
- [x] 在 `native/sdk/include/zhulong/plugin.h` 中为 `zhulong_face_candidate` 添加 `face_id`，为 `zhulong_face_item` 添加 `matched_face_id`，确保 8 字节对齐。
- [x] 在 `native/src/algorithm/runner/runner.cpp` 中将 `capture_time_ms` 与向量检索胜出的 `face_id` 填入结构体，并在序列化为 `MSG_RESULT` JSON 消息时保留输出。
- [x] 运行原生单元测试与内存对齐验证。
- **验证**：`make native-test`

### 步骤 2：Go 侧推理结果契约更新 (`internal/algorithm`)
- [x] 在 `internal/algorithm/result.go` 中：
  - `RawInferencePayload` 增加 `CaptureTimeMs int64 `json:"capture_time_ms"``；
  - `InferenceResult` 的 `Timestamp` 字段锚定为 `time.UnixMilli(payload.CaptureTimeMs).UTC()`（抓拍瞬间物理时间）；
  - `FaceCandidate` 补充 `FaceID string `json:"face_id"``；
  - `FaceSubItem` 补充 `MatchedFaceID string `json:"matched_face_id"``；
  - `PersonItem` 补充 `Attributes map[string]any `json:"attributes,omitempty"``；
  - `DetectionItem` 补充 `Attributes map[string]any `json:"attributes,omitempty"``。
- [x] 更新 `result_test.go` 与 `ipc_contract_test.go` 保证跨语言解析序列化单测全绿。
- **验证**：`go test -race -v ./internal/algorithm/...`

### 步骤 3：数据库版本化迁移 (Migration 000011)
- [x] 创建 `internal/database/migrations/000011_create_perception_and_alarm_tables.up.sql`：
  - 创建 `recognitions` 表（含 `captured_at DATETIME NOT NULL`、Top-5 候选人 `candidates_json`、双图路径、结构化属性）；
  - 创建 `alarms` 表（含 `captured_at DATETIME NOT NULL`、违规大图 `image_path` 与目标特写 `crop_path`）；
  - 创建 `captures` 表（含 `captured_at DATETIME NOT NULL`、目标特写与通用结构化 `attributes_json`）；
  - 基于 `(camera_id, captured_at DESC)` 创建对应的高频复合时序索引。
- [x] 创建 `000011_create_perception_and_alarm_tables.down.sql` 执行逆序 DROP TABLE。
- [x] 运行数据库迁移测试。
- **验证**：`go test -race -v ./internal/database/...`

### 步骤 4：核心领域实体与 GORM 存储层 (`internal/record`)
- [x] 创建 `internal/record/model.go`：
  - 定义 `Recognition`、`Alarm`、`Capture` GORM 模型（严格遵循 ULID、UTC 抓拍时间 `CapturedAt`、弱外键）；
  - 定义请求/响应 DTO 与基于 `capturedAt` 的分页查询 Filter。
- [x] 创建 `internal/record/store.go`：
  - 实现依赖 `database.DBProvider` 的 Store；
  - 支持按相机、`captured_at` 时间范围、人员、标签、分页检索（按 `captured_at DESC` 排序）与高效批量写入。
- [x] 编写表驱动单元测试 `internal/record/store_test.go`。
- **验证**：`go test -race -v ./internal/record/... -run TestStore`

### 步骤 5：业务处理服务与削峰防抖引擎 (`internal/record`)
- [x] 创建 `internal/record/cooldown.go`：
  - 实现带滑动窗口与 TTL 过期的 `TrackingCooldownManager`（默认 3s 防抖）。
- [x] 创建 `internal/record/service.go`：
  - 实现 `InferenceResult` 消费处理器；
  - 将实体的 `CapturedAt` 锚定为 `InferenceResult.Timestamp`；
  - 实现防抖削峰过滤；
  - 根据 `matched_face_id` 反查并快照 `person_faces.image_path`；
  - 规划与管理现场图片落盘路径（`snapshots/...`）；
  - 执行数据库批量写入。
- [x] 编写表驱动单元测试 `internal/record/service_test.go`（覆盖时间戳保真、削峰、底照快照对账与空值降级）。
- **验证**：`go test -race -v ./internal/record/... -run TestService`

### 步骤 6：HTTP Handler 与路由挂载 (`internal/record`)
- [x] 创建 `internal/record/handler.go`：
  - `GET /api/v1/records/recognitions`；
  - `GET /api/v1/records/alarms`；
  - `GET /api/v1/records/captures`；
  - 严格使用 `internal/httputil` 响应信封。
- [x] 创建 `internal/record/locale.go`（三语国际化支持）。
- [x] 创建 `internal/record/module.go`：
  - Uber Fx 模块装配，提供 `NewRecordComponents`；
  - 注册至 `protected_routes`。
- [x] 编写集成测试 `internal/record/handler_test.go`。
- **验证**：`go test -race -v ./internal/record/... -run TestHandler`

### 步骤 7：应用装配与全系统回归集成
- [x] 在 `internal/app/app.go` 中注册 `record.NewRecordComponents`。
- [x] 将记录持久化消费者接入 `StreamHub` 的 AI 流输出。
- [x] 运行 Swagger 文档自动更新：`make api-docs`。
- [x] 运行完整代码门禁：
  - `make native-test`
  - `TMPDIR=/home/niko/tmp make go-check`
- **验证**：全系统门禁全绿，测试覆盖率完备。
