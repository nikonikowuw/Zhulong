---
trigger: glob
globs:
  - 'web/src/routes/**'
  - 'web/src/routeTree.gen.ts'
  - 'web/src/features/**/index.tsx'
  - 'web/vite.config.ts'
priority: 100
description: 保持路由层、特性入口与生成物边界。
---

# 前端路由与特性边界

- 路由只声明 path、守卫和校验后的 URL 输入；页面和业务逻辑在特性中，跨特性使用公开入口。不要把原始 search 参数直接传给请求。[页面路由示例](../../../web/src/routes/_authenticated/persons/index.tsx#L1-L8)
- 不手改生成的 `routeTree.gen.ts`；从路由声明经 TanStack Router 插件生成。[插件配置](../../../web/vite.config.ts#L37-L54)
