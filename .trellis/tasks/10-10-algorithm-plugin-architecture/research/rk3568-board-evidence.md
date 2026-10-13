# RK3568 目标板证据快照

状态：**只读探查**。2026-10-13 经 `ssh rk3568` 采集，未安装、未修改、未运行任何推理。所有命令均为查询类（`uname` / `ls` / `cat` / `strings` / `sha256sum` / `dmesg` / `find` / `nm`）。

本文件是 `10-13-algo-rknn-inference-backend` 与 `10-13-algo-rga-vision-backend` 的共享证据来源，取代父任务 `10-10-algorithm-plugin-architecture/research/rockchip-context-sharing.md` §1 中「SDK/板端未实测」的部分表述。父任务该节记录的**架构性限制**（声明存在 ≠ 行为可用）仍然有效。

## 1. 系统与内核

| 项 | 实测值 | 采集命令 |
| --- | --- | --- |
| 主机名 | `linaro-alip` | `uname -a` |
| 内核 | `5.10.226`，`#1 SMP Tue Jan 20 09:04:50 CST 2026`，`aarch64` | `uname -a` |
| 发行版 | Debian GNU/Linux 11 (bullseye) | `cat /etc/os-release` |
| 用户态架构 | `arm64` | `dpkg --print-architecture` |
| 根分区 | `/dev/root` 29G，已用 25G，可用 **2.9G（90%）** | `df -h /` |

⚠️ 磁盘 90% 占用是构建与部署的**实际约束**：板端原生编译需要额外空间，方案取舍见各任务 `design.md`。

## 2. RKNPU：驱动与运行库

| 项 | 实测值 | 采集命令 |
| --- | --- | --- |
| RKNPU 驱动 | **v0.9.8**（`[drm] Initialized rknpu 0.9.8 20240828 for fde40000.npu on minor 1`） | `dmesg \| grep -i rknpu`、`cat /sys/kernel/debug/rknpu/version` |
| NPU 节点 | `fde40000.npu`，IOMMU 已启用（`rknpu iommu is enabled, using iommu mode`） | `dmesg` |
| `librknnrt.so` | **2.3.2**（`429f97ae6b@2025-04-09T09:09:27`），`/usr/lib/librknnrt.so`，7726232 字节 | `strings /usr/lib/librknnrt.so` |
| `librknnrt.so` SHA-256 | `d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8` | `sha256sum` |
| `/opt/rknn-toolkit2/rknpu2/lib/librknnrt.so` | 同上版本，**SHA-256 与 `/usr/lib` 逐字节相同** | `sha256sum` |
| `librknn_api.so` | ⚠️ **1.5.2**（`c6b7b351a@2023-08-23`），`/usr/lib/librknn_api.so`，5241256 字节，仅 **25** 个 `rknn_` 导出符号 | `strings`、`nm -D --defined-only` |
| `librknnrt.so.1.5.2.bak` | 5241256 字节，与 `librknn_api.so` 同尺寸的旧版残留 | `ls -l /usr/lib/` |
| `ldconfig -p` 可见项 | `librknnrt.so (libc6,AArch64) => /lib/librknnrt.so` | `ldconfig -p` |
| `nm -D` 符号数对比 | `librknnrt.so`(2.3.2) **39** 个 `rknn_` 符号 vs `librknn_api.so`(1.5.2) **25** 个 | `nm -D --defined-only` |

**结论**：链接目标必须是 `librknnrt.so`（2.3.2）。`librknn_api.so`（1.5.2）是陈旧残留，符号集不同，不得链接。

## 3. RKNN 头文件

| 项 | 实测值 |
| --- | --- |
| 路径 | `/opt/rknn-toolkit2/rknpu2/include/rknn_api.h` |
| SHA-256 | **`c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a`** |
| 与父任务研究快照对比 | 与 `research/rockchip-context-sharing.md` §2 记录的官方固定 ref `59a913d172e7f5ff03c9076e2ec7b1b1288ffd08` 快照 **SHA-256 完全相同** |
| 关键声明行 | L51 `RKNN_FLAG_SHARE_WEIGHT_MEM 0x00000020`；L491-501 `rknn_dup_context`；L723-733 `rknn_set_weight_mem` |
| 其他副本 | `/userdata/rknn_model_zoo/3rdparty/rknpu2/include/rknn_api.h`、`/userdata/rknn_model_zoo/3rdparty/rknpu2/include/rknn_matmul_api.h`、`/userdata/rknn_model_zoo/3rdparty/rknpu1/include/rknn_api.h`、`/root/algo_test/rk3568/reflective_vest/tests/stubs/rknn_api.h`（**stub，勿用**） |

