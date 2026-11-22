---
trigger: glob
globs:
  - 'internal/filestore/**'
  - 'internal/mediafile/**'
  - 'internal/storage/**'
  - 'internal/app/media_router*.go'
  - 'internal/alertpush/signed_media*.go'
  - 'internal/record/retention*.go'
  - 'internal/recording/upload*.go'
priority: 65
description: 统一媒体读取与对象存储抽象，保护写门控、凭据安全、签名验证与保留证据。
---

# 媒体读取与存储保护

- 业务媒体统一经 `filestore.Store` 与 `mediafile` 进行抽象访问。媒体读取统一走路径白名单与 rooted/presigned 访问；签名媒体先做限流、有效期与签名验证再读取，严禁拼接裸路径。见[Filestore 抽象](../../../internal/filestore/types.go)、[根解析](../../../internal/mediafile/resolver.go#L46-L172)、[签名入口](../../../internal/alertpush/signed_media.go#L25-L117)。
- 写入遵循存储 `EmergencyGate` 门控与 Local Spooling；远端存储故障不阻塞本地录制。回收前检查证据保护，对象删除确认后才协调数据库索引，不丢弃待重试的清理状态。见[写门控](../../../internal/storage/gate.go)、[清理协调](../../../internal/storage/remote_cleaner.go)、[记录留存](../../../internal/record/retention.go#L211-L340)。
