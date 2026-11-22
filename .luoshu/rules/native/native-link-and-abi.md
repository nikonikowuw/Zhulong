---
trigger: glob
globs:
  - 'Makefile'
  - '.air.toml'
  - 'cmd/**/*.go'
  - 'internal/**/*.go'
  - 'native/**'
priority: 20
description: 保持宿主原生链接产物一致，并保护 C ABI 的所有权与回调生命周期。
---

# 原生链接与 C ABI

- 对直接或间接依赖 `internal/engine` 的 Go 包执行 build、test、vet 时，使用仓库 Make 目标或 `python3 native/scripts/build.py go …`；不要绕过 wrapper。全量 Go 检查用 `make go-check`，增量 Go 测试保留 `-race`。修改 C++ 后执行 `make native-build`，Air 不监听 C++。见 [Make 入口](../../../Makefile#L77-L83)、[Go 检查](../../../Makefile#L244-L248)、[链接注入实现](../../../native/scripts/build.py#L583-L639)、[Air 监听配置](../../../.air.toml#L14-L26)。
- 宿主只通过 `internal/engine` 调用公开 C ABI。使用整数 `cgo.Handle` 作为回调 token，不让 Go 指针逃逸到 C；在导出边界捕获 C++ 异常；原生句柄显式创建和销毁。回调参数借用原生内存，仅在该次同步调用内使用，缓存或异步派发前复制。见 [Go 所有权约定](../../../internal/engine/engine.go#L8-L29)、[C 回调契约](../../../native/include/Zhulong/engine.h#L265-L295)、[C++ 异常隔离](../../../native/src/abi/engine.cpp#L96-L108)。
