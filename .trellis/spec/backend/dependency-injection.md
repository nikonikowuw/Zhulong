# 依赖注入与应用生命周期规范 (Uber Fx)

> Uber Fx 依赖注入、生命周期钩子（OnStart/OnStop）编排及依赖图验证。

---

## 1. 核心隔离原则

1. **业务包零 Fx 依赖**：业务模块（`internal/camera` 等）仅暴露纯粹的 Go 构造函数，严禁在业务包中导入 `go.uber.org/fx`。
2. **装配收敛在 `internal/app`**：Fx 的 Provider 声明与应用编排集中在 `internal/app/`。
3. **构造函数无副作用**：构造函数只做内存分配与依赖绑定，**严禁启动 goroutine、发起网络请求或加载大模型**；所有长时任务交由生命周期钩子托管。

---

## 2. 当前宿主生命周期顺序

`lifecycleRuntime` 管理多个领域服务，下面是当前实现顺序。新增或调整生命周期 hook 时，应同步更新此表与 `internal/app/runtime_test.go` 的顺序测试。

| 阶段 | 当前顺序 |
| --- | --- |
| **启动 (`lifecycleRuntime.Start`)** | 日志输出准备 ➔ 数据库打开与迁移（失败阻断）➔ 区域配置缓存重载（失败仅告警）➔ Audit ➔ Network ➔ SystemTime ➔ Storage ➔ Person ➔ Snapshot worker ➔ Record ➔ native 日志队列与回调注册 ➔ Native Engine ➔ Camera 密钥初始化与健康调度器 ➔ Algorithm ➔ TCP listener 与 HTTP Serve。Network、SystemTime、Storage、Person、Record、Algorithm 的启动错误当前记录告警后继续；Native、日志回调、Camera 初始化和 listener 等关键步骤失败会回滚已启动资源。
| **停止 (`lifecycleRuntime.Stop`)** | HTTP 优雅关闭并等待 Serve ➔ Camera ➔ Algorithm ➔ Person ➔ Record ➔ Snapshot worker 排空并停止 ➔ Storage ➔ SystemTime ➔ Network ➔ Audit ➔ Native Engine / 日志回调 ➔ SQLite ➔ 同步并关闭日志资源。

⚠️ **有数据依赖的相对顺序是硬约束**：`camera.Stop` 先于 `algorithm.Stop`，`algorithm.Stop` 先于 `native.Close`；`record.Stop` 先于 Snapshot worker 停止，Snapshot worker 必须在 Storage 与 Native 仍可用时排空。不得仅凭 Fx 注册顺序推断这些依赖。

`internal/app/runtime_test.go` 应守护关键顺序（包括 AI 消费者停止先于 Native 销毁）；新增后台 worker 时，应明确其依赖、取消方式、排空行为与关闭位置。

启动后续步骤失败时，`lifecycleRuntime.Start` 在返回错误前清理已启动资源，因为 Fx 不会对失败的 `OnStart` 自动调用该 hook 的 `OnStop`。当前 Engine 已包含媒体流、FrameBroker 与异步处理能力，不得再按“native stub 无 worker/流水线”的假设设计关闭行为。

---

## 3. 协程上下文与停机安全

- **长驻协程上下文**：后台持续运行的协程必须基于**应用根 Context**，**严禁使用 `OnStart` 传入的 `ctx`**（`OnStart` 的 `ctx` 启动完成后会立即取消）。
- **CGO 停机超时**：`OnStop` 传出的 `ctx` 有 Deadline 超时。底层 C++ Pipeline 必须有明确的取消标志位与唤醒机制，并在释放原生内存前真正完成线程 `join()`。

---

## 4. 依赖图静态校验测试

在 `internal/app/app_test.go` 中加入验证测试，防止生产部署因缺少依赖或循环引用崩溃：

```go
package app_test

import (
 "testing"
 "go.uber.org/fx"
 "github.com/nikonikowuw/Zhulong/internal/app"
)

// Internal `app` package test.
func TestAppDependencyGraph(t *testing.T) {
    if err := fx.ValidateApp(fx.Supply(app.DefaultConfig()), app.Module, fx.NopLogger); err != nil {
        t.Fatalf("Fx dependency graph validation failed: %v", err)
    }
}
```

---

## 5. 模块化路由自动装配 (Value Groups)

为避免每次新增业务模块都修改全局 `router.go`，采用 Uber Fx 值组（Value Groups / Multibindings）进行解耦：

1. **统一注册接口**：在 `internal/app/router.go` 定义 `RouteRegistrar` 接口（`RegisterRoutes(rg *gin.RouterGroup)`）。业务 Handler 直接实现该接口（如 `camera.Handler`、`auth.Handler`）。
2. **公开、保护与上传路由分流**：
   - 公开端点（如初始化、登录）：在装配中作为 `RouteRegistrar` 打标 `group:"public_routes"`，挂载至 `/api/v1` 根组。
   - 受保护端点（如摄像机、录像、AI）：在装配中打标 `group:"protected_routes"`，自动挂载至受 `auth.RequireAuth` 保护的子路由组。
   - **大体积上传端点**：打标 `group:"upload_routes"`，挂载至**另一个** `/api/v1` 组，该组单独施加上传限额与独立的 `AccessLog` / `Audit` / `RequireAuth`。受保护组继承的 1 MiB `MaxBodyLimit()` 会毙掉合法上传，且**无法事后放宽**（Gin 在 `Group()` 时即求值中间件）。判定标准、代码示例与回归护栏见 [中间件规范 §1](./middleware-guidelines.md)。
   - 一个 Handler **可以同时**注册到多个组（如 `person.Handler` 同时提供 `ProtectedRoutes` 与 `UploadRoutes`），此时需要为上传端点单独写一个 `RegisterUploadRoutes` + `RouteRegistrar` 适配器，避免把大体积端点误挂到受保护组。
3. **上帝路由器解耦**：`newRouter` 仅接收 `p.PublicRoutes`、`p.ProtectedRoutes` 与 `p.UploadRoutes` 切片并遍历挂载，不引用任何特定业务包（如 `internal/camera`），新增模块时 `router.go` 保持零修改。

---

## 6. 数据库连接提供者契约 (DBProvider) 与领域模块化 (Domain fx.Modules)

1. **统一 `database.DBProvider` 契约**：
   - 业务 Store（如 `auth`、`camera`、`audit`）禁止接收松散的裸闭包 `func() *gorm.DB`，统一依赖 `database.DBProvider` 接口（`DB() *gorm.DB`）。
   - `*database.Store` 原生实现 `DBProvider`，在构造期仅做类型绑定，保证构造无副作用并在执行 SQL 时完成动态判空。
2. **细粒度领域 `fx.Module` 组装**：
   - `internal/app` 彻底摒弃单体上帝装配函数，按领域划分命名模块：`databaseModule`、`engineModule`、`auditModule`、`authModule`、`cameraModule`、`httpModule` 与 `runtimeModule`。
   - `lifecycleRuntime` 使用 `runtimeParams (fx.In)` 声明依赖，实现与上层具体实现类的松耦合。


