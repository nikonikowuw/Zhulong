# 原生硬件加速后端规范 (Hardware Backends: RKNN & RGA)

> 异构芯片（Rockchip NPU/RGA）加速后端编译期接入、链接隔离、运行时对齐约束与硬核避坑规约。

---

## 1. 编译期后端配置与正交开关

硬件加速后端由公共运行库 `libzhulong_algo_runtime.so` 实现，在**编译期**通过 CMake cache 变量静态绑定，不引入运行期 `dlopen` 分流：

| 变量 | 取值 | 说明 |
| :--- | :--- | :--- |
| `ZHULONG_INFERENCE_BACKEND` | `cpu`（默认）\| `rknn` | 推理后端选择 |
| `ZHULONG_VISION_BACKEND` | `cpu`（默认）\| `rga` | 视觉前处理后端选择 |
| `ZHULONG_RKNN_ROOT` | 路径 | 仅 `rknn` 需要；须含 `include/rknn_api.h` 与 `lib/librknnrt.so` |
| `ZHULONG_RGA_ROOT` | 路径 | 仅 `rga` 需要；须含 `include/rga/im2d.h` 与 `lib/librga.so` |

- **编译防御**：`rknn` 缺 `ZHULONG_RKNN_ROOT` 或 `rga` 缺 `ZHULONG_RGA_ROOT` 时，CMake 触发 `FATAL_ERROR`（fail closed）。
- **正交性**：两个开关完全解耦，支持任意组合（如「RKNN 推理 + CPU 前处理」、「RKNN 推理 + RGA 前处理」等）。
- **目标产物归属**：后端实现直接编译进 `libzhulong_algo_runtime.so`。宿主二进制 `Zhulong` 仅静态链接 `libZhulongEngine.a`，**严禁向宿主引入任何厂商 SDK 动态库依赖**。
- **Profile 分流**：`cpu` 与 `rknn` 采用不同的交叉 profile（`native/profiles/rk3568.json` 与 `rk3568-rknn.json`），避免 mock 插件在 rknn profile 下因缺少真实模型而启动失败。

---

## 2. Rockchip RKNN 推理后端契约 (NPU)

*硬件基线：RK3568（Debian 11 arm64 / 内核 5.10.226 / `librknnrt.so` 2.3.2 / RKNPU 驱动 0.9.8）。*

### 2.1 依赖隔离与符号管理

1. **头文件绝对隔离**：`rknn_api.h` 仅允许在 `native/src/backends/rknn/` 内包含，严禁外溢至公开 SDK 头文件。
2. **正确链接目标**：
   - 必须动态链接 `librknnrt.so`（SONAME 即 `librknnrt.so`）。
   - ⚠️ **严禁链接陈旧的 `librknn_api.so`**（板端历史残留 1.5.2 版本，会导致运行期符号缺失）。
3. **RPATH 与链接选项**：
   - 必须显式添加 `-Wl,-rpath-link,<rknn>/lib` 供 GNU ld 解析传递依赖符号；
   - 必须清空产物 `BUILD_RPATH`（`set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")`），杜绝开发机绝对路径烧入 `.so`。

### 2.2 ⚠️ `RKNN_QUERY_MEM_SIZE` 破坏上下文规避（2.3.2 缺陷）

- **故障现象**：执行过 `RKNN_QUERY_MEM_SIZE` 的 `rknn_context`，后续执行 `rknn_dup_context` 时必定发生 **SIGSEGV** 崩溃在 `librknnrt.so` 内部。
- **强制规避契约**：**主 root 上下文严禁调用 `RKNN_QUERY_MEM_SIZE`**。若需获取内存大小用于审计/遥测，必须通过独立临时上下文查询后立即销毁：

```cpp
rknn_context probe = 0;
if (rknn_init(&probe, path, 0, 0, nullptr) == RKNN_SUCC) {
    rknn_query(probe, RKNN_QUERY_MEM_SIZE, &asset->mem_size, sizeof(asset->mem_size));
    rknn_destroy(probe);
}
```

### 2.3 权重共享与会话派生 (`rknn_dup_context`)

