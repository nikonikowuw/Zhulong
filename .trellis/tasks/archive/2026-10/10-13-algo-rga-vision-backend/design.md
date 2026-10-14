# RGA 视觉前处理后端：技术设计

依据 `prd.md` R1–R9。证据来源：`research/rk3568-board-evidence.md`（真机只读实测）。接口名称与参数是实现建议，不是已存在 API。

## 1. 边界与接入点

RGA 后端**替换** `IVisionBackend` 的实现，不改动接口本身，也不改动插件 ABI。

现有调用链（已确认）：

```text
native/sdk/include/zhulong/algo.hpp:107   → get_default_vision_backend()
native/tests/algo_runtime_test.cpp:83     → get_default_vision_backend()
                                             ↓
native/src/algorithm/runtime/runtime_factory.cpp
   static auto backend = std::make_shared<CpuVisionBackend>();   ← 当前硬编码
```

与推理后端**相互独立**：可以「RKNN 推理 + CPU 前处理」，也可以「CPU 推理 + RGA 前处理」。因此后端选择用**两个正交的 CMake 选项**，不是单一开关。见 §3。

## 2. 目录与依赖隔离

```text
native/src/backends/rga/          ← 新建；RGA 专有头文件唯一允许出现处
  rga_vision_backend.hpp/.cpp     ← IVisionBackend 实现
  rga_error_map.hpp/.cpp          ← IM_STATUS → zhulong_status_t
  rga_buffer_guard.hpp            ← rga_buffer_t / im_handle 的 RAII 包装
```

依据 `.trellis/spec/native/directory-structure.md:44,62`：厂商专有头文件只能在 `src/backends/` 内引用。CMake 中该目录 include 带 `PRIVATE`。

验收方式（AC1）：

```bash
grep -rl 'rga/im2d\|rga/rga' native/src/ native/sdk/ native/include/   # 只应命中 src/backends/rga/
```

## 3. 后端选择：两个正交编译期开关

```cmake
# native/CMakeLists.txt 新增
set(ZHULONG_INFERENCE_BACKEND "cpu" CACHE STRING "算法运行库推理后端: cpu | rknn")
set_property(CACHE ZHULONG_INFERENCE_BACKEND PROPERTY STRINGS cpu rknn)

set(ZHULONG_VISION_BACKEND "cpu" CACHE STRING "算法运行库视觉前处理后端: cpu | rga")
set_property(CACHE ZHULONG_VISION_BACKEND PROPERTY STRINGS cpu rga)

if(ZHULONG_VISION_BACKEND STREQUAL "rga")
    if(NOT ZHULONG_RGA_ROOT)
        message(FATAL_ERROR "ZHULONG_VISION_BACKEND=rga 需要 -DZHULONG_RGA_ROOT=<librga 前缀>")
    endif()
    target_sources(ZhulongAlgoRuntime PRIVATE
        src/backends/rga/rga_vision_backend.cpp
        src/backends/rga/rga_error_map.cpp)
    target_include_directories(ZhulongAlgoRuntime PRIVATE
        ${ZHULONG_RGA_ROOT}/include)
    target_link_libraries(ZhulongAlgoRuntime PRIVATE
        ${ZHULONG_RGA_ROOT}/lib/librga.so)
    target_compile_definitions(ZhulongAlgoRuntime PRIVATE ZHULONG_VISION_RGA=1)
endif()
```

`runtime_factory.cpp`：

```cpp
std::shared_ptr<IVisionBackend> get_default_vision_backend() {
#ifdef ZHULONG_VISION_RGA
    static auto backend = std::make_shared<RgaVisionBackend>();
#else
    static auto backend = std::make_shared<CpuVisionBackend>();
#endif
    return backend;
}
```

⚠️ **协调点**：兄弟任务 `10-13-algo-rknn-inference-backend` 建立同一套机制的推理侧开关。两者都改 `native/CMakeLists.txt`、`native/scripts/build.py` 与 `runtime_factory.cpp`，**必须串行执行**。本任务在其后。

⚠️ 完整的阶段四串行链（含 `10-13-algo-broker-host`）见父任务 `prd.md` §1.1「执行顺序」。

⚠️ 这**不是** `native/profiles/rk3568.json` 的复用。那个 JSON 是交叉编译工具链 profile（cc/cxx/sysroot/cflags），由 `build.py profile()` 解析并做 `tree_digest` 缓存身份校验。后端选择是 CMake 层的独立开关，两者正交。

## 4. 库依赖引入：与 RKNN 任务共用同一前缀方案

`build/sysroots/rk3568` 是干净 Debian rootfs，**不含 RGA**（实测确认）。与 RKNN 任务同样的问题，同样选**方案 A：独立前缀**。

