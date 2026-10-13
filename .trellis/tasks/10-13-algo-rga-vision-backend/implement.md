# RGA 视觉前处理后端：实施计划

前置：本任务为父任务 `10-10-algorithm-plugin-architecture` 阶段四的一半。

**执行顺序**：`10-13-algo-rknn-inference-backend` → **本任务**。两者都触碰 `native/CMakeLists.txt`、`native/scripts/build.py`、`runtime_factory.cpp`，**必须串行，不可并行编辑**。

本任务依赖 RKNN 任务先建立：
- 两个正交 CMake 开关（`ZHULONG_INFERENCE_BACKEND` / `ZHULONG_VISION_BACKEND`）
- `build/deps/` 独立前缀的获取与 SHA-256 校验模式（`build.py` 扩展）
- `runtime_factory.cpp` 的编译期分支结构

若 RKNN 任务未完成，本任务需先补这些机制（等于重复劳动），因此不建议并行。

## 1. 实施清单

### 阶段 A：构建接入（复用 RKNN 任务建立的机制）

- [ ] 在 `native/CMakeLists.txt` 补 `ZHULONG_VISION_BACKEND` 的 `rga` 分支：`-DZHULONG_RGA_ROOT` 必需，缺则 `FATAL_ERROR`。
  - 验证：`cmake -DZHULONG_VISION_BACKEND=rga` 无 `ZHULONG_RGA_ROOT` 时报错退出。
- [ ] 新建 `native/src/backends/rga/` 目录；CMake 中 include 一律 `PRIVATE`。
  - 验证：`grep -rl 'rga/im2d' native/src/ native/sdk/ native/include/` 只命中 `backends/rga/`。
- [ ] 扩展 `build.py`：支持 RGA 前缀获取与 SHA-256 校验（`im2d.h` = `5ee06891…`，`rga.h` = `f50214c9…`）。
  - ⚠️ 保持离线；从板端 `scp`。
  - ⚠️ `scp` 时三级符号链都要：`librga.so` / `librga.so.2` / `librga.so.2.1.0`。
  - ⚠️ 不塞进 `build/sysroots/rk3568`。
- [ ] `runtime_factory.cpp` 加 `#ifdef ZHULONG_VISION_RGA` 分支。

### 阶段 B：提取共用几何计算

- [ ] 把 Letterbox 的几何计算（`scale` / `unpad_w` / `unpad_h` / `pad_left` / `pad_top`）从 `CpuVisionBackend::letterbox` 提取为 `runtime/` 内的自由函数。
  - ⚠️ 提取范围**严格限定**在这一个纯计算函数。
  - ⚠️ 不改 `IVisionBackend` 接口，不重构 `CpuVisionBackend` 其他部分，**不搬 `nms`**（与 RGA 无关）。
  - 验证：`make native-test` 既有 letterbox 测试全绿，输出逐字节不变。

### 阶段 C：RGA 后端实现

- [ ] 从板端 `/usr/include/rga/im2d_type.h` 读取真实 `IM_STATUS_*` 枚举名，写 `rga_error_map.cpp`。
- [ ] 实现 `resize`：`wrapbuffer_virtualaddr` + `imresize`。
  - 包装层先做 CPU 同款参数校验（空指针、非正尺寸、stride 合法性），不把非法参数交给 RGA。
  - `stride == 0` 转为 `width * 3`（保持 CPU 语义）。
  - 通道交换映射到 RGA 的 `RK_FORMAT_RGB_888` / `RK_FORMAT_BGR_888`。
- [ ] 实现 `letterbox`：`memset 114` → 复用阶段 B 的几何函数 → ROI 上 `imresize`。
  - ⚠️ ROI 的 wstride 对齐要求必须先 `imcheck()` 验证。
- [ ] 实现 `RgaBufferGuard` RAII：`importbuffer_*` 创建的 handle 必须 `releasebuffer_handle`。
  - ⚠️ 本任务**只用 `wrapbuffer_virtualaddr`**，不引入 `importbuffer_fd`（DMA-BUF 属后续任务）。
- [ ] 实现透明回退：`RgaVisionBackend` **组合**（非继承）一个 `CpuVisionBackend`，任一 RGA 前置条件不满足时转调。
  - 构造时探测：`imcheck()` + `/dev/rga` 可访问性。
  - ⚠️ 回退对调用方完全透明：返回 `OK`，输出满足同一合同（HWC 3 通道 `UINT8`、补边 114、尺寸/padding 一致）。
- [ ] 加异常捕获：`letterbox`/`resize` 内部 `try/catch(...)` 转状态码，输出清零。

### 阶段 D：格式能力与真机验证

- [ ] 能力矩阵测试：`RGB24`/`BGR24` 双路径成功；`NV12`/`YUV420P` RGA 成功、CPU `NOT_SUPPORTED`；`UNKNOWN` 双路径 `NOT_SUPPORTED`。
  - ⚠️ `RGBA32` 支持情况实测确认，不从头文件推断。
- [ ] 像素比对：RGA vs CPU 同输入逐像素，定义容差并固化进测试。
  - ⚠️ 容差实测确定，不预填。