**这个 SHA-256 命中是本轮最重要的发现**：父任务研究中那 5 条 `rknn_dup_context` / `RKNN_FLAG_SHARE_WEIGHT_MEM` / `rknn_set_weight_mem` / `rknn_set_internal_mem` 声明，从「官方快照」升级为「与目标板头文件一致」。

⚠️ **但这只证明声明存在**。2.3.2 runtime + 0.9.8 驱动下 `rknn_dup_context` 是否真能共享权重、是否真有内存收益、销毁次序如何，**全部仍未验证**。

## 4. RGA：驱动、库与头文件

| 项 | 实测值 |
| --- | --- |
| RGA 内核驱动 | **v1.3.4**（`rga: Module initialized. v1.3.4`） |
| 设备节点 | `/dev/rga`（`crw-rw---- root video 10,123`） |
| `librga.so.2.1.0` | `/usr/lib/aarch64-linux-gnu/librga.so.2.1.0`，213336 字节，2022-01-07 |
| 符号链 | `librga.so` → `librga.so.2` → `librga.so.2.1.0`（完整） |
| 库内 API 版本字符串 | **`rga_api version 1.10.1_[10]`** |
| 头文件宏 `RGA_API_VERSION` | `RGA_API_MAJOR=1`、`MINOR=10`、`REVISION=1`、`BUILD=10` → **`1.10.1_[10]`** |
| 头文件目录 | `/usr/include/rga/`（`im2d.h` 为伞头文件，实际 API 在 `im2d_single.h`） |
| `im2d.h` SHA-256 | `5ee06891775451c56e6264f5d53db0a4e948e1830287f3a5db4f75c30ac0ff11` |
| `rga.h` SHA-256 | `f50214c9196cfdb1a66301d71d53cd335ed4bc8b8cdf5a56fdcce5965db2c6b9` |
| `im2d_single.h` 可用 API | `imcheck`、`improcess`、`imresize`、`imcvtcolor`、`imcrop`、`imcopy`、`imrectangle`、`imfill`、`imblend` |

**结论**：头文件与库 API 版本**完全一致**（均 `1.10.1_[10]`），RGA 侧版本错配风险显著低于 RKNN 侧。驱动 1.3.4 与库 API 1.10.1 的组合可用性由 `imcheck()` 在运行期校验，须实测。

⚠️ `/dev/rga` 为 `root:video`，**运行进程须属于 `video` 组**，否则 RGA 调用失败。这是部署期约束。

### 4.1 RGA 硬件能力与调度（`/sys/kernel/debug/rkrga/`）

`hardware` 输出（RK3568 实测）：

```txt
rga2, core 4: version: 3.2.63318
input range:  2x2 ~ 8192x8192
output range: 2x2 ~ 4096x4096
scale limit:  1/16 ~ 16
byte_stride_align: 4          ← ⚠️ stride 对齐要求
max_byte_stride: 32768
csc: RGB2YUV 0x3 YUV2RGB 0x7
feature: 0x205f
mmu: RGA_MMU
```

`scheduler_status` / `request_manager` / `load`：

```txt
num of scheduler = 1          ← ⚠️ 单调度器，无硬件并行
scheduler[0]: rga2
pd_ref = 0                    ← 电源域引用计数（对应 pm_genpd 的 off-0）

rga internal request dump:
request count = 0             ← 内核维护请求队列

load = 0%
process 1: pid = 1047, name: /opt/ias/ias-engine --config /etc/ias/configs/config-dev.yaml
```

**关键结论**：

1. **无用户态 init/deinit**：`grep -rniE 'rga_init|rga_deinit|rga_open|rga_close' /usr/include/rga/*.h` 零命中。
2. **调度在内核**：驱动是「RGA multicore Device Driver v1.3.4」，用户态提交 job，内核负责排队/调度/电源管理。**不存在用户态连接池语义。**
3. **单调度器**：`num of scheduler = 1`，多 worker 并行调用 RGA ≠ 硬件并行。
4. **电源域独立**：`rga` 域实测 `off-0`，域内含 `fdeb0000.rk_rga`、`fdee0000.vepu`、`fdef0000.iep`、`fded0000.jpegd` 与 3 个 IOMMU（`fded0480`/`fdee0800`/`fdef0800`）。`rk_rga` 的 `autosuspend_delay_ms` 为 `n/a`（语义待确认），其他三个为 `2000`。
5. **上下电开销未实测**。

### 4.2 ⚠️ stride 对齐随 RGA 版本变化（跨平台关键约束）

