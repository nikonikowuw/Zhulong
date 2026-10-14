# 依赖注入与应用生命周期规范 (Uber Fx)

> Uber Fx 依赖注入、生命周期钩子（OnStart/OnStop）编排及依赖图验证。

---

## 1. 核心隔离原则

1. **业务包零 Fx 依赖**：业务模块（`internal/camera` 等）仅暴露纯粹的 Go 构造函数，严禁在业务包中导入 `go.uber.org/fx`。
2. **装配收敛在 `internal/app`**：Fx 的 Provider 声明与应用编排集中在 `internal/app/`。
3. **构造函数无副作用**：构造函数只做内存分配与依赖绑定，**严禁启动 goroutine、发起网络请求或加载大模型**；所有长时任务交由生命周期钩子托管。

---

## 2. 当前骨架生命周期时序

| 阶段 | 严格顺序 |
| --- | --- |
| **启动 (`lifecycleRuntime.Start`)** | 1. 打开 GORM/SQLite 并执行嵌入式版本化迁移 (失败则阻断) ➔ 2. 创建并启动 Native C++ Engine ➔ 3. 校验/初始化相机 AES-256-GCM 密钥（已有密文而密钥缺失则阻断）➔ 4. 启动 Camera HealthScheduler ➔ 5. 绑定 TCP listener 并启动 Gin HTTP Serve |
| **停止 (`lifecycleRuntime.Stop`)** | 1. 优雅关闭 HTTP 并等待 Serve 退出 ➔ 2. 关闭 SSE Hub 与停止 Camera HealthScheduler（含 StreamHub 分发器与 AI 流） ➔ 3. 停止 Algorithm Supervisor 所有运行中的子进程 ➔ 4. 停止并销毁 C++ opaque handle ➔ 5. 关闭 SQLite pool ➔ 6. Sync Zap |

⚠️ **停机顺序是硬约束，不得静默调整**：`camera.Stop` 必须先于 `algorithm.Stop`，`algorithm.Stop` 又必须先于 `native.Close`（即 `engine.close`）。理由是 native `Engine` 持有 per-stream 的 `FrameBroker`，而 runner 子进程是它的 IPC 客户端：

- 若 `native.Close` 先于 `algorithm.Stop`，runner 仍持有 shm 映射而 broker 已随引擎析构；
- 若 `algorithm.Stop` 先于 `camera.Stop`，AI 消费者会先消失、broker 先被销毁，而 runner 仍在收帧。

该顺序由 `internal/app/runtime_test.go` 的 `TestAIShutdownOrderPrecedesNativeClose` 守护：被改坏时测试会失败，而不是静默产生 use-after-free。

启动的后续步骤失败时，`lifecycleRuntime.Start` 在返回错误前按逆序清理已打开资源，因为 Fx 不会对失败的 `OnStart` 自动调用该 hook 的 `OnStop`。正常停机时先排空 HTTP 请求，再释放 native 与数据库资源。当前 native stub 不创建 worker thread、媒体流水线或 NPU context；未来增加后台线程时，必须先停止、唤醒并 Join 后才能销毁句柄。

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


