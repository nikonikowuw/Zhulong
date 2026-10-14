# RKNN 供应商前缀（vendored）

本目录是 Rockchip RKNPU2 供应商运行库的**受控副本**，仅供 **RKNN 推理后端构建**使用。
宿主（`ZhulongEngine` / Go 二进制）**不得**链接本目录中的任何库。

## 来源与版本

| 文件 | 来源 | SHA-256 |
| --- | --- | --- |
| `include/rknn_api.h` | RKNN Model Zoo / rknpu2 官方发布（与目标板 `/opt/rknn-toolkit2/rknpu2/include/rknn_api.h` 逐字节一致） | `c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a` |
| `lib/librknnrt.so` | RKNN Runtime 2.3.2（`429f97ae6b@2025-04-09T09:09:27`），与目标板 `/usr/lib/librknnrt.so` 逐字节一致 | `d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8` |

- Runtime 版本：**2.3.2**
- 目标板驱动：**RKNPU v0.9.8**（`dmesg`: `Initialized rknpu 0.9.8 20240828`）
- 目标板：RK3568 / Debian 11 (bullseye) arm64 / 内核 5.10.226
- `librknnrt.so` 的 `SONAME` 为 `librknnrt.so`（**不是** `.so.2`）。
- `rknn_api.h` 只 `#include <stdint.h>`，自带无系统头依赖。

## 板端同一性证据

`librknnrt.so` 与 `rknn_api.h` 均取自 RK3568 真机副本；SHA-256 与
`.trellis/tasks/10-13-algo-rknn-inference-backend/prd.md` §2.2 记录的真机实测值一致：

- 板端 `/usr/lib/librknnrt.so` SHA-256 = `d31fc19c…`（与 `lib/librknnrt.so` 相同）
- 板端 `/opt/rknn-toolkit2/rknpu2/include/rknn_api.h` SHA-256 = `c48e11a6…`

构建脚本 `native/scripts/build.py` 的 `rknn_prefix()` 会在每次 RKNN 构建前离线校验这两个
SHA-256，不匹配即失败（AC6）。

## 显式排除陈旧库

板端 `/usr/lib/librknn_api.so` 是 **1.5.2** 的陈旧残留（仅 25 个 `rknn_` 导出符号，与 2.3.2 的
39 个不同）。本目录**不包含**它，构建也**不得**链接它；产物 `NEEDED` 只允许出现 `librknnrt.so`。

## 使用方式

```bash
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn.json build
```

CMake 通过 `-DZHULONG_RKNN_ROOT=<本目录绝对路径>` 消费该前缀；include 目录为 `PRIVATE`，
`librknnrt.so` 以绝对文件路径链接进 `libZhulongAlgoRuntime.so`。

## 为什么需要独立的 `rk3568-rknn` profile

板端 `MockDetector` 走 `zhulong::Model::load("mock_detector.weights")`；在 RKNN 后端下这会触发
`rknn_init` 一个不存在的文件 → `model_create` 失败 → runner 退出码 2。因此：

- **cpu 交叉产物**（`native/profiles/rk3568.json`）继续服务 mock / broker 板端测试；
- **rknn 交叉产物**（`native/profiles/rk3568-rknn.json`）专供 RKNN 后端验证。

两个 profile 各司其职，不互相替代。