`byte_stride_align` **不是常数**，而是「RGA 版本 × 像素格式」的函数。同一个 `librga.so` 按硬件能力动态校验（板端字符串：`unsupported width stride %d, %s width stride should be %d aligned!`，符号 `_Z15rga_check_alignPKc12rga_buffer_tib`）。

| SoC | RGA 版本 | 核心列出方式 | `byte_stride_align` | 证据 |
| --- | --- | --- | --- | --- |
| **RK3568** | RGA2 | `rga2, core 4`（单核） | **4** | ✅ 本项目板端实测 |
| RK3576 | RGA3 | `rga3_core0` / `rga3_core1`（多核） | **16** | 官方 FAQ 文档实测输出 |
| RK3588 | RGA3 | `rga3, core 1` / `core 2`（多核） | **16** | 社区实测输出 |

官方开发者指南（RGA3 段落原文摘引）：

> `width stride must be 16-aligned，x_offset、y_offset must be 64-aligned，width、height、height stride must be 2-aligned`

**⚠️ stride 与 offset 是两套独立约束，且跨代差异巨大**：

| RGA 版本 | `Byte_stride` | **width stride** | **x_offset / y_offset** | height / height stride |
| --- | --- | --- | --- | --- |
| **RGA2**（含 Lite/Enhance/Pro） | **4** | RGB888 需 4 对齐 | **2 对齐** | 2 对齐 |
| **RGA3** | **16** | **16 对齐** | **⚠️ 64 对齐** | 2 对齐 |

**offset 要求从 2 → 64，相差 32 倍。**

**RGA3 的按格式细分（官方表）**：

| Byte_stride | 格式 | 对齐要求 |
| --- | --- | --- |
| 16 | `RGBA_8888`/`BGRA`/`ARGB`/`ABGR`/`RGBX`/`BGRX`/`XRGB`/`XBGR` | width stride **4 字节对齐** |
| 16 | `RGB_565`/`BGR_565` | width stride **2 字节对齐** |
| 16 | `YUYV_422`/`YVYU_422`/`UYVY_422`/`VYUY_422` | width stride **8 字节对齐** |
| 16 | `RGB_888`/`BGR_888` | width stride **4 字节对齐** |
| 16 | 10bit YUV（`YCbCr_420_SP_10B` 等） | width stride **16 字节对齐** |
| 16 | 其他格式 | width stride **不要求对齐** |

#### ⚠️ 混合核：RK3576/RK3588 同时含 RGA2 与 RGA3

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

#### ✅ upstream 先例：统一取 16

Linux kernel RGA3 支持补丁原文：

> Align the stride to a multiple of 16 according to the RGA3 requirements mentioned in the datasheet. **This also ensures that the stride of the RGA2 is aligned to 4 bytes**, as it needs to divide the value by 4 (one word) before storing it in the register.

⚠️ 但 **RKNN 侧不能单方面决定**：`rknn_tensor_attr.w_stride` 标注为 **read-only**（`rknn_api.h:298`）。它是查询得到的，必须匹配；只有 `h_stride` 是 `write-only`。

#### ⚠️ 未确认点

`x_offset`/`y_offset` 的 64 对齐**单位是像素还是字节**，官方文档未明确。若为字节，`pad_top=140` 的偏移 `140 × wstride × 3` 可能恰好满足；若为像素则必然失败。**必须实测，不猜。**

#### `imresize` 无 rect，裁剪+缩放必须用 `improcess`

板端头文件确认：

```c
// im2d_single.h:61 —— 无 rect 参数
IM_API IM_STATUS imresize(const rga_buffer_t src, rga_buffer_t dst,
                          double fx = 0, double fy = 0, int interpolation = 0,
                          int sync = 1, int *release_fence_fd = NULL);

// im2d_single.h:442 —— 带 srect/drect，单次完成裁剪+缩放+CSC
IM_API IM_STATUS improcess(rga_buffer_t src, rga_buffer_t dst, rga_buffer_t pat,
                           im_rect srect, im_rect drect, im_rect prect,
                           int acquire_fence_fd, int *release_fence_fd,
                           im_opt_t *opt_ptr, int usage);
```

⚠️ **两阶段算法（检测 → 按框裁剪 → 分类）必须用 `improcess`**，不能用 `imresize`。

#### ⚠️ `srect` 的对齐要求未实测

板端报错串里有 `Error srcRect` / `Error dstRect` / `Error yuv not align to 2, rect[x,y,w,h]`，但**没有** rect 的 64 对齐报错串（因 RK3568 是 RGA2，只需 2 对齐）。