- **实测收益**：RK3568 真机验证，同一模型 4 个 Worker 会话，1 次 `rknn_init` + 4 次 `rknn_dup_context` 相比 4 次独立 init，常驻内存增量从 50.4MB 骤降至 14.3MB（**节省约 3.5× 物理内存**）。
- **串行化派生**：`rknn_dup_context` 的派生调用必须受互斥锁（`dup_mutex`）保护，不可并发调用。
- **生命周期所有权**：
  - `RknnModelAsset` 持有根上下文与 `dup_mutex`，进程内多 Worker 共享；
  - `RknnSessionContext` 持有派生上下文与所属 ModelAsset 的 `shared_ptr`，确保根上下文晚于所有 Worker 会话销毁。
  - 创建过程失败时必须严格回滚资源，杜绝 double-destroy。

### 2.4 错误码映射表 (14 码覆盖)

| RKNN 状态码 | 统一映射状态 | 语义说明 |
| :--- | :--- | :--- |
| `RKNN_SUCC` (0) | `ZHULONG_STATUS_OK` | 成功 |
| `PARAM_INVALID`, `MODEL_INVALID`, `CTX_INVALID`, `INPUT_INVALID`, `OUTPUT_INVALID` | `ZHULONG_STATUS_INVALID_ARGUMENT` | 参数或模型格式非法 |
| `DEVICE_UNAVAILABLE`, `DEVICE_UNMATCH`, `TARGET_PLATFORM_UNMATCH`, `INCOMPATIBLE_*` | `ZHULONG_STATUS_NOT_SUPPORTED` | 运行环境或平台不匹配（非代码缺陷） |
| `MALLOC_FAIL` | `ZHULONG_STATUS_OUT_OF_MEMORY` | 内存不足 |
| `TIMEOUT` | `ZHULONG_STATUS_TIMEOUT` | 超时 |
| `FAIL` 及其他未知码 | `ZHULONG_STATUS_INTERNAL` | 底层内部错误 |

### 2.5 数据传输与并发约束

1. **输入缓冲要求 Packed**：`rknn_input` 无 stride 字段，传入内存必须紧凑排布。输入校验按元素总数 `inputs[i].element_count() == attr.n_elems` 比对，适配 UINT8 图像到 FP16 模型的内部自动转换。
2. **输出 Float32 自动归一**：设置 `want_float = 1` 由 runtime 统一写出 float32，避免手写反量化公式。
3. **输出资源释放 Guard**：`rknn_outputs_release` 必须由 RAII guard 管理，保证异常路径亦被释放。
4. **单核 NPU 特性**：RK3568 为单核 NPU，增加 Worker 数仅能重叠前后处理时间，不能提升纯 NPU 推理并行度；严禁开启未经充分验证的 `RKNN_FLAG_ASYNC_MASK`。

---

## 3. Rockchip RGA 视觉前处理后端契约 (2D Rasterizer)

*硬件基线：RK3568（Debian 11 bullseye arm64 / 内核 5.10.226 / RGA 驱动 v1.3.4 / `librga.so.2.1.0` API `v1.10.1_[10]`）。*

### 3.1 运行期硬件探测与对齐门禁

RGA 后端启动时必须执行硬件能力自适应探测，**严禁硬编码对齐参数**：

- **行跨度对齐 (`byte_stride_align`)**：优先从 `/sys/kernel/debug/rkrga/hardware` 读取（RK3568 实测为 **4** 字节）。在 debugfs 不可读的无特权容器环境下安全回退至平台基准对齐值（4 字节），杜绝静默瘫痪退化。
- **ROI 偏移对齐 (`offset_align`)**：在测试缓冲调用 `imcheck_t` 动态探测（RK3568 实测为 **2** 像素）。
- **设备权限约束**：`/dev/rga` 设备节点属组为 `root:video`。启动探针必须使用 `open("/dev/rga", O_RDWR | O_CLOEXEC)` 校验读写权限，运行 Runner 的系统用户**必须加入 `video` 组**。

### 3.2 格式能力矩阵与颜色转换语义

