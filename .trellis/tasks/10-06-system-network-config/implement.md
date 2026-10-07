# 边缘异构系统网络配置与两阶段安全回滚 · 执行计划 (Implementation Plan)

## Ordered Execution Checklist

### Phase 1: 后端底层抽象与多驱动适配器 (Backend Core & Providers)
- [x] 1.1 定义领域数据模型与 `NetworkProvider` 接口 (`internal/network/model.go`, `provider.go`)。
- [x] 1.2 实现跨平台网卡状态读取：Linux 基于 Netlink 与 `sysfs` (`reader_linux.go`)，提供开发机跨平台桩 (`reader_fallback.go`)。
- [x] 1.3 实现 NetworkManager 适配器 (`provider_nm.go`)：封装 D-Bus/nmcli 连接操作。
- [x] 1.4 实现 systemd-networkd 适配器 (`provider_systemd.go`)：读写 `.network` 配置与 reload。
- [x] 1.5 实现外部 Hook 脚本适配器 (`provider_script.go`) 与 Mock 适配器。
- [x] 1.6 实现自适应探测工厂 (`detectProvider()`)，编写网卡读取与 Provider 单元测试。

### Phase 2: 两阶段看门狗状态机与服务编排 (Watchdog & Service)
- [x] 2.1 实现防掉电看门狗事务管理器 (`watchdog.go`)：持久化 `<data-dir>/network_transaction.json`、原子落盘、超时定时器。
- [x] 2.2 实现 `NetworkService` 业务编排 (`service.go`)：单默认网关互斥校验、IPv4 CIDR 校验、高熵 Token 签发、延时应用协调。
- [x] 2.3 实现轻量连通性探测器 (`ping.go`)：支持网关与目标 IP 的快速 ICMP 探测。
- [x] 2.4 实现 Fx 依赖注入生命周期 (`module.go`)：在 `OnStart` 钩子中执行开机自检与未确认事务自动回滚。
- [x] 2.5 编写看门狗状态机、防掉电回滚与业务编排单元测试。

### Phase 3: RESTful API 与 CLI 紧急救援 (HTTP Handlers & CLI)
- [x] 3.1 实现 Gin API 处理器 (`handler.go`)，添加符合规范的 Swaggo 2.0 注解与错误信封转换。
- [x] 3.2 注册路由：将接口挂载至 `protected_routes`，并将免密带 Token 确认接口挂载至 `public_routes`。
- [x] 3.3 在 `cmd/Zhulong/main.go` 中集成 `--reset-network` 标志位，实现硬件出厂 IP 紧急恢复命令。
- [x] 3.4 编写 API 端点集成测试与权限预检测试 (`handler_test.go`)。

### Phase 4: 前端界面与交互集成 (React & UI/UX)
- [x] 4.1 新增 `web/src/features/systemSettings` 特性目录，建立类型定义与 API 客户端 (`networkApi.ts`)。
- [x] 4.2 编写网卡状态卡片组件 `NetworkCard.tsx`（展示物理 MAC、Link 状态、当前网口高亮）。
- [x] 4.3 编写网卡配置编辑模态框 `NetworkEditModal.tsx`（DHCP/Static 切换、单网关互斥校验、Ping 诊断按钮）。
- [x] 4.4 编写迁移引导模态框 `NetworkMigrationModal.tsx` 与全局倒计时悬浮横幅 `WatchdogCountdownBanner.tsx`。
- [x] 4.5 编写 `SystemSettingsPage.tsx` 页面容器，更新 `Sidebar.tsx` 和 `App.tsx` 支持 `#settings` 路由。
- [x] 4.6 完善三语国际化资源（`en.json`, `zh-Hans.json`, `zh-Hant.json`），编写前端组件单元测试。

### Phase 5: 全链路质量验证与验收 (Quality Assurance)
- [x] 5.1 运行后端单元测试与竞态检测：`go test -race ./internal/network/...`。
- [x] 5.2 运行前端单元测试与构建验证：`pnpm test` 与 `pnpm build`。
- [x] 5.3 验证 Swagger 文档生成：`swag init`。
- [x] 5.4 验证 CLI 救援命令：`go run ./cmd/Zhulong --reset-network`。

---

## Validation Commands

```bash
# 后端编译与单元测试
go test -v -race ./internal/network/...

# 全量后端测试
go test -v -race ./...

# 前端单元测试与代码检查
cd web && pnpm test -- --run && pnpm lint && pnpm build && cd ..

# Swagger 规范生成验证
swag init -g cmd/Zhulong/main.go -o api/swagger
```

---

## Review Gates & Rollback Points

1. **Gate 1（驱动抽象与自适应探测）**：确保在 macOS/非 Linux 环境测试时无缝 fallback 到 MockProvider，不阻断日常研发与 CI 测试。
2. **Gate 2（状态机掉电安全性）**：测试断电模拟用例（写入事务文件后立即重启实例，验证 Fx OnStart 能否 100% 触发回滚）。
3. **Gate 3（前后端接口一致性）**：严格校验 `{ code, message, data }` 响应结构与 Token 免密确认流。
4. **Rollback 应急方案**：若底层网络驱动引入系统级不稳定，通过配置文件 `[network] enabled = false` 允许完全旁路禁用该模块。
