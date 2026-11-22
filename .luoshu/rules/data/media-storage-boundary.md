---
trigger: glob
globs:
  - 'internal/mediafile/**'
  - 'internal/storage/**'
  - 'internal/app/media_router*.go'
  - 'internal/alertpush/signed_media*.go'
  - 'internal/record/retention*.go'
priority: 65
description: 统一媒体读取根和存储写保护，不绕过签名验证或保留证据保护。
---

# 媒体读取与存储保护

- 媒体读取统一走 `mediafile` 的路径白名单、根快照与 rooted open。签名媒体先做限流、有效期与签名验证，再复用读取服务；不要直接拼接用户路径。见[根解析](../../../internal/mediafile/resolver.go#L46-L172)、[签名入口](../../../internal/alertpush/signed_media.go#L25-L117)。
- 写入遵循独立存储写门控，不因媒体可读就推断挂载可写。回收前检查证据保护，检查失败时保守保护；文件清理确认后才协调数据库，不丢弃待重试的清理状态。见[写门控](../../../internal/storage/gate.go#L61)、[快照清理和协调](../../../internal/storage/service.go#L182-L199)、[记录留存](../../../internal/record/retention.go#L211-L340)。