| 输入格式 | RGA 硬件加速 | CPU 回退行为 | 约束与内存布局 |
| :--- | :---: | :---: | :--- |
| `RGB24` / `BGR24` | ✅ | ✅ | 紧凑排布或满足 4 字节行对齐；目标输出为 HWC UINT8 |
| `RGBA32` | ✅ | ❌ | 单平面 4 字节/像素，硬件完成 CSC 提取与缩放 |
| `NV12` | ✅ | ❌ | **仅支持连续内存**；实际垂直步长通过 `(p1 - p0) / y_stride` 动态推导并传给 `hstride`（支持紧凑及 MPP 16 行对齐帧）；非连续内存返回 `NOT_SUPPORTED` |
| `YUV420P` | ✅ | ❌ | **仅支持连续内存**（plane1/plane2 紧密相连并与推导的 hstride 匹配）；非连续内存返回 `NOT_SUPPORTED` |
| `UNKNOWN` | ❌ | ❌ | 显式返回 `ZHULONG_STATUS_NOT_SUPPORTED` |

- **色彩空间转换**：硬件 CSC 采用 **Limited-range BT.601** 标准公式（YUV 16-235 映射至 RGB 0-255）。

### 3.3 防挂死契约与两阶段裁剪 (Anti-Hang Rules)

1. ⚠️ **严禁对目标缓冲区做指针偏移（防 DMA OOB 越界死锁）**：
   - **致命陷阱**：在 Letterbox 计算中，若将目标 ROI 指针（`dst_buf + roi_offset`）直接传给 `wrapbuffer_virtualaddr`，驱动会基于该偏移指针按完整 `wstride × hstride` 计算 DMA 地址，导致访问越出 Tensor 物理边界，引发 RGA 硬件陷入 D 状态死锁并导致板端看门狗复位！
   - **正确做法**：目标缓冲恒定以全尺寸（`target_w × target_h`）包装为 `dst_buffer`，指针本身不偏移；偏移量完全通过 `improcess` 的 `drect`（`pad_left, pad_top, unpad_w, unpad_h`）传递。
2. ⚠️ **强依赖 `imcheck_t` 预校验**：每次调用 `improcess` 前必须使用 `imcheck_t` 预校验 `srect` 与 `drect` 合法性，失败直接在软件层拦截并回退 CPU。
3. **两阶段裁剪对齐适配 (`crop_scale`)**：当裁剪 ROI 坐标不满足 2 像素对齐时，采用**参数向外扩取整**适配，杜绝原地内存搬运。
4. **滤波差异验收容差**：RGA 硬件双线性插值与 CPU 浮点中心双线性滤波核函数不同，下采样（如 1080P → 640x640）单像素极值差可达 119。测试断言采用 `worst <= 128` 判定一致性。
5. **热路径 Tensor 原地复用**：`try_rga_letterbox` 传入容量充足且形状匹配的 CPU Tensor 时原地复用内存，禁止每帧重新执行 `dst = Tensor(...)` 堆分配。

---

## 4. 全链路 DMA-BUF 硬件零拷贝流转契约 (Zero-Copy Pipeline)

*覆盖链路：解码 ➔ FrameBroker ➔ SCM_RIGHTS ➔ RGA 视觉前处理 ➔ RKNPU2 零拷贝绑定。*

### 4.1 底层物理分配器 (`DmaBufAllocator`)

- **分配优先级探测**：
  1. `/dev/dma_heap/cma`：优先分配连续物理内存块，满足硬件硬性物理连续要求；
  2. `/dev/dma_heap/system`：系统级 DMA-Heap 分配；
  3. `/dev/dri/card0` (DRM Dumb Buffer)：通过 DRM ioctl 创建并导出 DMA-BUF（`DRM_IOCTL_PRIME_HANDLE_TO_FD`）。⚠️ **KMS 限制**：Dumb Buffer 属于显示管理功能，仅允许在主节点 `/dev/dri/card*` 创建，严禁在渲染节点 `/dev/dri/renderD*` 创建（内核会拒绝并返回 `EACCES`）；且探测时必须发起最小 `CREATE_DUMB` 真实探针校验，防止部分驱动开放 card 节点却不支持 dumb 造成的不可用假阳性；
  4. 宿主测试环境/非 Linux 平台自动回退为不可用（`is_supported() == false`）。
- **FrameBroker 双轨分发与零拷贝穿透**：
  - 当上游硬件帧具备有效 `HardwareFrame::dma_fd >= 0` 时，`publish_frame` 直接分发该文件描述符，**完全杜绝任何 CPU `std::memcpy` 与 Cache 刷新**；
  - 当为 CPU 软解帧时（`dma_fd < 0`），写入 POSIX 共享内存池（`shm_pool_`）以兼容标准 SHM 客户端；若硬件环境激活了 `dma_slots_`，则同步写入 DMA-BUF 槽位并刷出 Cache，供下游加速插件直绑。
