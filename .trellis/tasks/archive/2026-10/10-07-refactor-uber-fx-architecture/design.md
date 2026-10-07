# 技术设计：全面重构 Uber Fx 依赖注入与生命周期治理架构 (Design Document)

## 1. 架构总览与核心演进

本次重构旨在将单体式胶水装配彻底改造为标准的 Uber Fx 模块化依赖注入拓扑，并保持底层异构系统无副作用构造与级联安全回滚能力。

```
                    ┌────────────────────────────┐
                    │      cmd/Zhulong/main      │
                    │         fx.New(...)        │
                    └─────────────┬──────────────┘
                                  │
    ┌─────────────────────────────┼─────────────────────────────┐
    ▼                             ▼                             ▼
┌──────────────────────────┐ ┌──────────────────────────┐ ┌──────────────────────────┐
│     databaseModule       │ │       engineModule       │ │       loggerModule       │
│ • database.New           │ │ • engine.New             │ │ • newLogger              │
│ • fx.As(DBProvider)      │ │                          │ │                          │
└────────────┬─────────────┘ └────────────┬─────────────┘ └──────────────────────────┘
             │                            │
             ├────────────────────────────┼─────────────────────────────┐
             ▼                            ▼                             ▼
┌──────────────────────────┐ ┌──────────────────────────┐ ┌──────────────────────────┐
│        authModule        │ │       cameraModule       │ │       auditModule        │
│ • NewUserStore           │ │ • NewCameraStore         │ │ • NewStore               │
│ • NewMemorySessionStore  │ │ • NewKeyManager/Cipher   │ │ • NewService             │
│ • NewAuthService         │ │ • NewStateRegistry/Hub   │ │ • NewHandler             │
│ • NewHandler             │ │ • NewStreamHub/Probe/Svc │ │                          │
│   (-> group:public)      │ │ • NewLifecycleManager    │ │   (-> group:protected)   │
│                          │ │ • NewHandler             │ │                          │
│                          │ │   (-> group:protected)   │ │                          │
└──────────────────────────┘ └──────────────────────────┘ └──────────────────────────┘
             │                            │                             │
             └────────────────────────────┼─────────────────────────────┘
                                          ▼
                             ┌──────────────────────────┐
                             │        httpModule        │
                             │ • newRouter              │
                             │   (group:public/protect) │
                             │ • webui.FileSystem       │
                             │ • newHTTPServer          │
                             └────────────┬─────────────┘
                                          │
                                          ▼
                             ┌──────────────────────────┐
                             │      runtimeModule       │
                             │ • newLifecycleRuntime    │
                             │   (fx.In 依赖注入声明)    │
                             │ • registerLifecycle      │
                             │   (OnStart/OnStop 钩子)  │
                             └──────────────────────────┘
```

---

## 2. 核心设计要点

### 2.1 数据库访问抽象：`database.DBProvider`
废弃松散易错的 `getDB func() *gorm.DB` 闭包，在 `internal/database` 中定义官方契约：

```go
// DBProvider abstracts access to the underlying *gorm.DB.
type DBProvider interface {
    DB() *gorm.DB
}
```

- `*database.Store` 原生实现 `DBProvider`；
- `auth.NewUserStore(provider database.DBProvider)`；
- `camera.NewCameraStore(provider database.DBProvider)`；
- `audit.NewStore(provider database.DBProvider)`；
- 在 Fx 中注册时：
  ```go
  fx.Provide(
      fx.Annotate(
          database.New,
          fx.As(new(database.DBProvider)),
          fx.As(new(databaseLifecycle)), // 用于 runtime
      ),
  )
  ```
  彻底消灭调用 `dbStore.DB()` 引发的空指针风险，且具备良好的静态类型约束和单元测试 mock 能力。

### 2.2 拆解单体 `newServices` 为命名 Fx Modules
在 `internal/app/` 建立模块化定义（或组织良好的 Providers）：
- **`databaseModule`**：根据 `Config.DataDir` 与 `*zap.Logger` 构造 `*database.Store`，绑定 `DBProvider` 与 `databaseLifecycle`。
- **`engineModule`**：构造 `*engine.Engine`，绑定 `engineLifecycle`。
- **`auditModule`**：注入 `DBProvider` 构造 `audit.Store`、`audit.Service`、`audit.Handler`，并将 Handler 自动注入 `protected_routes`。
- **`authModule`**：注入 `DBProvider` 构造 UserStore、SessionStore、AuthService 与 AuthHandler，并将 Handler 自动注入 `public_routes`。
- **`cameraModule`**：装配 Camera 内部子组件（LazyCipher, KeyManager, StateRegistry, EventHub, StreamHub, Prober, HealthScheduler, CameraService, LifecycleManager, Handler），并将 Handler 自动注入 `protected_routes`。
- **`httpModule`**：注入 Value Groups 路由集、文件系统，构造 Gin 引擎与 `*http.Server`。
- **`runtimeModule`**：通过 `fx.In` 依赖注入直接装配 `lifecycleRuntime`，注册生命周期 `registerLifecycle`。

### 2.3 路由值组自动收集 (Value Groups)
利用 Fx 的 `fx.ResultTags` 自动将各 Handler 归类到对应路由组，消灭大结构体 `servicesOut` 的机械透传：

```go
fx.Provide(
    fx.Annotate(
        newAuthHandler,
        fx.As(new(RouteRegistrar)),
        fx.ResultTags(`group:"public_routes"`),
    ),
    fx.Annotate(
        newCameraHandler,
        fx.As(new(RouteRegistrar)),
        fx.ResultTags(`group:"protected_routes"`),
    ),
    fx.Annotate(
        newAuditHandler,
        fx.As(new(RouteRegistrar)),
        fx.ResultTags(`group:"protected_routes"`),
    ),
)
```

### 2.4 生命周期与级联安全回滚保证 (Lifecycle Hardening)
- 继续保持 `lifecycleRuntime` 的**确定性强时序状态机**，但构造函数的入参由之前的单体大对象 `*applicationServices` 升级为松耦合的 `runtimeParams (fx.In)`：
  ```go
  type runtimeParams struct {
      fx.In

      Database databaseLifecycle
      Native   engineLifecycle
      Camera   cameraLifecycle
      Audit    auditLifecycle
      Server   *http.Server
      Logger   *zap.Logger
  }
  ```
- 严格保持已验证的启动顺序与逆序错误回滚机制：
  1. `database.OpenAndMigrate` ➔ 2. `audit.Start` ➔ 3. `native.Start` ➔ 4. `camera.InitCipher & Start` ➔ 5. `http.Listen & Serve`。
  任一阶段失败均严格逆序回滚前面所有已启动组件。

---

## 3. 兼容性与演进策略

1. **测试兼容性**：
   - `runtime_test.go` 无需改变测试意图，由于 `lifecycleRuntime` 接口解耦为清晰的 `databaseLifecycle`, `engineLifecycle` 等，测试用例不仅完全兼容，且更加纯粹；
   - `app_test.go` 新增 `TestAppDependencyGraph`，使用 `fx.ValidateApp` 对重构后的整个 Fx 拓扑进行依赖完整性验证。
2. **零运行时破坏**：
   - 外部调用（`cmd/Zhulong/main.go`）调用的 `app.New(cfg)` 接口保持不变，启动和关机行为完全无感平滑迁移。
