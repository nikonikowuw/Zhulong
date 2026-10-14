# Rockchip RGA 视觉前处理后端接入

父任务：`10-10-algorithm-plugin-architecture`（阶段四）。本任务只交付 **RGA 硬件视觉前处理后端**，不含 RKNN 推理（见兄弟任务 `10-13-algo-rknn-inference-backend`）与 DMA-BUF 零拷贝数据通路。

## 1. 目标

在公共算法运行库 HAL（`IVisionBackend`）下接入 Rockchip RGA 硬件前处理后端，让 Letterbox / 缩放 / 色彩转换走 RGA 2D 加速器，并在不满足硬件条件时**透明 fallback 到既有 CPU 实现**，保证算法源码与插件 ABI 不变。

## 2. 仓库事实与真机证据

### 2.1 现有代码

- `native/src/algorithm/runtime/vision_backend.hpp` 已定义 `IVisionBackend`（`letterbox` / `resize`）与 `LetterboxParams`，并声明 `nms`。
- `native/src/algorithm/runtime/cpu_vision_backend.cpp` 是当前唯一实现：支持 packed `RGB24`/`BGR24`，双线性插值，Letterbox 补边值 114，直接写入目标 Tensor ROI。
- `NV12`、`YUV420P`、`RGBA32`、`UNKNOWN` 当前显式返回 `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED`（见 `.trellis/spec/native/algo-plugin-contract.md` §4）。
- `native/src/algorithm/runtime/runtime_factory.cpp` 硬编码返回 `CpuVisionBackend`。
- `native/src/backends/` **尚不存在**。

### 2.2 目标板实测（2026-10-13，经 SSH `rk3568`，只读命令）

| 项 | 实测值 |
| --- | --- |
| OS / 架构 | Debian GNU/Linux 11 (bullseye) / arm64 |
| 内核 | `5.10.226`（`linaro-alip`） |
| RGA 内核驱动 | **v1.3.4**（`dmesg`：`rga: Module initialized. v1.3.4`） |
| 设备节点 | `/dev/rga`（`crw-rw---- root video 10,123`） |
| `librga.so.2.1.0` | `/usr/lib/aarch64-linux-gnu/librga.so.2.1.0`（213336 字节，2022-01-07） |
| `librga.so` → `librga.so.2` → `librga.so.2.1.0` | 符号链完整，`ldconfig -p` 可见 |
| 库内 API 版本字符串 | **`rga_api version 1.10.1_[10]`** |
| 头文件目录 | `/usr/include/rga/`（`im2d.h` 为伞头文件，实际 API 在 `im2d_single.h`） |
| `RGA_API_VERSION`（头文件宏） | `RGA_API_MAJOR=1`、`MINOR=10`、`REVISION=1`、`BUILD=10` → **`1.10.1_[10]`** |
| `im2d.h` SHA-256 | `5ee06891775451c56e6264f5d53db0a4e948e1830287f3a5db4f75c30ac0ff11` |
| `rga.h` SHA-256 | `f50214c9196cfdb1a66301d71d53cd335ed4bc8b8cdf5a56fdcce5965db2c6b9` |
| `im2d_single.h` 可用 API | `imcheck`、`improcess`、`imresize`、`imcvtcolor`、`imcrop`、`imcopy`、`imrectangle`、`imfill`、`imblend` |
| 板端工具链 | gcc/g++ 10.2.1、make 4.3、cmake 3.25.1、**ninja 缺失** |
| 板端磁盘 | `/` 已用 **90%**，可用 2.9G |

### 2.3 关键结论

- **头文件与库 API 版本一致**（均 `1.10.1_[10]`），RGA 侧的版本错配风险显著低于 RKNN 侧。
- 驱动 1.3.4 + 库 API 1.10.1 的组合可用性由 `imcheck()` 在运行期校验，必须在目标板实测而非推断。
- `/dev/rga` 权限为 `root:video`，**运行进程需属于 `video` 组**，否则 RGA 调用会失败——这是部署期约束，必须记录。
- `im2d.h` 是伞头文件，直接 `#include <rga/im2d.h>` 即可，无需分别包含子头。

## 3. 需求

| ID | 需求 |
| --- | --- |
| R1 后端实现 | 在 `native/src/backends/rga/` 内实现 `IVisionBackend` 的 RGA 版本（`letterbox`、`resize`）。RGA 专有头文件只允许出现在该目录。 |
| R2 透明 Fallback | 硬件不支持、格式不匹配、`imcheck()` 失败或运行时错误时，**透明回退到 `CpuVisionBackend`**，调用方只看到成功结果；不得因硬件不可用而使插件初始化失败。 |
| R3 输出等价 | RGA 路径与 CPU 路径的输出必须满足同一份合同：HWC 3 通道 `UINT8`、Letterbox 补边值 114、目标格式通道顺序正确、尺寸/padding 一致。 |
| R4 格式扩展 | 在 RGA 路径上支持 `NV12`/`YUV420P` 等 CPU 路径尚不支持的格式（RGA 原生支持 CSC）；CPU fallback 仍按既有行为显式拒绝这些格式。**能力差异必须显式可查，不得让调用方以为 CPU 也支持。** |
| R5 后端选择 | 通过编译期 profile 隔离选择后端，不改动 `IVisionBackend` 接口；CPU 构建路径保持可用，宿主不得新增 RGA 链接依赖。 |
| R6 版本与校验 | 运行期通过 `imcheck()` / `querystring(RGA_VERSION)` 记录驱动与库版本；版本不匹配时按 R2 回退并记录，不静默继续。 |
| R7 错误与资源 | RGA 返回码映射到 `zhulong_status_t`；buffer 句柄（`im_handle` / `rga_buffer_t`）成对释放；C++ 异常不得穿越 C ABI 边界。 |
| R8 部署约束 | `/dev/rga` 的 `root:video` 权限、`librga.so` 的依赖与固定版本必须写入部署说明；构建脚本校验库 SHA-256。 |
| R9 不做零拷贝 | 本任务只做「CPU 内存 ↔ RGA 处理」，不实现 DMA-BUF 导入导出；不把 `importbuffer_fd` 路径混进来。 |

