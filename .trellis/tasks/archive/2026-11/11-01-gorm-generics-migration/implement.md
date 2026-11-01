# GORM Generics API 迁移实施计划 (Implementation Plan)

## 批次与执行清单

### 批次 1: 基础领域 Store 迁移
- [x] 改造 `internal/auth/store.go`
  - 将 `HasUser`, `GetByUsername`, `GetByID`, `Create` 改造为 `gorm.G[User]`
  - 验证命令：`go test -v -race ./internal/auth/...`
- [x] 改造 `internal/audit/store.go`
  - 将 `Create`, `List` 改造为 `gorm.G[AuditLog]`
  - 验证命令：`go test -v -race ./internal/audit/...`
- [x] 改造 `internal/storage/store.go`
  - 将 `Get`, `Set`, `Delete`, `List` 改造为 `gorm.G[StorageKV]` 等
  - 验证命令：`go test -v -race ./internal/storage/...`
- [x] 改造 `internal/asyncjob/store.go`
  - 将 `Create`, `GetByID`, `UpdateStatus`, `UpdateProgress` 等改造为 `gorm.G[Job]`
  - 验证命令：`go test -v -race ./internal/asyncjob/...`

### 批次 2: 关联与配置领域 Store 迁移
- [x] 改造 `internal/algorithm/store.go` & `impact_store.go`
  - 将算法包模型、影响分析相关持久化迁移至 `gorm.G[Algorithm]` 与 `gorm.G[AlgorithmImpact]`
  - 验证命令：`go test -v -race ./internal/algorithm/...`
- [x] 改造 `internal/spatial/store.go`
  - 标定与区域持久化迁移至 `gorm.G[SpatialCalibration]` 等
  - 验证命令：`go test -v -race ./internal/spatial/...`
- [x] 改造 `internal/gb28181/store.go`
  - 国标平台与通道配置持久化迁移至 `gorm.G[GB28181Platform]`, `gorm.G[GB28181Channel]`
  - 验证命令：`go test -v -race ./internal/gb28181/...`

### 批次 3: 核心复杂业务 Store 迁移
- [x] 改造 `internal/camera/store.go`
  - 包含 `Camera` 与 `CameraStream` 父子级联事务，`ExpectedRevision` 乐观锁更新
  - 验证命令：`python3 native/scripts/build.py go test -v -race ./internal/camera/...`
- [x] 改造 `internal/task/store.go`
  - 包含分析任务流转状态机、信道租约查询
  - 验证命令：`python3 native/scripts/build.py go test -v -race ./internal/task/...`
- [x] 改造 `internal/person/store.go`
  - 包含人脸底库关联、向量特征存储、业务外键与快照冗余更新
  - 验证命令：`go test -v -race ./internal/person/...`

### 批次 4: 高频感知流与读快照 Store 迁移
- [x] 改造 `internal/record/store.go`
  - 泛型优化 `listInReadSnapshot`
  - 迁移 `CreateRecognitionBatch`, `CreateAlarmBatch`, `CreateCaptureBatch` 为 `gorm.G[T].CreateInBatches`
  - 迁移各种按时间、按相机过滤查询
  - 验证命令：`go test -v -race ./internal/record/...`

### 批次 5: 规范与全量集成验证
- [x] 更新 `.trellis/spec/backend/database-guidelines.md`，确立 GORM Generics API 的工程守则
- [x] 运行全量验证：`make go-check`
- [x] 运行引擎与业务全量关联测试：`python3 native/scripts/build.py go test -race ./internal/...`

## 质量与回归门禁
- 严禁出现未捕获的数据竞争（`-race` 必须全绿）。
- 每批次必须在单测全绿之后才推进到下一个批次。