```text
build/deps/rga/           ← 从板端 scp 出的 librga 前缀
  include/rga/            ← im2d.h, im2d_single.h, rga.h, ...
  lib/librga.so           ← 或 librga.so.2.1.0 + 符号链
```

**版本固定值**（来自真机实测，AC6 校验用）：

```text
im2d.h            sha256 = 5ee06891775451c56e6264f5d53db0a4e948e1830287f3a5db4f75c30ac0ff11
rga.h             sha256 = f50214c9196cfdb1a66301d71d53cd335ed4bc8b8cdf5a56fdcce5965db2c6b9
librga.so.2.1.0   大小   = 213336 字节
                  API    = rga_api version 1.10.1_[10]  (与头文件宏 RGA_API_VERSION 一致)
目标驱动           = RGA v1.3.4
目标内核           = 5.10.226 / Debian 11 arm64
```

✅ RGA 侧的版本一致性**远好于 RKNN**：头文件 `RGA_API_VERSION` 与库内字符串都是 `1.10.1_[10]`，没有 RKNN 那种 `librknn_api.so`(1.5.2) 与 `librknnrt.so`(2.3.2) 并存的陷阱。

⚠️ 但 `/usr/lib/aarch64-linux-gnu/` 下有 `librga.so` / `librga.so.2` / `librga.so.2.1.0` 三级符号链。从板端 `scp` 时**三个都要**，否则链接期找不到 `librga.so`。

## 5. RGA 调用映射

`im2d_single.h` 提供的可用 API（实测）：`imcheck`、`improcess`、`imresize`、`imcvtcolor`、`imcrop`、`imcopy`、`imrectangle`、`imfill`、`imblend`。

⚠️ **所有 RGA 调用前必须先过 §5.0 的对齐校验**，本节的 `resize`/`letterbox` 映射都建立在该前提上。

### 5.0 对齐约束（版本相关，⚠️ 关键）

**RGA 的对齐要求不是常数，而是「RGA 版本 × 像素格式」的函数，且 stride 与 offset 的约束截然不同。** 同一个 `librga.so` 二进制按硬件能力动态校验（板端字符串证据：`unsupported width stride %d, %s width stride should be %d aligned!`，符号 `_Z15rga_check_alignPKc12rga_buffer_tib`——那个 `%d` 由硬件描述符填入）。

**实测的 `byte_stride_align`（`cat /sys/kernel/debug/rkrga/hardware`）**：

| SoC | RGA 版本 | 核心列出方式 | `byte_stride_align` | 证据来源 |
| --- | --- | --- | --- | --- |
| **RK3568** | RGA2 | `rga2, core 4`（单核） | **4** | ✅ 本项目板端实测 |
| RK3576 | RGA3 | `rga3_core0` / `rga3_core1`（多核） | **16** | 官方 FAQ 文档实测输出 |
| RK3588 | RGA3 | `rga3, core 1` / `core 2`（多核） | **16** | 社区实测输出 |

#### ⚠️ stride 与 offset 是两套独立约束（从官方对齐表提取）

| RGA 版本 | `Byte_stride` | **width stride** | **x_offset / y_offset** | height / height stride |
| --- | --- | --- | --- | --- |
| **RGA2**（含 Lite/Enhance/Pro） | **4** | RGB888 需 4 对齐 | **2 对齐** | 2 对齐 |
| **RGA3** | **16** | **16 对齐** | **⚠️ 64 对齐** | 2 对齐 |

**offset 要求从 RGA2 的 2 → RGA3 的 64，相差 32 倍。** 这是跨代最显著的约束差异。

⚠️ 表中的 `x_offset`/`y_offset` 是 **buffer 级偏移**；`srect` 的 rect 级偏移要求推测相同但未实测（见 §5.4）。

官方 RGA3 段落原文摘引：

> `width stride must be 16-aligned，x_offset、y_offset must be 64-aligned，width、height、height stride must be 2-aligned`

**RGA3 的按格式细分（官方表）**：

| Byte_stride | 格式 | 对齐要求 |
| --- | --- | --- |
| 16 | `RGBA_8888`/`BGRA`/`ARGB`/`ABGR`/`RGBX`/`BGRX`/`XRGB`/`XBGR` | width stride **4 字节对齐** |
| 16 | `RGB_565`/`BGR_565` | width stride **2 字节对齐** |
| 16 | `YUYV_422`/`YVYU_422`/`UYVY_422`/`VYUY_422` | width stride **8 字节对齐**，x/y_offset 另有要求 |
| 16 | `RGB_888`/`BGR_888` | width stride **4 字节对齐** |
| 16 | `YCbCr_420_SP_10B` 等 10bit | width stride **16 字节对齐** |
| 16 | 其他格式 | width stride **不要求对齐** |

#### ⚠️ 混合核不确定性（影响 RK3576/RK3588）

官方 FAQ 原文：

