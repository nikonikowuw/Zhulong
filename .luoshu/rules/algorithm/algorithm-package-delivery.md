---
trigger: glob
globs:
  - 'packages/**'
  - 'native/sdk/**'
  - 'native/profiles/**'
  - 'native/CMakeLists.txt'
  - 'native/scripts/*sdk*'
  - 'native/scripts/board_check.sh'
  - 'native/scripts/bundle_runtime.py'
  - 'native/scripts/check_package_drift.py'
  - 'native/scripts/build.py'
  - 'internal/algorithm/verifier*.go'
  - 'internal/algorithm/uploader*.go'
  - 'Makefile'
priority: 100
description: 确保算法包能离仓开发，验证结论与交付场景一致。
---

# 算法包与 SDK 交付

- 包工程只依赖安装后的 SDK 前缀，不引用仓库私有头/库。归档根提供与合法 `manifest.id` 同名的可执行入口、清单和样本，不用旧式共享插件替代入口。验包在包目录执行并核对结果类型；`sample_decoded=false` 只能记作烟测。见 [安装准入](../../../internal/algorithm/verifier.go#L206-L250)、[入口与样本](../../../internal/algorithm/verifier.go#L362-L382)、[动态验包](../../../internal/algorithm/verifier.go#L423-L477)。
- 推理/视觉后端归算法 Runtime，不给宿主 Engine 加厂商推理/视觉依赖；测试桩不出货。交叉构建不执行目标测试。独立前缀用 `make sdk-clean-check` 验证，真机另用 `make board-check`；任何宿主或交叉编译通过都不等于硬件验收。见 [Engine 链接](../../../native/CMakeLists.txt#L131-L160)、[算子后端](../../../native/CMakeLists.txt#L353-L404)、[发行与验证入口](../../../Makefile#L85-L147)、[交叉入口](../../../Makefile#L172-L212)。
