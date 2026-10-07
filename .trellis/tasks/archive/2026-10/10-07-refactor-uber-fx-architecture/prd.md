# PRD：全面重构 Uber Fx 依赖注入与生命周期治理架构 (Refactor Uber Fx Architecture)

## 1. 目标与愿景 (Goal)

彻底拥抱 Uber Fx 最佳工程实践与规范，解决当前后端核心装配中的「上帝函数」、「间接延迟闭包」及「胶水代码臃肿」问题。
将原本集中在 `internal/app/app.go` 中的单体式装配拆解为高内聚、低耦合的模块化 Fx 架构；在**严格维持嵌入式无副作用构造与异构硬件/CGO 逆序优雅回滚安全**的前提下，建立规范统一的依赖注入与生命周期治理体系。

## 2. 核心问题与重构动因 (Motivation)

1. **单体上帝函数臃肿**：`internal/app/app.go` 中的 `newServices` 函数超过百行，手工 `new` 出所有业务 Store、Service、Hub 和 Handler，破坏了 DI 容器自动推导 DAG 的初衷。
2. **反模式的函数值闭包传递**：各业务模块 Store 接收裸函数 `getDB func() *gorm.DB`，导致类型系统约束丢失、容易发生 `dbStore.DB()` 装配空指针事故，且每个 Store 必须充斥重复的防御性运行时判空。
3. **自研状态机与 Fx 生命周期机制割裂**：`lifecycleRuntime` 手工维护了各服务的启动与停止状态标志位，未完全发挥 Uber Fx 原生依赖拓扑回滚的能力；装配代码与生命周期逻辑混杂。

## 3. 需求范围与核心规范 (Requirements)

### 3.1 模块化解耦与细粒度装配 (Fx Modules)
- 将单体 `newServices` 彻底拆解为按领域划分的独立 Fx Module / Provider 集：
  - `databaseModule`：负责数据库存储与迁移。
  - `engineModule`：负责 C++ Native 引擎。
  - `authModule`：负责用户凭据、会话与认证服务。
  - `cameraModule`：负责摄像头管理、密钥管理、探测与流媒体调度。
  - `auditModule`：负责轻量级安全审计。
  - `httpModule`：负责 Gin 引擎、中间件、SPA 静态资源与 HTTP Server 托管。
- 业务包严格遵守 **「零 Fx 依赖」** 原则：`internal/auth`、`internal/camera` 等业务包只保留纯 Go 构造函数，所有 Fx 装配逻辑收敛在 `internal/app/`。

### 3.2 数据库依赖与访问规范 (Database Access Contract)
- 废弃松散无序的裸 `func() *gorm.DB` 签名。
- 确立统一、健壮的数据库连接提供者契约（如 `database.DBProvider` 接口或清晰的抽象），统一收敛 `DB()` 获取与就绪性检查，兼顾「构造期无 I/O」与「静态类型安全」。

### 3.3 生命周期与确定性逆序回滚 (Lifecycle & Cascading Rollback)
- **构造函数无副作用**：所有组件在 `fx.New` 构造阶段仅分配内存与绑定依赖，严禁在构造函数中打开 SQLite 文件、创建硬件句柄或监听端口。
- **确定性启动顺序**：保证启动时序严格满足异构依赖约束：
  `Database (Open & Migrate) ➔ Audit (Start) ➔ Native Engine (Start) ➔ Camera (InitCipher & Start) ➔ HTTP Server (Listen & Serve)`。
- **级联逆序安全回滚**：任何启动阶段发生错误，已启动的硬件、线程与连接资源必须**严格按逆序完成安全清理与释放**（HTTP ➔ Camera ➔ Native ➔ Audit ➔ Database ➔ Logger Sync）。

### 3.4 路由值组自动收集 (Value Groups)
- 延续并规范 `group:"public_routes"` 与 `group:"protected_routes"` 的自动收集模式，任何新增业务模块只需提供 `RouteRegistrar` 即可自动挂载，全局路由器保持零修改。

## 4. 验收标准 (Acceptance Criteria)

- [x] **拆解完成**：`internal/app/app.go` 中的单体 `newServices` 函数被完全消除，拆分为按模块划分的清晰 Providers。
- [x] **类型契约统一**：各模块 Store 不再使用松散的裸 `func() *gorm.DB`，统一使用规范的数据库提供者接口或类型。
- [x] **生命周期测试 100% 通过**：`internal/app/runtime_test.go` 中所有启动顺序、迁移失败拦截、HTTP 优雅排空、监听失败回滚、硬件释放测试全量通过且保持严格时序不变。
- [x] **依赖图静态校验测试通过**：`app_test.go` 中通过 `fx.ValidateApp` 静态校验无死锁、无缺失依赖、无循环引用。
- [x] **全栈回归测试通过**：全库所有后端测试通过（含 `-race` 检查），前端与构建产物无任何破损。