> The **RK3588** contains **one RGA2-Enhanced core** (which is already implemented by the rockchip rga driver) and **two independent RGA3 cores**.

RK3576 同理（`rga3_core0`/`core1` + RGA2-Pro）。所以：

```txt
RK3576 / RK3588 的对齐要求因核而异：
  RGA2-Pro    → byte_stride 4， offset 2
  RGA3 core0  → byte_stride 16，offset 64
  RGA3 core1  → byte_stride 16，offset 64

⚠️ 用户态无法控制 job 落到哪个核（内核调度；实测 num of scheduler = 1）
```

**推论**：同一份调用参数在不同核上可能一个通过一个失败。**唯一安全的做法是按最严要求（16 / 64）准备。**

#### 板端运行期报错串（RK3568 实测提取，逐条对应上表）

```txt
unsupported width stride %d, %s width stride should be %d aligned!   ← 主校验，%d = byte_stride_align
err yuv not align to 2                                                ← YUV 必须 2 对齐
%s FBC mode does not support width_stride[%d] is non-16 aligned       ← FBC 模式 16 对齐
%s FBC mode does not support height_stride[%d] is non-16 aligned
%s TILE8*8 mode does not support width[%d] is non-8 aligned           ← TILE8*8 另有要求
%s TILE8*8 mode does not support input width_stride[%d] is non-16 aligned
If it is an RK encoder output, it needs to be aligned with an odd multiple of 256.
imresize error, factor[fx,fy]=[%lf,%lf], ALIGN[dw,dh]=[%d,%d][%d,%d]  ← resize 尺寸对齐
```

⚠️ **本任务首期只验 RK3568（RGA2 / stride 4 / offset 2）**。上表的 RGA3 行是**已知的跨平台约束**，不假装已解决；切换到 RK3576/RK3588 时按 §5.3 的策略处理。

⚠️ **未确认点**：`x_offset`/`y_offset` 的 64 对齐**单位是像素还是字节**，官方文档未明确。实际影响很大：若为像素，`pad_top=140` 直接失败（`140 % 64 ≠ 0`）；若为字节，偏移 `140 × wstride × 3` 可能恰好通过。**必须在 RGA3 硬件上实测，不猜。**


### 5.1 `resize` 映射

```cpp
// IVisionBackend::resize 签名
zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                        zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                        int dst_w, int dst_h, zhulong_pixel_format_t dst_format);
```

RGA 路径：`wrapbuffer_virtualaddr(src, ...)` → `imresize(src_rga, dst_rga)`。

⚠️ **必须处理的差异**：

| 项 | CPU 实现 | RGA 行为 | 处理 |
| --- | --- | --- | --- |
| 缩放算法 | 像素中心双线性 | 硬件双线性，实现细节厂商自有 | 定义像素容差，不要求逐位相同（AC3） |
| 通道交换 | 手工 `swap_channels` | RGA 用 `src_format`/`dst_format` 表达 | 映射到 RGA 的 `RK_FORMAT_RGB_888` / `RK_FORMAT_BGR_888` |
| stride=0 语义 | 视为紧密排列 | RGA 需显式 wstride | 在包装层把 0 转为 `width*3` |
| 负数/不足 stride | 返回 `INVALID_ARGUMENT` | 同 | 包装层先行校验，不交给 RGA |
| **对齐** | 无要求 | 依 §5.0 的版本/格式矩阵 | **先适配**：向上取整到 16（§5.3）；取整后仍需满足 §5.0 的格式专属要求（如 RGB888 需 4 对齐、10bit YUV 需 16 对齐） |

### 5.2 `letterbox` 映射

CPU 实现是「`memset` 114 → 在 ROI 上 `resize`」。RGA 路径在 **RK3568 上**同构：

1. `memset(dst, 114, byte_size)`（保留 CPU 语义，RGA 不做补边填充）
2. 计算 `scale` / `unpad_w` / `unpad_h` / `pad_left` / `pad_top` —— **复用 CPU 实现的同一套计算逻辑**，不重复实现（见 §6）
3. 在 ROI 上 `imresize(src_rga, dst_roi_rga)`

⚠️ ROI 的 `wrapbuffer_virtualaddr` 需正确计算 `dst_buf + (pad_top * target_w + pad_left) * 3` 与 `wstride = target_w * 3`，且 `wstride` 必须满足 §5.0 的对齐要求（RK3568：4 字节；`target_w * 3` 不总是 4 的倍数，例如 `target_w=639` → `1917`，需显式处理）。不满足则按 §7 回退 CPU。

#### ⚠️ RGA3（RK3576/RK3588）上 ROI 写法**不可行**

官方 RGA3 约束要求 **`x_offset` / `y_offset` 必须 64 对齐**（§5.0）。而 Letterbox 的 padding 偏移：

