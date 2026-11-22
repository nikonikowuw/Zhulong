---
trigger: glob
globs:
  - 'internal/app/app.go'
  - 'internal/app/runtime*.go'
  - 'internal/**/module.go'
priority: 45
description: 将资源启停与失败回滚集中到宿主生命周期，保护依赖有序释放。
---

# Fx 与宿主生命周期

- Fx 装配留在 `internal/app`。领域构造函数只装配依赖，不开文件、绑监听器或启动 worker；将资源启停交给宿主 `lifecycleRuntime`，不在业务包注册独立 Fx 钩子。见 [宿主装配](../../../internal/app/app.go#L154-L169)和[数据库构造与启动](../../../internal/database/store.go#L40-L105)。
- 正常关停和启动回滚共用 `stopSequence`；获取资源后立即登记清理责任，不以某域后续启动成功为前提。核对消费者先于依赖关闭，并覆盖部分初始化失败。见[撤销顺序](../../../internal/app/runtime.go#L527-L574)和[资源启动](../../../internal/app/runtime.go#L282-L421)。
