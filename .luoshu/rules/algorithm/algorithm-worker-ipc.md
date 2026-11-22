---
trigger: glob
globs:
  - 'native/sdk/src/**'
  - 'native/sdk/abi.json'
  - 'native/src/algorithm/ipc/**'
  - 'native/tests/frame_broker_test.cpp'
  - 'native/tests/ipc_socket_fd_test.cpp'
  - 'internal/algorunner/**'
  - 'internal/app/adapters.go'
priority: 100
description: 保护算法进程间协议、帧租约及 Worker 失败语义。
---

# 算法 Worker 与 IPC

- 保持线格式自然对齐和版本契约；ACK 原样回传租约代次，拒绝过期 ACK。结果在 ACK 前发送，释放后不再读帧。跨进程 fd 用 `SCM_RIGHTS`，不信对端 fd 数值；裁剪后帧必须交付真实裁剪像素。见 [IPC 布局](../../../native/src/algorithm/ipc/ipc_protocol.hpp#L130-L168)、[发布实现](../../../native/src/algorithm/ipc/frame_broker.cpp#L423-L470)、[过期确认检查](../../../native/src/algorithm/ipc/frame_broker.cpp#L864-L885)、[Worker 回传](../../../native/sdk/src/algo_main.cpp#L1065-L1108)。
- 进程等待与 broker I/O 有界，不跨 supervisor/worker 控制锁等待。更改 Worker 的 stage 或退出语义时同步 Go allowlist 和跨语言测试；broker 断开按依赖退避，不记算法崩溃、不触发回滚。见 [启动编排](../../../internal/algorunner/supervisor.go#L520-L560)、[stage 校验](../../../internal/algorunner/runner_log.go#L322-L341)、[依赖故障回归](../../../internal/algorunner/watcher_dependency_test.go#L79-L119)。
