---
trigger: glob
globs:
  - 'internal/app/router*.go'
  - 'internal/app/upload_router*.go'
  - 'internal/app/app.go'
  - 'internal/auth/**'
  - 'internal/apitoken/**'
  - 'internal/openapi/**'
priority: 50
description: 保持浏览器会话、机器令牌、独立上传分组的认证和限额边界。
---

# 路由认证边界

- 生产宿主须注入会话认证服务，保护浏览器普通/上传两组；不要把 Cookie 当作机器身份。OpenAPI 只接受 Bearer，缺少令牌服务时必须拒绝。新增上传端点不要把大请求先挂到 1 MiB 的普通 API 分组，再试图在 handler 放宽限额。见[认证装配](../../../internal/app/app.go#L154-L169)、[分组](../../../internal/app/router.go#L134-L200)。
- 保持限额先于通用操作审计；需要审计绑定失败的入口在绑定前声明动作。禁止把会话 Cookie、Bearer 令牌或认证失败中的攻击者输入写入日志/审计。见[审计装配](../../../internal/app/router.go#L94-L109)、[登录处理](../../../internal/auth/handler.go#L28-L48)、[令牌中间件](../../../internal/apitoken/middleware.go#L32-L100)。
