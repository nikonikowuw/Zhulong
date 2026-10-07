# 执行计划：全面重构 Uber Fx 依赖注入与生命周期治理架构 (Implementation Plan)

## 任务拆解与执行顺序

### 阶段一：数据库提供者契约与业务 Store 重构
- [x] **1.1 定义 `database.DBProvider` 接口**
  - 在 `internal/database/store.go` 中声明 `DBProvider` 接口，确认 `*database.Store` 隐式实现该接口。
  - 编写/确认 `internal/database/store_test.go`。
- [x] **1.2 改造 `internal/auth` Store 契约**
  - 修改 `internal/auth/store.go`，将 `NewUserStore(getDB func() *gorm.DB)` 改为 `NewUserStore(provider database.DBProvider)`。
  - 同步更新 `internal/auth/store_test.go`、`handler_test.go`、`service_test.go` 中的单测调用。
  - 验证命令：`python3 native/scripts/build.py go test ./internal/auth/...`
- [x] **1.3 改造 `internal/camera` Store 契约**
  - 修改 `internal/camera/store.go`，将 `NewCameraStore(getDB func() *gorm.DB)` 改为 `NewCameraStore(provider database.DBProvider)`。
  - 同步更新 `internal/camera/store_test.go` 及相关测试。
  - 验证命令：`python3 native/scripts/build.py go test ./internal/camera/...`
- [x] **1.4 改造 `internal/audit` Store 契约**
  - 修改 `internal/audit/store.go`，将 `NewStore(getDB func() *gorm.DB)` 改为 `NewStore(provider database.DBProvider)`。
  - 同步更新 `internal/audit/store_test.go` 及测试调用。
  - 验证命令：`python3 native/scripts/build.py go test ./internal/audit/...`

### 阶段二：装配层彻底解耦与模块化重构 (internal/app)
- [x] **2.1 重构 `lifecycleRuntime` 依赖声明**
  - 在 `internal/app/runtime.go` 中引入 `runtimeParams`（嵌入 `fx.In`），解绑臃肿的 `*applicationServices` 结构体。
  - 更新 `newLifecycleRuntime(p runtimeParams) *lifecycleRuntime`。
  - 确保 `internal/app/runtime_test.go` 的各项单测完全兼容并通过。
  - 验证命令：`python3 native/scripts/build.py go test ./internal/app/... -run TestMigrationFailure`
- [x] **2.2 拆解单体 `newServices` 为模块化 Providers**
  - 在 `internal/app/` 中拆分各模块的装配逻辑：
    - Database Provider
    - Engine Provider
    - Auth Provider & Routes
    - Audit Provider & Routes
    - Camera Provider & Routes
  - 使用 `fx.Annotate` 配合 `fx.As` 和 `fx.ResultTags` 将各模块路由直接送入 `group:"public_routes"` 与 `group:"protected_routes"`。
  - 彻底删除废弃的 `newServices` 与 `servicesOut`。
- [x] **2.3 组装全新 `app.Module`**
  - 在 `internal/app/app.go` 中聚合各模块化 Option。
  - 保持 `app.New(config Config) *fx.App` 对外接口完全兼容。

### 阶段三：静态图校验与生命周期测试
- [x] **3.1 编写 Fx 依赖图静态校验测试**
  - 在 `internal/app/app_test.go` 中新增 `TestAppDependencyGraph(t *testing.T)`，使用 `fx.ValidateApp(...)` 验证无缺失依赖与循环引用。
  - 验证命令：`python3 native/scripts/build.py go test ./internal/app/... -run TestAppDependencyGraph`
- [x] **3.2 全量生命周期测试回归**
  - 运行 `internal/app/runtime_test.go` 中的全量测试用例，确保级联回滚与优雅停机行为严格一致。
  - 验证命令：`python3 native/scripts/build.py go test -v ./internal/app/...`

### 阶段四：全系统回归与产物构建
- [x] **4.1 全栈并发与数据竞争检查**
  - 验证命令：`python3 native/scripts/build.py go test -race ./internal/...`
- [x] **4.2 二进制编译与启动验证**
  - 验证命令：`python3 native/scripts/build.py go build ./cmd/Zhulong`

## 回滚与风险控制 (Rollback Strategy)
- 保持 `app.New` 函数签名与返回值（`*fx.App`）不变，外部入口 `cmd/Zhulong/main.go` 保持零变更。
- 本次重构仅影响内部依赖传递机制与装配拓扑，不改变任何 HTTP API 接口、数据表结构或运行时对外协议。