```cpp
dst_buf + (pad_top * target_w + pad_left) * 3
```

`pad_left` / `pad_top` 由缩放比决定，**几乎不可能 64 对齐**（例：1080p → 640×640 时 `pad_top = 140`，`140 % 64 = 12`）。因此在 RGA3 上直接用 ROI 指针做 `imresize` 会失败。

⚠️ 但**具体处理方式取决于 §5.3 的适配策略**，不在这里预选——下面那三个方案都可能在 O(N) 代价下变成负优化。

### 5.3 约束适配策略（核心决策规则）

RGA 的约束不满足时，有两条路：**适配**（让输入符合要求）或 **fallback CPU**。选错会做出负优化。

#### 判据：适配成本是 O(1) 还是 O(N)？

```txt
适配只需改「传给 API 的参数」（O(1)）→ 适配，用 RGA
适配需要「重新排布内存」  （O(N)）→ 直接 fallback CPU
                                     （CPU 都遍历全部像素了，给 RGA 图什么？）
```

⚠️ **「改参数」与「改布局」是两回事**：

| 形式 | 做法 | 成本 | 例子 |
| --- | --- | --- | --- |
| **形式 1：改参数** | 内存不动，只改传给 RGA 的数值 | **O(1)** | `wstride`、`srect` 坐标、`target` 尺寸 |
| **形式 2：重排内存** | 申请新 buffer + 逐行拷贝 | **O(N)** | 把 padded 布局拷成紧密布局 |

⚠️ 形式 2 在本项目中**一律不采用**：既然 CPU 已遍历全图，RGA 的收益归零，不如直接 fallback CPU。

**为什么 O(N) 适配是负优化**（粗算，需实测修正）：

```txt
Letterbox 输出 640×640×3 = 1.23 MB
  CPU memset 114（本来就要做）    ~123 us @ 10GB/s
  RGA resize 1080p → 640×360      ~345 us
  CPU 全图适配一遍                ~数百 us ~ ms   ← 吃掉 RGA 全部收益
```

#### 源侧与目的侧分开看

| 侧 | 谁决定布局 | 典型情况 | 需适配？ |
| --- | --- | --- | --- |
| **源（RGA 输入）** | 解码器（FFmpeg） | `linesize` 通常 32/64 对齐；16:9 摄像头（1920×1080 / 1280×720）天然满足 | ⚠️ 通常无需；异常时走形式 2 → fallback |
| **目的（RGA 输出）** | **我们自己分配** | 选 `target_w % 16 == 0` 即可 | ✅ 分配时一次决定，无运行期适配 |

⚠️ 源侧 `linesize` 的「通常对齐」是 FFmpeg `avcodec_align_dimensions` 的默认行为，**本项目未实测**；不假设，异常时按 §7 回退。

#### 逐约束的策略表

| 不符合的约束 | 形式 | 策略 |
| --- | --- | --- |
| **stride 不对齐（目的侧）** | ✅ 形式 1——分配时选对齐值 | **适配**：向上取整到 16 |
| **stride 不对齐（源侧）** | ✅ 形式 1——`wstride` 是参数 | **适配**：传实际 `linesize` |
| **crop 框坐标不对齐** | ✅ 形式 1——`srect` 是参数 | **适配**：坐标取整（见 §5.4） |
| **target 尺寸超范围** | ✅ 形式 1 | **适配**：调整 target |
| **格式不支持** | ❌ **形式 2**（逐像素转换） | **fallback CPU** |
| **offset 不对齐（RGA3，Letterbox）** | ⚠️ 取决于方案，见下 | 需单独判断 |

#### stride：统一取 16

**无论 RGA2 还是 RGA3，一律用 16 对齐的 stride。**

理由：

1. `16 % 4 == 0`，RGA2 的 4 字节要求**自动满足**。
2. 有 upstream 先例（Linux kernel RGA3 支持补丁原文）：
   > Align the stride to a multiple of 16 according to the RGA3 requirements mentioned in the datasheet. **This also ensures that the stride of the RGA2 is aligned to 4 bytes**, as it needs to divide the value by 4 (one word) before storing it in the register.
3. 典型值天然满足：`target_w × 3 % 16 == 0` ⟺ `target_w % 16 == 0`（因 `gcd(3,16)=1`），而 640 / 608 / 416 / 320 等 YOLO 常见输入**全是 16 的倍数**。

⚠️ **但 RKNN 侧不能单方面决定**：`rknn_tensor_attr.w_stride` 标注为 **read-only**（`rknn_api.h:298`：「the stride of tensor along the width dimention of input, Note: it is **read-only**, 0 means equal to width」）。它是**查询得到的**，必须匹配，不能强制。只有 `h_stride` 是 `write-only`。

#### offset（RGA3）：需要单独策略

