---
trigger: glob
globs:
  - 'web/src/lib/api-client.ts'
  - 'web/src/features/**/api/**'
  - 'web/src/features/**/data/**'
priority: 100
description: 复用共享 HTTP 客户端，在特性边界拆信封并校验不可信数据。
---

# 前端 API 边界

- 特性 API 复用 `apiClient`，保留其 `Accept-Language` 和错误处理。成功拦截器原样返回 Axios 响应，由特性 API 读取 `resp.data.data`；不要假设已全局拆包。见 [共享传输层](../../../web/src/lib/api-client.ts#L71-L109)。
- 外部载荷进入特性前用 Zod 解析，不把 TypeScript 泛型或 `as` 断言当作运行时校验。见 [摄像头 API 的解包与解析](../../../web/src/features/cameras/api/cameras-api.ts#L46-L83)。