- **CPU Cache 同步括号 (Cache Synchronization Bracket)**：
  - CPU 读写前：调用 `sync_start(DMA_BUF_SYNC_READ | DMA_BUF_SYNC_WRITE)` 触发 `DMA_BUF_IOCTL_SYNC`；
  - 硬件外设介入前：调用 `sync_end(DMA_BUF_SYNC_READ | DMA_BUF_SYNC_WRITE)` 刷回 Cache Line；
  - 针对 `EINTR` 信号中断提供带退避的自动重试机制。

### 4.2 RGA 硬件加速与 `wrapbuffer_fd`

- 当输入源帧具备有效 `dma_buf_fd >= 0` 时，RGA 视觉后端优先调用 `wrapbuffer_fd`：
  ```cpp
  rga_buffer_t src_buf = wrapbuffer_fd(src.dma_buf_fd, src.width, src.height, rga_fmt, src.strides[0], hstride);
  ```
- 目标输出 `Tensor` 同理支持通过 `tensor.dma_buf_fd()` 直接进行 `wrapbuffer_fd`。
- **混合模式保护**：当 `dma_buf_fd < 0` 时，透明安全回退至已有的 `wrapbuffer_virtualaddr` 虚拟地址通路。

### 4.3 RKNPU2 零拷贝张量绑定 (`rknn_create_mem_from_fd`)

- **零内存拷贝输入导入**：
  当输入张量是由有效 DMA-BUF 派生时（`tensor.dma_buf_fd() >= 0`），使用 `rknn_create_mem_from_fd` 将 DMA-BUF fd 注册为 NPU 内存对象，并调用 `rknn_set_io_mem` 完成直绑：
  ```cpp
  rknn_tensor_mem* mem = rknn_create_mem_from_fd(ctx, tensor.dma_buf_fd(), tensor.data(), tensor.byte_size(), 0);
  rknn_set_io_mem(ctx, mem, &attr);
  ```
- **资源生命周期 Guard**：绑定生成的 `rknn_tensor_mem` 在执行 `rknn_run` 完毕后及时通过 `rknn_destroy_mem` 释放 NPU IO 映射，避免 NPU 内存上下文泄露。
- **软硬件自动降级**：当输入张量为常规 CPU 虚拟内存时，后端自动采用标准的 `rknn_inputs_set` 内存拷贝流程。

---

## 5. Rockchip MPP 硬件视频解码后端契约 (VPU)

*硬件基线：RK3568（Debian 11 bullseye arm64 / 内核 5.10.226 / `librockchip_mpp.so.1` 1.5.0-1 / VPU 驱动 `/dev/mpp_service`）。*

### 5.1 动态加载与跨平台编译隔离

- **杜绝编译期链接**：宿主引擎二进制 `libZhulongEngine.a` 及测试套件**严禁静态或动态链接 `librockchip_mpp.so`**。在非 RK 硬件（macOS、x86_64 Linux CI 开发机）上编译时，必须零链接错误且无需安装 MPP 库文件。
- **动态符号加载 (`MppLoader`)**：
  - 采用单例懒加载模式，运行时优先探测内核字符设备 `/dev/mpp_service` 是否存在且具备读写权限；
  - 探测成功后通过 `dlopen` 按顺序探测 `librockchip_mpp.so.1` 和 `librockchip_mpp.so`，并通过 `dlsym` 绑定核心 API 函数表（`mpp_create`, `mpp_init`, `mpp_destroy`, `mpp_packet_*`, `mpp_frame_*`, `mpp_buffer_*` 等）；
  - 若任一关键符号缺失或 `dlopen` 失败，`MppLoader::is_supported()` 返回 `false`，解码工厂透明回退至 `FFmpegDecodeNode` 软解。

### 5.2 状态机配置与防死锁时序