`pad_left`/`pad_top` 由缩放比决定，**取 16 对齐的 stride 解决不了 offset 的 64 对齐要求**。

| 方案 | 成本 | 语义影响 | 评价 |
| --- | --- | --- | --- |
| **① 调整 target 使 pad 恰好对齐** | ✅ O(1) | ⚠️ 改变 letterbox 几何（可能影响精度） | 需算法方确认可接受 |
| **② 输出 stride 按 64 对齐 + pad 值设计为 64 倍数** | ✅ O(1) | 需 `Tensor` 支持 stride | ⚠️ 牵动 SDK，首期不做 |
| **③ RGA resize 到临时 buffer，CPU memcpy 到 ROI** | ❌ **O(N)** | 无 | 负优化，不推荐 |
| **④ fallback CPU** | O(N) 但无 RGA 开销 | 无 | ✅ 最保守，可接受 |

⚠️ 人家**不预选**。选项 ① 需算法方确认；选项 ② 需改 `Tensor`（当前 `data_.resize(total_elements * element_size)` 是紧密排列，构造函数无 stride 参数）；选项 ③ 按 O(N) 判据应排除。

⚠️ **且必须先确认 offset 对齐的单位是像素还是字节**（§5.0 末尾的未确认点）。若为字节，`pad_top=140` 的偏移 `140 × 1920 × 3 = 806400`，`806400 % 64 == 0` → **反而通过**，问题就不存在。

#### 决策树（写入实现）

```txt
RGA 预处理请求
  │
  ├─ 格式 RGA 不支持？ ─────────→ fallback CPU（形式 2 无意义）
  │
  ├─ 是两阶段裁剪？ ───────────→ 走 §5.4（srect 取整，形式 1）
  │
  ├─ stride 不对齐？ ───────────→ 形式 1：传实际 stride / 分配时取 16 对齐 → 用 RGA
  │
  ├─ 探测到 RGA3 且 offset 需 64 对齐（Letterbox）？
  │    ├─ 单位是字节且恰好满足 ──→ 用 RGA
  │    ├─ 可调整 target 使 pad 对齐（形式 1）→ 用 RGA
  │    └─ 否则 ──────────────────→ fallback CPU
  │
  └─ 否则（纯 RGA2，offset 仅需 2）→ 基本天然满足 → 用 RGA
```

⚠️ **决策树里没有「形式 2」分支**——重排内存一律不在考虑范围，直接 fallback CPU。

#### 本任务范围

**首期只实现 RK3568（RGA2）路径**：stride 16 对齐（自动满足 4）、offset 2 对齐（基本天然满足）、格式矩阵按 §8。

⚠️ **RGA3 的 offset 策略不在本任务范围**。本任务只保证：

1. 对齐值从**运行期获取**（不硬编码 4 或 16），避免跨平台静默出错。
2. 不满足时**显式回退 CPU**，不把非法参数交给 RGA。
3. 把 §5.3 的决策树写进注释/文档，供后续 RGA3 工作直接使用。

### 5.4 两阶段算法的裁剪适配（`srect` 是参数，不是内存）

两阶段算法（检测 → 按框裁剪 → 分类）是最常见的预处理形态。它的适配**属于形式 1（O(1)）**，不需要重排内存。

#### 一次调用完成裁剪 + 缩放 + CSC

⚠️ **`imresize_t(src, dst, fx, fy, ...)` 没有 rect 参数**（板端头文件确认），裁剪与缩放必须用 `improcess`：

```c
IM_API IM_STATUS improcess(rga_buffer_t src, rga_buffer_t dst, rga_buffer_t pat,
                           im_rect srect, im_rect drect, im_rect prect,
                           int acquire_fence_fd, int *release_fence_fd,
                           im_opt_t *opt_ptr, int usage);
```

`improcess` 单次调用同时完成 **裁剪（`srect`）+ 缩放（`srect` → `drect`）+ 颜色转换**。

#### 适配点：`srect` 坐标取整（不改内存）

```txt
❌ 形式 2（O(N)，不要走这条路）：
   源帧 → CPU 裁剪到新 buffer → RGA 处理
   （CPU 遍历像素，RGA 收益归零）

✅ 形式 1（O(1)，实际可行）：
   源帧（内存不动）→ improcess(src, dst, srect=检测框, drect=目标区)
   （只传 rect 参数，RGA 自己按 rect 读像素）
```

| 对齐要求 | 由谁满足 | 做法 |
| --- | --- | --- |
| 源帧 `stride` | 解码器（FFmpeg `linesize`） | 直接传实际值 |
| **`srect.x` / `srect.y`** | ⚠️ **本节的适配点** | 检测框坐标**取整到对齐值** |
| 目标 `stride` | **我们自己分配** | 选 `target_w % 16 == 0` |

#### ⚠️ 取整方向的权衡（需算法方确认）

