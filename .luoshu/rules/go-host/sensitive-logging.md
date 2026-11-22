---
trigger: glob
globs:
  - 'internal/logsafe/**'
  - 'internal/app/logger*.go'
  - 'internal/httputil/errors*.go'
  - 'internal/clientlog/**'
  - 'internal/algorunner/process_log*.go'
priority: 65
description: 限制诊断日志中的敏感数据并保留错误关联信息。
---

# 敏感日志边界

- 日志使用注入的 Zap 记录器，保留请求 ID/流 ID 等关联字段，不传原始凭据、完整敏感 URL 或任意客户端字段给 sinks。需要错误诊断时在服务端安全入口记录 cause，不将 cause 原文写进错误信封。不要用重复的 handler 错误日志代替该入口。见[安全 core 测试](../../../internal/logsafe/core_test.go#L27)、[输出与记录器](../../../internal/app/logger.go#L68-L187)、[错误出口](../../../internal/httputil/errors.go#L105-L189)。
