# 边缘异构系统媒体存储配置与动态生命周期管理 · 执行计划 (Implementation Plan)

## Ordered Execution Checklist

### Phase 1: 后端核心领域模型与路径探针 (Backend Core & Inspector)
- [ ] 1.1 定义领域数据模型与配置结构体 (`internal/storage/model.go`)。
- [ ] 1.2 实现配置持久化仓储 (`internal/storage/store.go`)：基于 SQLite `system_metadata`，提供默认配置初始化、读取与原子更新。
- [ ] 1.3 实现跨平台存储探针：
  - Linux 基于 `unix.Statfs` 与 `unix.Stat` 获取容量、挂载点与设备号 (`inspector_linux.go`)；
  - 提供非 Linux 环境编译兼容桩 (`inspector_fallback.go`)，支持本地开发与 CI。
- [ ] 1.4 实现路径可写性预检与外挂盘掉线防穿透检测 (`inspector.go`)。
- [ ] 1.5 编写探针与配置持久化单元测试 (`inspector_test.go`, `store_test.go`)。

### Phase 2: 双水位回差引擎与极限熔断保护 (Cleaner & Emergency Gate)
- [ ] 2.1 实现极限容量熔断器 (`gate.go`)：基于高水位与最小空闲空间判断，提供原子 `CanWrite() bool` 判决与状态切换。
- [ ] 2.2 实现双水位滞后回差调度器 (`cleaner.go`)：高水位 (90%) 触发、低水位 (80%) 停止休眠，阶梯淘汰（过期 exports -> 未加锁 recordings -> 告警保护）。
- [ ] 2.3 实现批次步进流控与礼貌避让：每批删除 20~50 个文件后执行毫秒级休眠让渡，杜绝 ext4/jbd2 日志锁死。
- [ ] 2.4 实现服务编排层 (`service.go`)：串联探针、存储仓储、清理器与熔断器，支持路径热切流与手动触发清理。
- [ ] 2.5 编写双水位回差、批次流控与熔断保护单元测试 (`cleaner_test.go`, `service_test.go`)。

### Phase 3: RESTful API 与 Fx 依赖注入集成 (HTTP Handlers & Fx Module)
- [ ] 3.1 实现 Gin API 处理器 (`handler.go`)，覆盖状态大盘查询、策略配置修改、路径测试与手动清理触发，附带 Swaggo 2.0 注解。
- [ ] 3.2 注册路由：将接口挂载至 `internal/app` 受保护路由。
- [ ] 3.3 实现 Fx 依赖注入生命周期钩子 (`module.go`)：在 `OnStart` 启动后台低频巡检协程与状态刷新，在 `OnStop` 优雅关闭。
- [ ] 3.4 编写 HTTP API 端点集成测试与边界参数校验测试 (`handler_test.go`)。

### Phase 4: React 前端界面与交互集成 (React & UI/UX)
- [ ] 4.1 在 `web/src/features/systemSettings` 中建立存储类型定义与 API 客户端 (`storageApi.ts`)。
- [ ] 4.2 编写容量大盘组件 `StorageCapacityBar.tsx`：多色分段条形图展示各分类占比与当前运行健康徽标。
- [ ] 4.3 编写渐进式配置表单 `StorageConfigForm.tsx`：核心区配置存储路径（带实时检测按钮）与保留天数；高级折叠面板微调高低水位与紧急熔断阈值。
- [ ] 4.4 编写手动清理确认弹窗 `ManualCleanupDialog.tsx`。
- [ ] 4.5 接入系统设置一级页面与侧边栏路由联动（支持 `#settings/storage` 或标签切换）。
- [ ] 4.6 完善三语国际化字典（`en.json`, `zh-Hans.json`, `zh-Hant.json`），编写前端组件单元测试。

### Phase 5: 全链路质量验证与回归测试 (Quality Assurance)
- [ ] 5.1 运行后端单元测试与竞态检测：`go test -v -race ./internal/storage/...`。
- [ ] 5.2 运行全量后端测试：`go test -v -race ./...`。
- [ ] 5.3 运行前端单元测试与构建检查：`cd web && pnpm test -- --run && pnpm lint && pnpm build && cd ..`。
- [ ] 5.4 验证 Swagger API 规范同步：`swag init -g cmd/Zhulong/main.go -o api/swagger`。

---

## Validation Commands

```bash
# 后端存储模块单元测试
go test -v -race ./internal/storage/...

# 全量后端测试
go test -v -race ./...

# 前端单元测试与代码检查
cd web && pnpm test -- --run && pnpm lint && pnpm build && cd ..

# Swagger 规范生成验证
swag init -g cmd/Zhulong/main.go -o api/swagger
```

---

## Review Gates & Rollback Points

1. **Gate 1（跨平台编译隔离）**：确保在非 Linux（macOS/Windows）宿主上编译通过，`statfs` 系统调用严格隔离在 Linux 构建标签内。
2. **Gate 2（I/O 流控与防丢帧）**：清理协程的批次休眠参数（`time.Sleep`）必须可测，且绝不允许在单次循环中无限制锁死文件系统。
3. **Gate 3（前后端接口一致性）**：严格遵循统一响应信封 `{ code: "OK", message, data }` 与全量 TypeScript 严格模式。
4. **Rollback 应急方案**：若存储清理引起任何外部异常，通过配置参数可临时关闭后台清理协程，回退为手动清理或外部脚本接管。