坐标取整意味着**裁剪区域略微偏移**，影响随对齐粒度变化：

| 对齐粒度 | 最大偏移 | 对分类任务的影响 |
| --- | --- | --- |
| RGA2（offset 2 对齐） | ≤ 1 像素 | ✅ 几乎无感 |
| RGA3（offset 64 对齐） | **≤ 63 像素** | ⚠️ 对小目标/人脸可能切掉边缘 |

⚠️ **63 像素的偏移对人脸识别是显著的**。RGA3 上的候选方案：

| 方案 | 做法 | 风险 |
| --- | --- | --- |
| a. **向外扩** | 框向外扩到对齐边界 | 多带背景（对分类通常比切掉边缘安全） |
| b. 向内缩 | 框向内缩到对齐边界 | 可能切掉目标边缘 |
| c. fallback CPU | 不取整，CPU 精确裁剪 | 失去 RGA 收益 |

⚠️ 人家**倾向 a**，但**需算法方确认**，不替算法团队决定语义。

#### ⚠️ `srect` 的对齐要求未实测

板端报错串里有 `Error srcRect` / `Error dstRect` / `Error yuv not align to 2, rect[x,y,w,h]`，但**没有** rect 的 64 对齐报错串（因 RK3568 是 RGA2，只需 2 对齐）。

**推测**：`srect.x/y` 受与 `x_offset`/`y_offset` 相同的约束（RGA2: 2，RGA3: 64），但**无直接证据**，须在目标硬件上实测。

#### ⚠️ `Tensor` 无 stride 的限制不阻塞本场景

`Tensor` 只能表达紧密排列（`data_.resize(total_elements * element_size)`，构造函数无 stride 参数）。这对 **Letterbox 的 padding** 是硬约束（§5.2），但**对两阶段裁剪不影响**：裁剪是直接缩放到目标区，**不需要 padding**。

#### 决策树（裁剪路径）

```txt
两阶段裁剪请求
  │
  ├─ 格式 RGA 不支持？ ──────→ fallback CPU
  │
  ├─ srect.x/y 对齐？
  │    ├─ 是 ──────────────────→ improcess 一次调用
  │    └─ 否 → 取整到对齐值（向外扩）
  │              └─ 偏移可接受？──→ improcess
  │                              └─ 不可接受（RGA3 大偏移）──→ fallback CPU
  │
  └─ 目标 buffer 由我们分配，stride 选 16 对齐
```

**全链路零 O(N) 拷贝** ✅

## 6. 复用既有实现，不重复造轮子

Letterbox 的几何计算（scale / padding）与 `nms` 已经在 `CpuVisionBackend` 里实现且经过测试。RGA 后端**不应复制**这些逻辑。

建议做法：把 Letterbox 的几何计算提取为 `runtime/` 内的一个自由函数（`compute_letterbox_geometry()`），CPU 与 RGA 两个后端共同调用。

⚠️ 提取范围**严格限定**在这一个纯计算函数。不改动 `IVisionBackend` 接口，不重构 `CpuVisionBackend` 的其他部分，不把 `nms` 搬走（`nms` 与 RGA 无关）。

## 7. CPU 透明 Fallback（R2，本任务的核心要求）

**触发回退的条件**：

| 条件 | 处理 | 适配成本 |
| --- | --- | --- |
| 编译期未选 RGA 后端 | 不适用（编译期已定） | — |
| 运行期 `imcheck()` 失败（驱动/库不匹配） | 回退 CPU，记录原因 | — |
| 无 `/dev/rga` 或权限不足（非 `video` 组） | 回退 CPU，记录原因 | — |
| 输入格式 RGA 不支持（如 `UNKNOWN`） | 回退 CPU | O(N) 转换无意义 |
| **stride 不对齐** | ✅ **先适配**：向上取整到 16（§5.3）；适配后仍失败才回退 | O(1) |
| **offset 不对齐（仅 RGA3）** | ⚠️ 按 §5.3 决策树：可 O(1) 调整则用 RGA，否则回退 | 需判断 |
| 硬件尺寸超范围（input 2x2~8192²，output 2x2~4096²） | 回退 CPU | O(1) 但会改语义 |
| RGA 调用返回错误码 | 回退 CPU，记录原因 | — |

⚠️ **不要把所有约束不满足都直接回退**：stride 是 O(1) 适配，先适配再用 RGA（§5.3 判据）。只有 O(N) 或无法 O(1) 修复的才回退。

**回退实现方式**：