1. ⚠️ **Parser 命令字前置配置契约 (Need to setup before init)**：
   - Rockchip MPP 内部的流切分器与快速解析模式**必须在 `mpp_init` 调用前配置完成**：
     ```cpp
     uint32_t split_mode = 1;
     mpi->control(ctx, MPP_DEC_SET_PARSER_SPLIT_MODE, &split_mode);
     uint32_t fast_mode = 1;
     mpi->control(ctx, MPP_DEC_SET_PARSER_FAST_MODE, &fast_mode);
     mpp_init(ctx, MPP_CTX_DEC, coding);
     ```
   - 若在 `mpp_init` 后调用，切分器无法生效，导致单包输入时无法识别 Access Unit 边界并产生首帧滞留。
2. **两阶段非阻塞拉取与 `info_change` 处理**：
   - 解码器配置超时为 0（`MPP_SET_INPUT_TIMEOUT` 与 `MPP_SET_OUTPUT_TIMEOUT` 为 0）；
   - 当 `mpp_frame_get_info_change(frame)` 为真时，读取实际画面尺寸与步长，创建或复用外部缓冲组 `buf_group`，并立即下发 `MPP_DEC_SET_EXT_BUF_GROUP` 与 `MPP_DEC_SET_INFO_CHANGE_READY`；
   - ⚠️ `info_change` 帧不包含图像有效载荷，**必须立即调用 `mpp_frame_deinit(&frame)` 销毁**，严禁将其当作有效画面送出。
3. **即时回放与快速输出**：
   - 启用 `MPP_DEC_SET_ENABLE_FAST_PLAY` 与 `MPP_DEC_SET_IMMEDIATE_OUT`，确保首个 IDR 关键帧无需积攒后续 P 帧即可立即解算输出。

### 5.3 底层 DMA-BUF 提取与 RAII 租借生命周期

1. **独立文件描述符所有权 (`dup`)**：
   - 从 `mpp_frame_get_buffer(frame)` 提取底层 `MppBuffer`；
   - 驱动返回的 `mpp_buffer_get_fd(buf)` 在 MPP 内部受生命周期管理。为避免下游异步消费与解码器内部复用产生竞态，必须通过 `int owned_fd = ::dup(raw_fd)` 获取独立文件描述符回填至 `HardwareFrame::dma_fd`。
2. ⚠️ **帧池防死锁关键时序 (Ref Increment & Immediate Deinit)**：
   - 取得 `MppBuffer` 后，必须调用 `mpp_buffer_inc_ref(buf)` 显式增加底层物理缓冲区的引用计数；
   - 紧接着**立即调用 `mpp_frame_deinit(&frame)` 释放外层 `MppFrame` 描述符容器**！若持有 `MppFrame` 直至下游消费完毕，MPP 内部的固定槽位（通常为 16~24 槽位）将在几帧后耗尽，导致硬件解码器永久挂起陷入死锁；
   - 将 `mpp_buffer_put(buf)` 绑定在 `HardwareFrame::release_fn` RAII 析构闭包中，并由 `close(owned_fd)` 释放独立描述符。当下游算法或 `FrameBroker` 消费完毕时，自动归还底层 VPU 缓冲池。
3. **硬件步长对齐与格式保真**：
   - 导出的图像格式恒定标记为 `FRAME_FORMAT_NV12`；
   - 水平步长 `hor_stride` 遵循 16 字节对齐，垂直步长 `ver_stride` 遵循 16 行对齐；
   - `HardwareFrame` 的 `stride` 与 `vstride` 严格填入实际物理跨度，确保下游 RGA 与 RKNN 能够以真实的物理行宽进行零拷贝直绑与 Letterbox 转换。

---

## 6. 验证命令

```bash
# 1. 检查头文件依赖隔离（仅允许在 native/src/backends/rknn/ 引用）
grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/

# 2. 检查宿主与动态库依赖（宿主绝对不链接厂商 SDK）
readelf -d build/native/cross-rk3568-rknn-rga/libZhulongAlgoRuntime.so | grep -E 'NEEDED|RUNPATH'
readelf -d build/targets/rk3568-rknn-rga/Zhulong | grep NEEDED

# 3. 运行本地 DMA-BUF 分配器与 IPC SCM_RIGHTS 传输单元测试
python3 native/scripts/build.py test

# 4. 交叉构建并在 RK3568 板端验证硬件解码与全链路零拷贝
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn-rga.json build
scp build/native/cross-rk3568-rknn-rga/ZhulongDecoderTests rk3568:/tmp/
ssh rk3568 "/tmp/ZhulongDecoderTests"
```
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn.json build
```
