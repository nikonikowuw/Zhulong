# 领域语义错误与泛型 HTTP 端点重构 实施计划 (implement.md)

## 实施阶段与步骤清单

### 阶段 1：新增 `internal/apperr` 模块
- [x] 1.1 创建 `internal/apperr/error.go`，定义 `Kind` 枚举、`Error` 结构体及工厂函数。
- [x] 1.2 创建 `internal/apperr/error_test.go`，编写表驱动测试验证 `Error()`, `Unwrap()`, `errors.Is/As` 兼容性。
- [x] 1.3 验证：运行 `go test -v ./internal/apperr/...`。

### 阶段 2：升级 `internal/httputil` 传输层基础设施
- [x] 2.1 在 `internal/httputil/bind.go` 实现通用的 `HandleBindError`，具备自动读取 json tag 与 validator 错误码映射功能。
- [x] 2.2 在 `internal/httputil/endpoint.go` 实现泛型端点适配器 `HandleJSON[Req, Resp]` 与 `Handle[Resp]`。
- [x] 2.3 在 `internal/httputil/errors.go` 升级 `WriteError`，无缝识别 `*apperr.Error` 并映射 HTTP 状态码与 i18n。
- [x] 2.4 在 `internal/httputil/` 编写测试用例覆盖泛型端点、绑定错误、413/422/400 场景及状态码映射。
- [x] 2.5 验证：运行 `go test -v ./internal/httputil/...`。

### 阶段 3：重构 `internal/auth` 服务层与控制器
- [x] 3.1 改造 `internal/auth/service.go`：将各业务错误（凭证错误、初始化状态、限频超限等）使用 `apperr` 规范化返回。
- [x] 3.2 改造 `internal/auth/handler.go`：
  - 路由挂载使用 `httputil.HandleJSON` / `httputil.Handle`。
  - 移除大段 `switch-case` 错误映射与局部 `handleBindError`。
  - 保持 `setSessionCookie` 与 `clearSessionCookie` 的正常调用。
- [x] 3.3 调整 `internal/auth/middleware.go` 中未认证的错误返回与 `apperr.Unauthenticated` 协同。
- [x] 3.4 验证：运行 `go test -v ./internal/auth/...`。

### 阶段 4：全局集成验证与代码质量检查
- [x] 4.1 运行全套后端测试：`go test -v -race ./internal/...`。
- [x] 4.2 运行代码检查：`go vet ./...`。
- [x] 4.3 确认 API 契约与 HTTP 状态码、响应体字段 100% 保持一致。

---

## 验证与回滚策略

- **验证命令**：
  ```bash
  go vet ./...
  go test -v -race ./internal/...
  ```
- **回滚方案**：
  - 若重构引入不可预期的破坏性行为，可通过 Git 快速恢复 `internal/auth` 与 `internal/httputil`。
