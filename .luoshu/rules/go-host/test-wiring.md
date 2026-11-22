---
trigger: glob
globs:
  - 'internal/**/*_test.go'
  - 'cmd/**/*_test.go'
priority: 65
description: 测试接线约定（真实中间件、构造期注入、多路扇出断言、GORM 回调注入故障）。
---

# 测试接线

- 审计断言不得用测试替身：挂真实 `audithttp.Middleware` 与真实 `audit.Service`，并在查询前调用 `auditSvc.Stop(ctx)` 排空异步队列，否则响应写出时条目仍在队列中，`List` 会随机漏项。见 [中间件](../../../internal/audit/http/scope.go#L45)、[排空入口](../../../internal/audit/service.go#L108)、[drainAudit 辅助](../../../internal/person/handler_test.go#L112)。

- `*Components` 在构造期捕获协作者，事后替换字段对已构造的 `Service` 无效；要换测试图就在构造前准备好参数与盘上的包（如 `installStubPackage`），禁止 `comp.Verifier = ...` 之后再断言行为已改变。见 [包桩安装](../../../internal/algorithm/testpkg_test.go#L50)。

- 一次变换分发到 N 路的汇聚点必须为每一路注册记录型替身并各自对同一期望值断言；只断言其中一路时，把过滤挪到该路之后仍会全绿。断言前用有界轮询等待替身记录，不依赖分发顺序的隐含假设。

- 存储层错误分支用真实 `*gorm.DB` 注册回调注入故障，禁 `go-sqlmock`、禁为 mock 抽 Repository 接口；处理器要选对（普通 SELECT 走 `Query`，`Scan` 走 `Row`，`Raw`/`Exec` 走 `Raw`），优先按 `tx.Statement.Dest` 类型精确打中，断言用 `errors.Is`/`errors.As` 而非包装文案。见 [回调注册](../../../internal/task/store_fault_test.go#L44-L56)、[按 Dest 匹配](../../../internal/task/store_fault_test.go#L336-L344)。