```cpp
class RgaVisionBackend : public IVisionBackend {
    CpuVisionBackend fallback_;      // ← 组合，不是继承
    bool rga_usable_{false};
    uint32_t stride_align_{0};       // ← 运行期探测，不硬编码
public:
    RgaVisionBackend() {
        rga_usable_ = probe_rga();             // imcheck() + /dev/rga 可访问
        stride_align_ = query_stride_align();  // §5.0：读 hardware 节点或首次 imcheck() 探测
    }

    zhulong_status_t letterbox(const zhulong_frame_view& src, Tensor& dst,
                               LetterboxParams& p) override {
        if (rga_usable_ && try_rga_letterbox(src, dst, p)) {
            return ZHULONG_PLUGIN_OK;
        }
        return fallback_.letterbox(src, dst, p);   // 透明兜底
    }
};
```

⚠️ **`stride_align_` 必须运行期探测，不硬编码**。硬编码 4 会在 RK3576/RK3588（RGA3 = 16）上静默出错；硬编码 16 会浪费 RGA2 的带宽。探测来源：`/sys/kernel/debug/rkrga/hardware` 的 `byte_stride_align`，或首次 `imcheck()` 的结果。⚠️ 若两处都取不到，回退 CPU（不猜）。

⚠️ **回退必须对调用方完全透明**：返回 `ZHULONG_PLUGIN_OK`，输出满足同一份合同（HWC 3 通道 `UINT8`、补边值 114、尺寸/padding 一致）。不得因硬件不可用而使插件初始化失败。

⚠️ 但**能力差异要显式**（R4）：RGA 路径支持 `NV12`/`YUV420P`，CPU 路径不支持。当格式为 `NV12` 且 RGA 不可用时，回退 CPU 会返回 `NOT_SUPPORTED` —— 这是**正确的**，不能假装成功。建议在 `IVisionBackend` 之外提供一个能力查询入口（如 `supports_format(fmt)`），让调用方可查。⚠️ 这需要与父任务确认是否需要改接口；**若接口不可改**，则通过日志与文档暴露能力差异，不改 `IVisionBackend`。

## 8. 格式能力矩阵

| 格式 | CPU 路径 | RGA 路径 | RGA 不可用时的最终行为 |
| --- | --- | --- | --- |
| `RGB24` | ✅ | ✅ | CPU 成功 |
| `BGR24` | ✅ | ✅ | CPU 成功 |
| `NV12` | ❌ `NOT_SUPPORTED` | ✅（RGA 原生 CSC） | `NOT_SUPPORTED` |
| `YUV420P` | ❌ `NOT_SUPPORTED` | ✅（RGA 原生 CSC） | `NOT_SUPPORTED` |
| `RGBA32` | ❌ `NOT_SUPPORTED` | ⚠️ 待实测确认 | `NOT_SUPPORTED` |
| `UNKNOWN` | ❌ `NOT_SUPPORTED` | ❌ | `NOT_SUPPORTED` |

⚠️ `RGBA32` 的 RGA 支持需实测，不从头文件推断。

⚠️ `NV12`/`YUV420P` 是多 plane 格式，`zhulong_frame_view` 的 `planes[4]`/`strides[4]` 可以表达。RGA 的 `wrapbuffer_virtualaddr` 对 YUV 需要正确的 y/u/v 指针与 stride —— **对齐要求必须实测**，这是 AC4 的主要风险点。

## 9. 资源管理与错误映射

```cpp
// RAII 包装，确保 rga_buffer_t / im_handle 成对释放
struct RgaBufferGuard {
    rga_buffer_handle_t handle{RGA_HANDLE_INVALID};
    ~RgaBufferGuard() { if (handle != RGA_HANDLE_INVALID) releasebuffer_handle(handle); }
};
```

- `wrapbuffer_virtualaddr` 不接管内存所有权（CPU 内存仍由调用方管理），但 `importbuffer_*` 会创建 handle，必须 `releasebuffer_handle`。
- ⚠️ 本任务**只用 `wrapbuffer_virtualaddr`**，不引入 `importbuffer_fd`（那是 DMA-BUF 任务，R9）。

错误映射：`IM_STATUS_SUCCESS` → `OK`；`IM_STATUS_INVALID_PARAM` / `IM_STATUS_ILLEGAL_PARAM` → `INVALID_ARGUMENT`；`IM_STATUS_NOT_SUPPORTED` → `NOT_SUPPORTED`；`IM_STATUS_NO_MEMORY` → `OUT_OF_MEMORY`；其余 → `INTERNAL`。

⚠️ 具体 `IM_STATUS_*` 枚举名必须从板端 `/usr/include/rga/im2d_type.h` 读取确认（SHA-256 已固定），不凭记忆写。

## 10. 部署约束（R8）

| 约束 | 说明 |
| --- | --- |
| `/dev/rga` 权限 | `crw-rw---- root video` —— 运行进程须属于 **`video` 组** |
| 符号链 | `librga.so` → `librga.so.2` → `librga.so.2.1.0`，`scp` 时三级都要 |
| 库版本 | `1.10.1_[10]`（与头文件宏一致） |
| 驱动版本 | RGA v1.3.4（内核 5.10.226） |
| 宿主要求 | `Zhulong` 宿主的 `NEEDED` **不得**出现 `librga.so`（AC5） |

