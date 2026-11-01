# 全面迁移 GORM Store 实现到 GORM Generics API (`gorm.G[T]`)

## 目标 (Goal)

将 Zhulong 后端领域持久化层（Store 实现）从旧式基于反射指针的 GORM 链式调用（如 `db.Where(...).First(&user)`、`db.Create(&camera)`）全面迁移到 GORM 官方原生的 **Generics API**（`gorm.G[T](db)`），提升类型安全、根治 SQL 条件累加污染隐患，并强制显式传递 `context.Context`，同时 100% 保持现有 Store 领域接口契约、SQLite WAL 性能与事务一致性。

## 需求范围 (Scope & Requirements)

1. **涉及模块范围**：
   - `internal/auth/store.go`
   - `internal/audit/store.go`
   - `internal/storage/store.go`
   - `internal/asyncjob/store.go`
   - `internal/gb28181/store.go`
   - `internal/spatial/store.go`
   - `internal/algorithm/store.go` & `internal/algorithm/impact_store.go`
   - `internal/camera/store.go`
   - `internal/task/store.go`
   - `internal/person/store.go`
   - `internal/record/store.go`
2. **保持领域契约与分层边界**：
   - 各领域的 Store 接口（如 `CameraStore`, `UserStore`）签名与结构体对外暴露完全保持不变，上层 Service / Handler / Controller 零改动、零破坏。
   - 错误包装行为 100% 保持兼容（`errors.Is(err, gorm.ErrRecordNotFound)` 转换为对应的领域错误，如 `ErrUserNotFound`, `ErrCameraNotFound` 等）。
3. **遵循 SQLite 边缘运行性能铁律**：
   - 保持所有事务内单连接写入约束（`db.Transaction`）。
   - 保持 `record` 表批量写入优化（`CreateInBatches(..., 100)`）。
   - 保持只读快照分页事务（`listInReadSnapshot`）机制及 `captured_at DESC, id DESC` 索引顺序，严禁触发 SQLite `USE TEMP B-TREE`。
4. **规范与文档更新**：
   - 更新 `.trellis/spec/backend/database-guidelines.md`，确立 GORM Generics API 的工程范式与禁忌。

## 验收标准 (Acceptance Criteria)

- [x] 所有被迁移的 Store 单元测试（`*_test.go`）100% 通过且全加 `-race` 检查，无数据竞争。
- [x] 边缘性能和 PRAGMA 断言通过，`database_test.go` 与 `pagination_test.go` 保持全绿。
- [x] `make go-check`（编译与 vet）完全通过，无未捕获的错误或弃用警告。
- [x] 所有对外 HTTP API 测试或业务流程保持原有行为与错误码一致。
- [x] `.trellis/spec/backend/database-guidelines.md` 已追加 GORM Generics API 规范章节。
