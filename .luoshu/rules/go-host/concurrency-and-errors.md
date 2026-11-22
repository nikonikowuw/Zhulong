---
trigger: glob
globs:
  - 'internal/**/*.go'
  - 'cmd/**/*.go'
priority: 100
description: Go 并发与错误处理铁律（-race、协程退出路径、定时器复用、禁 panic/吞错/上下文入结构体）。
---

# 并发与错误铁律

- 所有 Go 测试命令带 `-race`，数据竞争按阻断 Bug 处理。依赖 `internal/engine` 的包必须走 native wrapper（`python3 native/scripts/build.py go test -race ...`），不裸跑 `go test`，否则会复用陈旧的 Engine/FFmpeg 归档。见 [质量门禁](../../../Makefile#L244-L248)。

- 严禁无终止条件的孤儿协程：长时协程必须监听 `ctx.Done()` 或专属退出通道，成对的 `Start`/`Stop` 中 `Stop` 必须能让所有内部 goroutine 收敛后再返回。见 [可停止的采样服务](../../../internal/systeminfo/service.go#L111-L116)。

- 循环内 `select` 若可能放弃等待（`ctx.Done()`、退出通道或 `default` 先命中），用 `time.NewTimer` + `timer.Reset` 复用，退出时 `Stop`；只有等待必然被消费的单等待点才允许 `time.After`，否则 Timer 在触发前无法回收。

- 业务路径禁 `panic`（整机 NVR 崩溃停服），错误返回强类型 `error` 由上层恢复；禁 `_ = fn()` 吞错；禁把 `context.Context` 存进结构体字段（始终作为首参显式传入）；禁全局可变业务状态（依赖由构造函数注入）。