- [ ] 真机记录：`imcheck()` 结果、库版本 `1.10.1_[10]`、驱动 v1.3.4、`cat /sys/kernel/debug/rkrga/hardware` 的 `byte_stride_align`（预期 **4**）。
- [ ] 包装层实现 §5.3 的约束适配策略：
  - **形式 1（改参数）**：stride 不对齐 → 传实际值或分配时取 16 对齐；坐标不对齐 → 取整。
  - **形式 2（重排内存）一律不采用** → 直接回退 CPU（CPU 已遍历全图，RGA 收益归零）。
  - 适配后仍需满足 §5.0 的格式专属要求（RGB888 需 4 对齐、10bit YUV 需 16 对齐）。
  - 验证：单测区分「适配后成功」与「回退成功」两条路径，日志可辨（AC9）。
- [ ] 实现两阶段裁剪路径（§5.4）：用 `improcess(src, dst, srect, drect, ...)` 一次完成裁剪+缩放+CSC。
  - ⚠️ `imresize_t` **没有** rect 参数，裁剪必须用 `improcess`。
  - ⚠️ `srect` 坐标取整（形式 1，内存不动）；取整方向默认**向外扩**（保留目标完整），但需算法方确认。
  - ⚠️ **不得走「CPU 裁剪到新 buffer + RGA」**——那是形式 2（O(N)）负优化。
  - 验证：与 CPU 精确裁剪的输出在容差内一致（AC10）。
- [ ] 对齐值**运行期探测**，不硬编码：优先读 `/sys/kernel/debug/rkrga/hardware` 的 `byte_stride_align`，其次首次 `imcheck()`；两处都取不到则回退 CPU（不猜）。
  - ⚠️ 硬编码 4 会在 RK3576/RK3588（RGA3 = 16）静默出错；硬编码 16 会浪费 RGA2 带宽。
- [ ] `NV12`/`YUV420P` 的多 plane stride 实测；确认 `wrapbuffer_virtualaddr` 是否够用（对齐要求已知：YUV 需 2 对齐）。
- [ ] ⚠️ **不实现 RGA3 的 offset 适配**（首期只做 RK3568）；但把 §5.3 的决策树写入代码注释，供后续使用。
- [ ] 无 `/dev/rga` 环境下的回退验证（可用权限模拟）。

### 阶段 E：验收核对

- [ ] AC1–AC7 逐项核对。
- [ ] `readelf -d` 检查：`ZhulongAlgoRuntime.so` 依赖 `librga.so`，宿主 `Zhulong` 不依赖。
- [ ] 部署文档记录 `/dev/rga` 的 `root:video` 权限要求与符号链。
- [ ] 更新 `.trellis/spec/native/algo-plugin-contract.md` 记录 RGA 实际合同与格式能力矩阵。
- [ ] 记录残余风险。

## 2. 验证命令

```bash
# CPU profile 回归
make native-test

# 头文件隔离
grep -rl 'rga/im2d' native/src/ native/sdk/ native/include/

# 宿主无 RGA 依赖（交叉产物）
readelf -d build/targets/rk3568/Zhulong | grep NEEDED

# 交叉构建
CROSS_PROFILE=native/profiles/rk3568.json CROSS_OUTPUT=build/targets/rk3568/Zhulong make native-build

# 收工门禁
make check
```

⚠️ 交叉**只允许编译，不允许执行测试**（AGENTS.md）。

## 3. 回退点

- 阶段 A 失败：不改 CMake，RGA 后端整体不启用，CPU 路径不受影响。
- 阶段 B 失败（几何函数提取引入回归）：还原提取，RGA 后端改为自己实现几何计算（接受少量重复）。
- 阶段 C 失败：保留 `backends/rga/` 骨架但默认 `cpu`，RGA 实现标记未完成。
- 阶段 D 若 `NV12`/`YUV420P` 实测不可用：范围收窄为 `RGB24`/`BGR24`，格式能力矩阵如实降级。
- 任何阶段：不修改 `IVisionBackend` 接口、`plugin.h`、`tensor.hpp`，保证既有插件 ABI 与 CPU 路径零回归。

## 4. 已知风险

- ⚠️ RGA 对齐要求**分两套**（stride / offset）且**依版本变化**（RK3568：4/2；RGA3：16/64），硬编码会导致跨平台静默出错。
- ⚠️ **`srect` 对齐要求未实测**，推测与 `x_offset` 相同；**RGA3 上取整偏移最大 63 像素**，需算法方确认取整方向。
- ⚠️ **混合核不确定性**：RK3576/RK3588 同时含 RGA2-Pro 与 RGA3，用户态不可控 job 落哪个核。
- ⚠️ `x_offset`/`y_offset` 的 64 对齐**单位（像素 vs 字节）未确认**，是 RGA3 支持的关键未知。
- ⚠️ O(N) 适配的性能模型是**粗算**，未实测；若实测 CPU 适配比预期便宜，§5.3 判据可能需修正。
- ⚠️ `NV12`/`YUV420P` 多 plane 的 stride 语义未知，可能需要 `importbuffer` 而非 `wrapbuffer_virtualaddr`。
- ⚠️ RGA 与 CPU 的缩放实现差异可能使像素容差超预期。
- ⚠️ 板端磁盘仅剩 **2.9G**。
- ⚠️ 编译器代差（宿主交叉 14.2.0 / sysroot libstdc++ 10 / 板端 10.2.1）。
- ⚠️ `/dev/rga` 权限（`root:video`），运行进程需在 `video` 组。
- ⚠️ 与 RKNN 后端同时启用时的 DDR 带宽共享（硬件不同但内存带宽共用）。
- ⚠️ 本任务串行依赖 RKNN 任务建立的机制；若并行会冲突。
