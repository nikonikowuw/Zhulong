# 边缘异构系统对时服务与硬件时钟同步 · 执行计划 (Implementation Plan)

## Ordered Execution Checklist

### Phase 1: 跨平台驱动抽象与 Go 原生 SNTP 引擎 (ClockDriver & SNTP Client)
- [ ] 1.1 定义领域模型与 `ClockDriver` 接口 (`internal/systemtime/model.go`, `driver.go`)。
- [ ] 1.2 实现跨平台 Stub 内存驱动 (`internal/systemtime/driver_stub.go`，`!linux` 标签)，支持在 macOS 开发机与 CI 环境自测。
- [ ] 1.3 实现 Linux 原生驱动 (`internal/systemtime/driver_linux.go`，`linux` 标签)：系统调用 `clock_settime`、`adjtimex`、`/dev/rtc*` ioctl 以及 `/etc/localtime` 原子软链接与 `tzset()` 刷新。
- [ ] 1.4 实现 RFC 4330 SNTPv4 客户端与时间戳计算核心 (`internal/systemtime/sntp.go`)，支持 RTT/Offset 计算与主备服务器顺序降级探测。
- [ ] 1.5 编写 SNTP 编解码、时间戳数学计算与驱动抽象单元测试 (`sntp_test.go`, `driver_test.go`)。

### Phase 2: 两阶段状态机、业务服务编排与 Fx 生命周期 (Service & Lifecycle)
- [ ] 2.1 实现两阶段时钟安全状态机 (`internal/systemtime/statemachine.go`)：冷启动无限制 Step、稳态 $<500\text{ms}$ Slew、稳态 $\ge 10\text{min}$ 防暴冲复核机制。
- [ ] 2.2 编写数据库版本化迁移脚本 (`internal/database/migrations/000005_create_system_time_config.up.sql` 及 `.down.sql`)。
- [ ] 2.3 实现 GORM 持久化仓库 (`internal/systemtime/repository.go`)。
- [ ] 2.4 实现 `TimeService` 核心业务编排与后台定时 NTP 轮询 Worker (`internal/systemtime/service.go`)。
- [ ] 2.5 集成安全审计日志联动与 Uber Fx 模块 (`internal/systemtime/module.go`)：实现 `OnStart` 开机 RTC 自愈、时区加载、Worker 启停与 `OnStop` 优雅清理。
- [ ] 2.6 编写业务服务、两阶段状态机与开机自愈单元测试 (`service_test.go`)。

### Phase 3: RESTful API、路由挂载与 Swaggo 文档 (HTTP Handlers & Docs)
- [ ] 3.1 实现 Gin API 处理器 (`internal/systemtime/handler.go`)，提供查询状态、更新配置、立即对时、手动/浏览器对时接口，添加规范 Swaggo 2.0 注解。
- [ ] 3.2 在 `internal/app/app.go` 依赖注入图谱中注册 `systemtime` 模块与受保护路由。
- [ ] 3.3 运行 `make api-docs` 生成 Swagger 文档并更新 API 规范。
- [ ] 3.4 编写 HTTP Handler 单元测试与端点集成测试 (`handler_test.go`)。

### Phase 4: 前端系统时间配置看板 (React Frontend & UI/UX)
- [ ] 4.1 新建 `web/src/features/systemSettings/time/` 模块，定义 TypeScript 类型并封装 API 客户端 (`timeApi.ts`)。
- [ ] 4.2 编写实时走秒时钟与格式化 Hook (`useLiveClock.ts`) 及 TanStack Query 钩子 (`useSystemTime.ts`)。
- [ ] 4.3 编写实时状态与硬件指示卡片组件 (`TimeDashboardCard.tsx`)。
- [ ] 4.4 编写 NTP 自动对时配置表单 (`NtpConfigForm.tsx`)。
- [ ] 4.5 编写手动对时与「一键同步浏览器时间」高亮组件 (`ManualTimeForm.tsx`) 与 IANA 时区选择器 (`TimezoneSelect.tsx`)。
- [ ] 4.6 集成系统设置页面路由与侧边栏入口，完善中英繁三语国际化字典（`en.json`, `zh-Hans.json`, `zh-Hant.json`）。
- [ ] 4.7 编写前端组件单元测试与 Mock API 测试。

### Phase 5: 全链路质量验证与系统验收 (Quality Assurance)
- [ ] 5.1 运行后端专用模块测试与竞态检测：`go test -v -race ./internal/systemtime/...`。
- [ ] 5.2 运行全量后端测试：`go test -v -race ./...`。
- [ ] 5.3 运行前端测试与构建检查：`cd web && pnpm test -- --run && pnpm lint && pnpm build && cd ..`。
- [ ] 5.4 验证全系统 Makefile 构建与冒烟测试检查。

---

## Validation Commands

```bash
# 1. 后端 systemtime 模块单元测试与竞态检测
go test -v -race ./internal/systemtime/...

# 2. 全量后端测试
go test -v -race ./...

# 3. 重新生成 Swagger 文档
make api-docs

# 4. 前端单元测试、ESLint 与生产构建
cd web && pnpm test -- --run && pnpm lint && pnpm build && cd ..

# 5. 全项目冒烟与集成检查
make check
```

---

## Review Gates & Rollback Points

1. **Gate 1（跨平台编译与隔离）**：确保在 macOS 本地执行 `go test ./...` 毫不受 Linux 系统调用限制，`driver_stub.go` 与 `driver_linux.go` 的 build tags 隔离严格有效。
2. **Gate 2（时钟回拨与 PTS 安全）**：严格断言稳态下毫秒级偏移通过 `adjtimex` 处理，防止媒体流 PTS 倒退；断言冷启动状态下大偏移能正常阶跃恢复。
3. **Gate 3（前后端接口一致性）**：严格校验 `{ code: "OK", message: "...", data: { ... } }` 统一信封格式，错误时 `data: null`。
4. **Rollback 应急方案**：若底层时钟调整在某些定制 Linux 内核上出现意外行为，可通过配置或启动参数降级为只读监控模式，不调用侵入式系统调用。