**推测**：`srect.x/y` 受与 `x_offset`/`y_offset` 相同的约束（RGA2: 2，RGA3: 64），但**无直接证据**，须在目标硬件上实测。

#### ⚠️ `rknn_input` 无 stride 字段

`rknn_api.h:414-428`：

```c
typedef struct _rknn_input {
    uint32_t index;
    void* buf;
    uint32_t size;
    uint8_t pass_through;
    rknn_tensor_type type;
    rknn_tensor_format fmt;
    // ⚠️ 无 stride 字段
} rknn_input;
```

**后果**：padded buffer 喂 `rknn_inputs_set` 会**静默算错**（不是报错，是错结果）。

板端 `librga.so` 中逐条对应的运行期报错串（RK3568 实测提取）：

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

## 5. 可用模型与工具链

| 项 | 实测值 |
| --- | --- |
| 可用 `.rknn` | `/userdata/rknn_model_zoo/examples/facelivt/model/facelivtv2_s_rk3568_fp.rknn` |
| | `/userdata/rknn_model_zoo/examples/insightface/buffalo_l/model/w600k_r50.rknn` |
| | `/userdata/rknn_model_zoo/install/rk356x_linux_aarch64/rknn_facelivt_demo/model/facelivtv2_s_surv_rk3568.rknn` |
| 模型 zoo | `/userdata/rknn_model_zoo/`（含 `3rdparty/`、`build/`、`examples/`、`install/`） |
| 板端工具链 | gcc/g++ **10.2.1**、make 4.3、cmake 3.25.1、**ninja 缺失**、pkg-config 0.29.2 |
| 其他 | `/usr/bin/rknn_common_test`、`/usr/bin/rknn_server`、`/usr/bin/rknn_server.bak.1.5.2` |
| RKNN-Toolkit2 | `/opt/rknn-toolkit2/rknpu2/`（仅 `include/` + `lib/`；Python 包未安装，`import rknn` 失败） |

⚠️ `/root/algo_test/rk3568/reflective_vest/tests/stubs/rknn_api.h` 是**测试 stub**，不得当作真实头文件使用。

## 6. 宿主侧对照（本仓库开发机）

| 项 | 值 |
| --- | --- |
| 交叉编译器 | `aarch64-linux-gnu-gcc` / `g++` **14.2.0**、`aarch64-linux-gnu-ar` (binutils 2.44) |
| sysroot | `build/sysroots/rk3568`（完整 Debian arm64 rootfs） |
| sysroot 内 libstdc++ | **10**（`usr/include/c++/10`、`usr/lib/gcc/aarch64-linux-gnu/10`） |
| sysroot 内 RKNN/RGA | ❌ **无**（`find -iname '*rknn*' -o -iname '*rga*'` 为空） |
| 交叉产物 | `build/targets/rk3568/Zhulong`、`engine.test`（aarch64 ELF） |
| 交叉产物 `NEEDED` | `libstdc++.so.6`、`libm.so.6`、`libresolv.so.2`、`libdl.so.2`、`libpthread.so.0`、`libgcc_s.so.1`、`libc.so.6` —— **无 `librknnrt.so` / `librga.so`** |

⚠️ 编译器代差：宿主交叉 gcc **14.2.0** vs sysroot libstdc++ **10** vs 板端 gcc **10.2.1**。既有 `.trellis/spec/native/ingestion-contract.md` 的 host/cross 合同继续适用。

## 7. 仍未验证的事项

- `rknn_dup_context` 在 2.3.2 + 0.9.8 下的**实际行为**、内存收益、销毁次序、并发安全性。
- `RKNN_QUERY_SDK_VERSION` 返回的实际 `api_version` / `drv_version`（须成功 `rknn_init` 后查询）。
- `RKNN_QUERY_MEM_SIZE` 的真实 `total_weight_size` / `total_internal_size` 数值。
- `imcheck()` 在驱动 1.3.4 + 库 1.10.1 下的返回结果。
- RGA 对 `NV12` / `YUV420P` 的**多 plane 实际 stride 语义**（对齐要求已测：YUV 必须 2 对齐）。
- **RGA 电源域上下电开销**（域状态已测为 `off-0`，成本未测）。
- **RGA3 硬件上的 Letterbox 策略**（`x_offset`/`y_offset` 64 对齐导致 ROI 写法不可行；候选方案未验证）。
- 板端原生编译的空间是否够（磁盘仅剩 2.9G）。
- 端到端吞吐、P95、内存占用、功耗/温度。
- 多 worker 并发下的设备级排队行为（RK3568 单 NPU 核 + 单 RGA 调度器）。
