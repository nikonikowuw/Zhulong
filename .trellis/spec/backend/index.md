# Go 后端开发规范总览

> Zhulong Go 业务服务架构、编码规约、数据库与质量标准。

---

## 1. 规范索引

| 规范指南 | 核心内容 |
| --- | --- |
| [目录架构](./directory-structure.md) | 业务能力模块分包、包依赖方向与路径规划 |
| [跨语言命名规范](../naming-guidelines.md) | 文件、CLI 命令/参数、Go 标识符及领域术语 |
| [HTTP API 与 Swagger](./http-api-guidelines.md) | Gin 路由、DTO 校验、Swaggo 2.0 文档、SPA 回退 |
| [单用户认证](./authentication-guidelines.md) | SQLite 单用户凭据、内存 Session、Cookie、限流及前后端认证态契约 |
| [HTTP 中间件开发与编排](./middleware-guidelines.md) | 中间件分层隔离、流水线时序、Request ID、Access Log、CORS 与类型安全 |
| [数据库与版本化迁移](./database-guidelines.md) | GORM+SQLite、WAL/Pragma 配置、启动自动迁移、事务约束 |
| [依赖注入与生命周期](./dependency-injection.md) | Uber Fx 装配、启动失败回滚、HTTP 排空、native/数据库关闭顺序 |
| [边缘媒体存储与动态生命周期](./storage-guidelines.md) | 存储路径规范、statfs 水位感知、外挂盘防穿透、双水位回差清理与熔断 |
| [摄像机业务与生命周期](./camera-guidelines.md) | 主子流建模、AES-GCM 凭据加密、双流原子门禁、正交状态机与探活调度 |
| [宿主启动与构建合同](./host-runtime-contract.md) | CLI、health/SPA/Swagger、迁移、CGO 静态链接与跨层验证合同 |
| [错误处理与统一契约](./error-handling.md) | `AppError` 结构、必需三字段与可选字段级 `details`、后端 i18n 错误翻译、Zap 日志脱敏 |
| [结构化日志规范](./logging-guidelines.md) | Uber Zap 规约、强类型字段、等级划分、敏感信息脱敏 |
| [质量检查与测试门禁](./quality-guidelines.md) | 格式化、`-race` 竞态检测、空仓库基线守则 |

---

## 2. 开发前检查清单 (Pre-Development Checklist)

- [ ] **业务模块内聚**：新增代码是否归属于对应的 `internal/<module>`，无全局杂烩层？
- [ ] **命名自然清晰**：文件、Go 标识符、CLI 命令与参数是否自然且见名知意？
- [ ] **依赖单向无环**：是否未反向引用 `internal/app`？无模块间循环引用？
- [ ] **Fx 纯粹性**：业务构造函数是否保持普通纯函数（无 `fx.App` 侵入业务包）？
- [ ] **统一响应体**：Handler 是否遵循 `{ code, message, data }`，且仅 422 字段校验错误可附带 `details`？
- [ ] **中间件分层隔离**：API 中间件（日志/鉴权/CORS）是否仅挂载在 `/api/` 路由组，未污染 SPA 静态资源与 Swagger？
- [ ] **中间件执行时序**：中间件流水线是否遵循 Recovery ➔ RequestID ➔ CORS ➔ AccessLog ➔ Handler？
- [ ] **Context 存取类型安全**：Gin 上下文数据是否使用私有 key 与导出 Accessor 包装，未直接使用裸字符串 `c.Set/c.Get`？
- [ ] **底层错误脱敏**：SQLite/CGO 底层真实错误是否封装在内部打日志，未裸抛给前端？
- [ ] **严禁 AutoMigrate**：表变动是否以严格递增的 SQL 脚本置于 `migrations/`？

---

## 3. 质量验证命令

```bash
gofmt -w cmd internal
make go-check
```

*`make go-check` 通过 Native 构建脚本为 vet/race 注入正确的静态链接输入；详见 [Native 接入合同](../native/ingestion-contract.md)。门禁限定在 `cmd/` 和 `internal/` 自有包，避免 `./...` 递归扫描 `web/node_modules` 中第三方包自带的 Go 示例。*