## 11. 验证路径

| 层 | 命令 | 说明 |
| --- | --- | --- |
| CPU profile 回归 | `make native-test` | 保证未破坏既有路径（AC5） |
| 头文件隔离 | `grep -rl 'rga/im2d' native/src/ native/sdk/` | 只应命中 `backends/rga/`（AC1） |
| 宿主无污染 | `readelf -d build/targets/rk3568/Zhulong \| grep NEEDED` | 不得出现 `librga.so`（AC5） |
| 交叉构建 | `CROSS_PROFILE=... make native-build` | 走既有 wrapper |
| 真机版本 | 板端 `imcheck()` + 库版本查询 | 记录 1.10.1_[10] / 驱动 v1.3.4（AC6） |
| 真机像素比对 | RGA vs CPU 输出逐像素 + 容差 | AC3 |
| 真机格式矩阵 | NV12/YUV420P 在两条路径的行为 | AC4 |
| 回退验证 | 无 `/dev/rga` 环境下 letterbox 仍成功 | AC2 |

## 12. 残余风险

- RGA 与 CPU 的缩放实现差异可能导致像素容差超预期，需实测确定容差。
- ⚠️ **`x_offset`/`y_offset` 的 64 对齐单位（像素 vs 字节）未确认**——官方文档未明确。若为字节，则 `pad_top=140` 的偏移恰好满足，RGA3 的 Letterbox 问题可能不存在；若为像素则必然失败。**这是 RGA3 支持的关键未知。**
- ⚠️ **RGA3 的 Letterbox offset 适配策略未定**（§5.3 列了 4 个候选，均未在硬件上验证）。本任务首期不涉及。
- ⚠️ **`srect`（裁剪框）的对齐要求未实测**，推测与 `x_offset` 相同（RGA2: 2，RGA3: 64），但无直接证据（§5.4）。
- ⚠️ **RGA3 上裁剪框取整的最大偏移达 63 像素**，对人脸/小目标可能切掉边缘；取整方向（外扩/内缩/回退）**需算法方确认**（§5.4）。
- ⚠️ **源侧 `linesize` 的「通常 32/64 对齐」未实测**，是 FFmpeg `avcodec_align_dimensions` 的默认行为推测；不假设，异常时回退（§5.3）。
- ⚠️ **`Tensor` 无 stride 字段**：`data_.resize(total_elements * element_size)` 只能表达紧密排列。对 Letterbox 的 padding 是硬约束（§5.2），但对两阶段裁剪不影响（§5.4）。
- ⚠️ **`rknn_input` 无 stride 字段**（`rknn_api.h:414-428`）：padded buffer 喂 `rknn_inputs_set` 会**静默算错**（不是报错，是错结果）。目的侧 buffer 必须紧密排列，或依赖 RKNN 自身的 `w_stride` 查询结果。
- ⚠️ **混合核不确定性**：RK3576/RK3588 同时含 RGA2-Pro 与多个 RGA3，对齐要求不同，而用户态**无法控制 job 落到哪个核**（内核调度）。只能按最严要求准备。
- ⚠️ **多 RGA 核心的调度分配未知**；且若一个是 RGA2-Pro、一个是 RGA3，则同一组参数的通过性不确定。
- ⚠️ `NV12`/`YUV420P` 的对齐要求已有板端证据（YUV 必须 2 对齐：`err yuv not align to 2`），但**多 plane 的实际 stride 语义仍未在板端验证**，可能无法直接用 `wrapbuffer_virtualaddr`。
- ⚠️ **RGA 无用户态 init/deinit**（头文件已确认），但**电源域上下电开销未实测**：实测 `rga` 电源域为 `off-0`，域内含 `rk_rga`/`vepu`/`iep`/`jpegd` + 3 个 IOMMU；`rk_rga` 的 `autosuspend_delay_ms` 为 `n/a`（语义待确认）。
- ⚠️ **RGA 是单调度器**（实测 `num of scheduler = 1`），多 worker 无硬件并行收益；调度由内核 `request_manager` 负责，用户态无连接池语义。
- ⚠️ **O(N) 适配的性能模型是粗算**（§5.3），未实测；若实测发现 CPU 适配比预期便宜，判据可能需要修正。
- `RGBA32` 支持情况未知（需实测，§8 能力矩阵已标待确认）。
- 编译器代差（宿主 14.2.0 / sysroot libstdc++ 10 / 板端 10.2.1）。
- 板端磁盘仅剩 2.9G。
- 与 RKNN 后端同时启用时的设备级资源竞争（RGA 与 NPU 是不同硬件，理论上不冲突，但 DDR 带宽共享）。