## 4. 范围

**包含**：RGA 视觉后端实现、CPU 透明 fallback、格式能力矩阵、版本查询与校验、profile 分流、构建接入与部署约束记录。

**不包含**：DMA-BUF / fd 导入导出与零拷贝（后续任务）、RKNN 推理（兄弟任务）、`nms` 改动、昇腾 DVPP / NVIDIA VPI 后端、前端页面。

## 5. 明确不承诺

- 不承诺 RGA 相对 CPU 路径的性能倍数；不预填 FPS。
- 不承诺全链路零拷贝；本任务路径仍有 CPU 内存拷贝。
- 不承诺 RGA 与 RKNN 之间的 buffer 直接复用（那是 DMA-BUF 任务）。
- 不承诺 `NV12` 等格式在 CPU fallback 下可用——能力差异按 R4 显式暴露。
- 不把「RGA 后端编译通过」当作硬件加速已验收。

## 6. 验收标准

| ID | 关联 | 可验证结果 |
| --- | --- | --- |
| AC1 | R1 | RGA 后端在 `native/src/backends/rga/` 内实现 `IVisionBackend` 两个方法；`grep` 证明 `rga/` 头文件只被该目录引用。 |
| AC2 | R2 | 在无 `/dev/rga` 或 `imcheck()` 失败的环境下，`letterbox` 仍返回成功且输出与 CPU 路径一致；日志记录回退原因。 |
| AC3 | R3 | 同一输入下 RGA 与 CPU 输出逐像素比对（允许厂商实现的舍入差异，需定义容差并在测试中固化）；尺寸、padding、通道顺序一致。 |
| AC4 | R4 | `NV12`/`YUV420P` 在 RGA 路径成功转换，在 CPU 路径返回 `NOT_SUPPORTED`；能力矩阵有测试覆盖。 |
| AC5 | R5 | CPU profile 构建通过 `make native-test`；宿主交叉产物 `readelf -d` 中**不出现** `librga.so`。 |
| AC6 | R6、R8 | 真机记录 `imcheck()` 与库版本 `1.10.1_[10]`、驱动 v1.3.4；`/dev/rga` 权限要求写入部署文档；构建脚本校验 `librga.so` SHA-256。 |
| AC8 | R3、R6 | 真机读取 `cat /sys/kernel/debug/rkrga/hardware` 并记录 `byte_stride_align`（RK3568 预期 **4**）；对齐值**运行期探测**而非硬编码；不满足时显式回退 CPU，不把非法参数交给 RGA。 |
| AC9 | R3、R6 | **约束适配策略可验证**：stride 不对齐时向上取整到 16（形式 1，改参数）后用 RGA，而非直接回退；RGA 输出与 CPU 输出仍满足同一合同（容差见 AC3）。⚠️ 需区分「适配后成功」与「回退成功」两条路径，日志可辨。 |
| AC10 | R3、R6 | **两阶段裁剪适配**：`srect` 坐标取整（形式 1，内存不动）后用 `improcess` 一次完成裁剪+缩放+CSC；与 CPU 精确裁剪的输出在容差内一致；全链路无 O(N) 内存重排。⚠️ 若坐标无法取整（偏移不可接受），显式回退 CPU。 |
| AC7 | R7 | 故障注入（非法格式、越界尺寸、RGA 调用失败）后 buffer 全部释放；重复调用无 FD/内存增长；无异常穿越 ABI。 |

## 7. 未决与阻塞

- RGA 与 RKNN 两个后端如何共用同一 profile 切换点，需要与兄弟任务协调；见 `design.md`。
- 板端磁盘 90% 占用，构建/部署路径需规划。
- `NV12` 等格式的 stride/对齐要求需在真机上确认，不从头文件推断。
- 像素容差的具体数值需在实测比对后确定，当前不预填。
- ⚠️ **跨平台对齐差异**：RK3568（RGA2）stride 4 / offset 2，RK3576/RK3588（RGA3）stride 16 / offset 64，**且 RK3576/RK3588 同时含 RGA2-Pro 与 RGA3 核，用户态不可控 job 落哪个核**。本任务首期只验 RK3568（详见 `design.md` §5.0/§5.3）。
- ⚠️ **`x_offset`/`y_offset` 的 64 对齐单位（像素 vs 字节）未确认**，是 RGA3 支持的关键未知。
- ⚠️ **`srect`（裁剪框）的对齐要求未实测**，推测与 `x_offset` 相同（详见 `design.md` §5.4）。
- ⚠️ **RGA3 上裁剪框取整的最大偏移达 63 像素**，取整方向（外扩/内缩/回退）需算法方确认。
- ⚠️ **RGA 电源域开销未实测**：`rga` 电源域实测为 `off-0`，域内含多个设备 + 3 个 IOMMU；`rk_rga` 的 `autosuspend_delay_ms` 为 `n/a`。
