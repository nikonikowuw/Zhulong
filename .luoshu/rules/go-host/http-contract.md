---
trigger: glob
globs:
  - 'Makefile'
  - 'internal/**/*handler*.go'
  - 'internal/app/app.go'
  - 'internal/app/router.go'
  - 'internal/httputil/**'
  - 'internal/apidocs/**'
priority: 100
description: 维护 Go JSON 信封和端点装配、鉴权、Swagger 的一致性。
---

# HTTP 契约

- JSON 业务响应通过 `httputil.Success`、`SuccessWithStatus`、`WriteError` 或相应 helper 写出统一信封；成功机器码为 `OK`。不用裸 `c.JSON`，不直接收发 GORM model。文件、流和 WebSocket 不套 JSON 成功信封。见 [成功信封](../../../internal/httputil/response.go#L16-L37)和[错误入口](../../../internal/httputil/errors.go#L105-L112)。
- 新业务端点通过 `RegisterRoutes(*gin.RouterGroup)` 注册到对应 Fx value group；新增含 Swagger 注解的包要核对 `api-docs --dir` 白名单，不能只验证运行时路由。见 [Fx 路由分组](../../../internal/app/router.go#L52-L81)、[文档生成入口](../../../Makefile#L241-L242)。
- 错误状态、稳定机器码和本地化文案走 `apperr` / `httputil`，不直接返回内部 cause；422 字段错误按统一 `details` 输出。修改 fallback 时保证 API 或 Swagger 的错误不会返回 SPA HTML。见[错误输出](../../../internal/httputil/errors.go#L105-L189)和[路由回退](../../../internal/app/router.go#L203-L235)。
