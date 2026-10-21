# 原生推理与硬件加速后端规范 (Hardware & Inference Backends: RKNN, ONNX & RGA)

> 异构芯片（Rockchip NPU/RGA）与通用跨平台推理引擎（ONNX Runtime）编译期接入、链接隔离、运行时对齐约束与硬核避坑规约。

---

## 1. 编译期后端配置与正交开关

硬件加速后端由公共运行库 `libzhulong_algo_runtime.so` 实现，在**编译期**通过 CMake cache 变量静态绑定，不引入运行期 `dlopen` 分流：

| 变量 | 取值 | 说明 |
| :--- | :--- | :--- |
| `ZHULONG_INFERENCE_BACKEND` | `cpu`（默认）\| `rknn` \| `onnx` | 推理后端选择 |
| `ZHULONG_VISION_BACKEND` | `cpu`（默认）\| `rga` | 视觉前处理后端选择 |
| `ZHULONG_RKNN_ROOT` | 路径 | 仅 `rknn` 需要；须含 `include/rknn_api.h` 与 `lib/librknnrt.so` |
| `ZHULONG_ONNX_ROOT` | 路径 | 仅 `onnx` 需要；须含 `include/onnxruntime_cxx_api.h` 与 `lib/libonnxruntime.so` |
| `ZHULONG_RGA_ROOT` | 路径 | 仅 `rga` 需要；须含 `include/rga/im2d.h` 与 `lib/librga.so` |

- **编译防御**：`rknn` 缺 `ZHULONG_RKNN_ROOT`、`onnx` 缺 `ZHULONG_ONNX_ROOT` 或 `rga` 缺 `ZHULONG_RGA_ROOT` 时，CMake 触发 `FATAL_ERROR`（fail closed）。
- **正交性**：两个开关完全解耦，支持任意组合（如「RKNN 推理 + CPU 前处理」、「RKNN 推理 + RGA 前处理」等）。
- **目标产物归属**：后端实现直接编译进 `libzhulong_algo_runtime.so`。宿主二进制 `Zhulong` 仅静态链接 `libZhulongEngine.a`，**严禁向宿主引入任何厂商 SDK 动态库依赖**。
- **Profile 分流**：`cpu` 与 `rknn` 采用不同的交叉 profile（`native/profiles/rk3568.json` 与 `rk3568-rknn.json`），避免 mock 插件在 rknn profile 下因缺少真实模型而启动失败。

### 1.1 后端的运行期入口与优先级（环境变量是统一开关）

每次 `native/scripts/build.py` 调用都会重新 configure CMake，而 CMake 缓存里的 `ZHULONG_INFERENCE_BACKEND` 会被无参 configure **静默重置回 `cpu`**。因此一次性的 `build.py --inference-backend onnx build` 会被随后的任何 `make dev` / `make build` / `make check` 改回 `cpu` 并重链出桩后端。

`build.py` 的实际解析顺序（`vision_backend` 同理，对应 `ZHULONG_VISION_BACKEND`）：

| 优先级 | 来源 | 说明 |
| :--- | :--- | :--- |
| 1 | `--inference-backend <v>`（CLI） | 写入 profile 字典，覆盖同名字段；仅对当次调用生效 |
| 2 | profile JSON 的 `inference_backend` | 交叉 profile 的声明值（如 `rk3568-rknn.json` → `rknn`）**优先于环境变量** |
| 3 | `ZHULONG_INFERENCE_BACKEND` 环境变量 | 仅在 profile 未声明该键时生效；Air（`.air.toml` 的 build cmd 独立调用 build.py）由此继承 |
| 4 | `cpu` | 兜底默认 |

- 实现为 `value = p.get(key) or os.environ.get(env_key) or "cpu"`，取值必须落在白名单内，否则 `ValueError`（fail closed）。
- `Makefile` 暴露 `INFERENCE_BACKEND` 并将其 `export` 为 `ZHULONG_INFERENCE_BACKEND`，使 `make` 与 Air 看到同一选择：`make INFERENCE_BACKEND=onnx check`。由于 profile 优先级更高，该变量实际作用于未声明后端的 profile（Host），交叉构建仍沿用 profile 内的声明。
- ⚠️ **不要无条件注入 `--inference-backend cpu`**：CLI 优先级最高，那会覆盖交叉 profile 的 `rknn` 选择。留空即「沿用 profile」。

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
2. **输出 Float32 零拷贝直写 (`is_prealloc = 1`)**：设置 `want_float = 1` 与 `is_prealloc = 1`，输出前预分配目标 `Tensor` 并将 `raw_data()` 挂载给 `rknn_output.buf`，由底层 runtime 直接写出 float32，彻底消除中间临时堆分配（malloc/free）与 `memcpy` 拷贝开销。校验 `rknn_outputs[i].size == tensor.byte_size()` 防范 Stride 截断错位。
3. **输出资源释放 Guard**：`rknn_outputs_release` 必须由 RAII guard 管理；当 `is_prealloc = 1` 时只释放底层句柄，不释放外部 Tensor 缓冲区。
4. **单核 NPU 特性**：RK3568 为单核 NPU，增加 Worker 数仅能重叠前后处理时间，不能提升纯 NPU 推理并行度；严禁开启未经充分验证的 `RKNN_FLAG_ASYNC_MASK`。
5. **C++ 资源规范 (Rule of 5)**：`RknnModelAsset` 与 `RknnSessionContext` 显式禁用拷贝构造函数与拷贝赋值运算符，杜绝底层 `rknn_context` 双重释放。

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

- **分配优先级探测与多 BSP 动态扫描**：
  1. `/dev/dma_heap/` 动态遍历扫描：优先自适应匹配名称含 `cma` 的物理连续堆（如 `cma`、`rk-dma-heap-cma`、`linux,cma`），满足硬件硬性物理连续要求；
  2. 系统散布堆：次选匹配名称含 `system`、`sys` 或 `uncached` 的系统级 DMA-Heap 节点；
  3. `/dev/dri/card0` (DRM Dumb Buffer)：通过 DRM ioctl 创建并导出 DMA-BUF（`DRM_IOCTL_PRIME_HANDLE_TO_FD`）。
     - ⚠️ **KMS 限制与二维化步长**：Dumb Buffer 仅允许在主节点 `/dev/dri/card*` 创建，严禁在渲染节点 `/dev/dri/renderD*` 创建（内核会拒绝并返回 `EACCES`）；为突破硬件 `max_width` 几何尺寸限制并支持 1080p/4K 大分辨率，分配时统一固定宽度为 4096 像素并折算高度，映射实际内核返回的 `create_req.size`；
     - ⚠️ **FD 独立所有权**：关联 DRM Dumb 时由 `DmaBuffer` 内部通过 `::dup(drm_fd)` 持有独立文件描述符并于析构时自主关闭，杜绝依赖外部单例共享描述符产生的 UAF 与误伤复用描述符隐患；
  4. 宿主测试环境/非 Linux 平台自动回退为不可用（`is_supported() == false`）。
- **FrameBroker 双轨分发与零拷贝穿透**：
  - 当上游硬件帧具备有效 `HardwareFrame::dma_fd >= 0` 时，`publish_frame` 直接分发该文件描述符，**完全杜绝任何 CPU `std::memcpy` 与 Cache 刷新**；
  - 当为 CPU 软解帧时（`dma_fd < 0`），写入 POSIX 共享内存池（`shm_pool_`）以兼容标准 SHM 客户端；若硬件环境激活了 `dma_slots_`，则同步写入 DMA-BUF 槽位并由 `ScopedCpuSync` RAII 守卫刷出 Cache，供下游加速插件直绑。
- **CPU Cache 同步括号与 RAII 守卫 (ScopedCpuSync)**：
  - CPU 读写前：调用 `sync_start(DMA_BUF_SYNC_READ | DMA_BUF_SYNC_WRITE)` 触发 `DMA_BUF_IOCTL_SYNC`；
  - 硬件外设介入前：调用 `sync_end(DMA_BUF_SYNC_READ | DMA_BUF_SYNC_WRITE)` 刷回 Cache Line；
  - 推荐优先使用 `ScopedCpuSync` RAII 守卫管理生命周期，杜绝异常退出或提前返回遗漏 Cache 写回；
  - 针对 `EAGAIN` / `EINTR` 提供带 100 微秒自旋退避和 50 次重试上限的防护机制，杜绝 100% CPU 紧凑死循环。

### 4.2 RGA 硬件加速与 `wrapbuffer_fd`

- 当输入源帧具备有效 `dma_buf_fd >= 0` 时，RGA 视觉后端优先调用 `wrapbuffer_fd`：
  ```cpp
  rga_buffer_t src_buf = wrapbuffer_fd(src.dma_buf_fd, src.width, src.height, rga_fmt, src.strides[0], hstride);
  ```
- 目标输出 `Tensor` 同理支持通过 `tensor.dma_buf_fd()` 直接进行 `wrapbuffer_fd`。
- **混合模式保护**：当 `dma_buf_fd < 0` 时，透明安全回退至已有的 `wrapbuffer_virtualaddr` 虚拟地址通路。

### 4.3 RKNPU2 零拷贝张量绑定 (`rknn_create_mem_from_fd`)

- **全量 DMA-BUF 门禁（防混用冲突）**：
  Rockchip RKNPU2 底层驱动严禁同一 Context 混用常规拷贝 `rknn_inputs_set` 与零拷贝 `rknn_set_io_mem`。输入张量若存在部分提供 DMA-BUF 部分为 Host 内存，严格拦截并返回 `ZHULONG_STATUS_INVALID_ARGUMENT`；当从 DMA 模式切回 Host 模式时，主动清理会话缓存中残留的 `rknn_tensor_mem`。
- **两阶段事务提交缓存机制**：
  会话中缓存已注册的 `CachedDmaMem`。当检测到 fd/va/size 变动时：
  1. 调用 `reset()` 释放旧 `rknn_tensor_mem`；
  2. 调用 `rknn_create_mem_from_fd` 创建新句柄；
  3. 配置 `io_attr`（类型一致时设置 `pass_through = 1`）并执行 `rknn_set_io_mem`；
  4. ⚠️ **失败事务回滚**：若 `rknn_set_io_mem` 失败，立即销毁新句柄且不更新缓存元数据，杜绝下帧幽灵缓存命中跳过绑定；只有绑定成功才正式提交凭据。
- **软硬件自动降级**：当所有输入张量均为常规 CPU 虚拟内存时，后端自动采用标准的 `rknn_inputs_set` 内存拷贝流程。

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

## 6. ONNX Runtime 推理后端契约 (Cross-Platform Inference)

*运行时基线：`libonnxruntime.so` (1.16+ / 1.21)，通过 `ZHULONG_INFERENCE_BACKEND=onnx` 与 `ZHULONG_ONNX_ROOT` 编译期激活。*

### 6.1 依赖隔离与符号管理

1. **头文件绝对隔离**：
   - `<onnxruntime_cxx_api.h>` 与 `<onnxruntime_c_api.h>` 仅允许在 `native/src/backends/onnx/` 内部实现及对应白盒测试中包含，严禁外溢至 `native/sdk/include/`、`native/include/` 或 `native/src/algorithm/runtime/` 公共头文件。
   - ⚠️ **`<onnxruntime_cxx_api.h>` 静态初始化器避坑**：C++ 头文件中的 `Ort::Global<void>::api_` 会在每个包含它的编译单元生成对 `OrtGetApiBase` 的符号引用；因此 `onnx_error_map.hpp` 仅包含纯 C 头 `<onnxruntime_c_api.h>`，且 `OnnxModelAsset` / `OnnxSessionContext` 的构造与析构函数在 `onnx_inference_backend.cpp` 中类外定义（锚定 vtable 与 ORT C++ 释放调用）。
2. **链接与 RPATH 卫生**：
   - 仅 `ZhulongAlgoRuntime` 以 `PRIVATE` 方式链接 `${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so`，宿主 `libZhulongEngine.a` 绝不链接；
   - 配置 `-Wl,-rpath-link,${ZHULONG_ONNX_ROOT}/lib` 与 `SKIP_BUILD_RPATH TRUE`、`BUILD_RPATH ""`，确保 `libZhulongAlgoRuntime.so` 不残留开发机绝对路径。
3. **宿主复用 ONNX 能力的唯一通道是算法插件 + dlopen**：
   - 宿主需要通用推理能力时（如离线人脸特征提取），必须通过算法插件导出的专用 C ABI 复用 `libzhulong_algo_runtime.so`，由宿主侧 `FacePluginLoader` 在运行期 `dlopen`（见 [CGO 契约 §6](./cgo-contract.md)）；
   - 判据（必须为空）：`readelf -d build/targets/<name>/Zhulong | grep NEEDED` 不得出现 `libonnxruntime.so`；
   - 插件缺失时必须显式返回 `Zhulong_ERR_UNSUPPORTED`（Fail-Closed），严禁静默退化到模拟特征。

### 6.2 资源模型、权重共享与并发约束

1. **进程级共享 `Ort::Env`**：ORT 内部维护全局单例 `LoggingManager`，后端通过进程级静态 `std::shared_ptr<Ort::Env>` 复用环境实例，避免多次加载/卸载模型引发全局日志管理器状态冲突。
2. **不可变资产与 Worker 会话隔离 (`Rule of 5`)**：
   - `OnnxModelAsset` 持有 `std::shared_ptr<Ort::Env>`、`std::unique_ptr<Ort::Session>`（`Ort::Session::Run` 线程安全）及預解析的输入/输出 `TensorMeta` 与连续 `const char*` 名称数组；
   - `OnnxSessionContext` 持有 `std::shared_ptr<OnnxModelAsset>` 强引用与 Worker 独占的 `Ort::RunOptions`，確保模型权重生命周期始终长于所有派生会话；
   - 两者均显式 `= delete` 拷贝与移动构造/赋值（Rule of 5）。
3. **边缘并发线程收敛**：`Ort::SessionOptions` 强制设置 `SetIntraOpNumThreads(1)`、`SetInterOpNumThreads(1)` 与 `ORT_ENABLE_ALL`，防止 ORT 内部线程池与宿主多 Worker 发生超额订阅（Oversubscription）。

### 6.3 零拷贝张量桥接与异常防爆门

1. **输入零拷贝包装**：通过 `Ort::Value::CreateTensor(memory_info, const_cast<void*>(in_tensor.raw_data()), in_tensor.byte_size(), shape_ptr, shape_len, onnx_type)` 直接挂载调用方 `zhulong::Tensor` 连续内存，禁止输入侧冗余 `memcpy`。
2. **静态维度强校验与动态维度放行**：`forward` 前置校验输入数量、元素类型、维度阶数（Rank）；对模型中 `expected_dim > 0` 的静态维度严格校验相等，对 `<= 0`（如动态 Batch `-1`）的维度校验实参 `> 0`。
3. **异常全拦截与错误码映射 (`map_onnx_error`)**：`load_model`、`create_session`、`forward` 内部全量捕获 `const Ort::Exception&`、`const std::bad_alloc&`、`const std::exception&` 与 `...`：
   - `ORT_OK` ➔ `ZHULONG_PLUGIN_OK`
   - `ORT_INVALID_ARGUMENT` / `ORT_NO_MODEL` / `ORT_INVALID_PROTOBUF` / `ORT_INVALID_GRAPH` ➔ `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`
   - `ORT_NO_SUCHFILE` ➔ `ZHULONG_PLUGIN_ERR_NOT_FOUND`
   - `ORT_MODEL_LOADED` ➔ `ZHULONG_PLUGIN_ERR_ALREADY_EXISTS`
   - `ORT_NOT_IMPLEMENTED` / `ORT_EP_FAIL` ➔ `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED`
   - `ORT_ENGINE_ERROR` / `ORT_RUNTIME_EXCEPTION` / `ORT_FAIL` 及未知码 ➔ `ZHULONG_PLUGIN_ERR_INTERNAL`
   - `std::bad_alloc` ➔ `ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY`

### 6.4 跨后端算法包模型适配与单测 Mock 委派

1. **多格式输入布局解耦（Dual Layout）**：
   - 算法插件（如 `FaceRecognitionPlugin`）通过检测模型输入张量的形状与数据类型（`[1, 3, H, W]` Float32 vs `[1, H, W, 3]` UInt8），动态自适应采用 NCHW 归一化 Float32 张量（ONNX SCRFD: `(x - 127.5f)/128.0f`；ArcFace/FaceLiVTv2: `(x - 127.5f)/127.5f`）或 NHWC 裸字节张量（RKNN），使上层应用与推理引擎布局约束完全解耦。
   - 特征提取输出 512 维向量在插件内原地执行 L2 归一化（Unit Norm），确保后续 1:N 余弦相似度比对稳定对齐黄金基准。
2. **测试环境 Mock 委派规范**：
   - 在启用 `ZHULONG_INFERENCE_BACKEND=onnx` 构建时，通用 Runner/Broker 单元测试可能加载 `mock.model` 或以 `.weights` 结尾的桩模型。`OnnxInferenceBackend` 对符合 Mock 规约的模型透明委派至 `CpuInferenceBackend`，而对于真实 `.onnx` 模型或非法/损毁文件则执行严格的 ONNX 格式校验与错误码映射，兼顾了通用测试桩复用与 ONNX 严苛错误边界。

---

## 7. Rockchip VEPU JPEG 硬件编码后端契约（抓拍）

*硬件基线：RK3568（Debian 11 arm64 / 内核 5.10.226 / `librockchip-mpp` 1.5.0-1 / `vepu@fdee0000`）。*

### 7.1 三个硬件节点互不干扰（实测）

| 节点 | 用途 | MJPEG ×20 | AVC ×20 |
| --- | --- | --- | --- |
| `fdee0000.vepu` | JPEG 硬编 | **+20 中断** | +0 |
| `fdf40000.rkvenc` | H.264/H.265 硬编 | +0 | **+20 中断** |
| `fded0000.jpegd` | JPEG 解码 | +0 | +0 |

- **结论**：抓拍 JPEG 永远不会与将来的录像硬编抢硬件，**不需要仲裁**。唯一共享资源是 DDR 带宽与 IOMMU。
- ⚠️ 不要把 `/proc/interrupts` 里的 `fdf40000.rkvenc` 当成 JPEG 编码的证据；JPEG 在 `fdee0000.vepu`（设备树节点名是 `vepu@fdee0000`，用 `grep vpu` 搜不到它）。

### 7.2 尺寸与色彩契约（实测，⚠️ 禁止凭直觉加对齐/padding）

| 输入尺寸 | 产物 SOF 尺寸 | 备注 |
| --- | --- | --- |
| 16×16 / 32×32 / 48×48 / 60×60 / 64×64 | 完全一致 | 无最小尺寸门槛（16×16 可用） |
| **66×66** / 100×100 / 200×400 | 完全一致 | 非 16 对齐**无位移、无模糊、无 padding 伪影** |
| 1920×1080 / **1922×1082** | 完全一致 | 非 16 对齐的 1080p 级同样精确 |
| Y=16/128/235/255 | 解码 Y 原样返回 | **无 range 转换**，与软件后端行为一致 |

- ⚠️ **不得为对齐而外扩/内缩 ROI**：VEPU 自己处理内部 stride 对齐，人为改框只会白白改变证据图内容。
- 全幅矩形按 2 像素向内取整即可（色度下采样要求），与 `normalized_box_to_pixels` 的取整规则一致，保证两个后端产物几何相同。

### 7.3 后端选择与分流阈值

运行期开关 `ZHULONG_JPEG_BACKEND=cpu|mpp|auto`（默认 `auto`）。**未知取值 fail closed 到 `cpu`** 并记 ERROR，避免拼写错误静默打开硬件。

实测（真实代码路径，`quality=85`，`CLOCK_PROCESS_CPUTIME_ID`）：

| 尺寸 | 硬编 wall / cpu | 软编 wall / cpu | 胜者 |
| --- | --- | --- | --- |
| 64×64 | 0.92 / 0.69 ms | **0.38 / 0.38 ms** | 软编 2.4× |
| 100×100 | 0.82 / 0.63 ms | **0.58 / 0.58 ms** | 软编 1.4× |
| 128×128 | 0.87 / 0.64 ms | **0.64 / 0.64 ms** | 持平 |
| 160×160 | 0.95 / **0.64 ms** | 0.84 / 0.84 ms | 硬编省 CPU |
| 320×240 | **1.50 / 0.74 ms** | 2.12 / 2.11 ms | 硬编 1.4× |
| 1920×1080 | **21.7 / 2.35 ms** | 53.0 / 52.98 ms | 硬编 **22.5× CPU** |

- **分流规则**：`JpegBackend::prefer_hardware()` 以 `w*h > 16384`（128×128）为界。小图硬编的固定开销（上传拷贝 + ioctl ≈0.5ms）超过小面积 DCT，硬上只会拖慢 wall 并占住共享硬件。
- 人脸脸框量级（≤128×128）落在软件侧；人体框、全幅走硬件。

### 7.4 ⚠️ 强制启动自检（不可跳过）

`MppJpegEncoder::self_test()` 用两张**仅亮度不同**（Y=16 vs 235）、色度同为 128 的 32×32 合成帧断言：

1. 产物 SOI/EOI 完整；
2. SOF 声明尺寸 = 请求尺寸；
3. 两份 JPEG 字节**必须不同**。

第 3 条拦的是「编码成功、产出合法 JPEG、但恒为纯灰图且不报错」——自研骨架在帧挂载序列不完整时真实复现过（`mpi_enc_test` 同库同硬件正常）。自检失败即 WARN 并回退软件，**不得为「先跑起来」跳过**。

### 7.5 会话池与内存所有权

- 会话按 `(width, height)` 分桶，上限 8，满载按 LRU 淘汰；**绝不是每帧 `mpp_init`**（那是几十毫秒级）。
- 板端已验证：24 个不同尺寸连续编码（触发多轮淘汰与 `mpp_destroy`）不崩、不退化。
- 所有权：会话/缓冲由 `MppJpegEncoder` 分配并释放；`MppFrame`/`MppPacket` 每次编码配平释放；JPEG 字节**拷贝**给调用方，packet 缓冲不外泄。
- ⚠️ **编码符号表与解码节点独立**：不得并入 `nodes/decode/mpp_loader`，否则一个缺失的 enc 符号会把硬件解码一并判为不支持。

### 7.6 DMA-BUF 直通（已启用，**仅整帧**）

`MppJpegEncoder::encode_dma_frame()`：把解码器产出的 fd 直接 `mpp_buffer_import(MPP_BUFFER_TYPE_EXT_DMA)`
成 `MppBuffer` 交给 VEPU，**零 CPU 拷贝**。实测（1080p，`quality=85`）：

| 路径 | wall | CPU | 备注 |
| --- | --- | --- | --- |
| 软编 (FFmpeg mjpeg) | 53.0 ms | 52.98 ms | 基线 |
| 硬编 + CPU 上传 | 21.7 ms | 2.31 ms | §7.3 |
| **硬编 + DMA-BUF 直通** | **20.3 ms** | **0.91 ms** | 相对软编 **58× CPU** |

- ⚠️ **wall 的地板是 VEPU 本身**（1080p 约 20ms）。继续压 CPU 不会让单张变快，只降低对 A55 的占用与抢占。
- ⚠️ **裁剪框不走直通**：MPP 编码器没有一致可靠的「源偏移」语义（UV 平面基址是否跟随 offset 未验证）。要做需 RGA crop → `mpp_buffer`，而裁剪图多是 ≤128×128（本来就走软编），收益与复杂度不成比例。
- ⚠️ **stride 必须原样取自帧**（`frame.stride` / `frame.vstride`），**绝不可**用 `align16(width)` 顶替 —— 解码器可能给 2048 这类行跨度，顶替会让 VEPU 按错行宽读取、产出斜切图。会话池的 key 因此升级为 `(w, h, hor_stride, ver_stride)` 四元组。
- ⚠️ **缓存一致性**：直通路径的写者也是硬件（解码器），CPU 侧无脏行，**不需要** `DMA_BUF_IOCTL_SYNC`。一旦引入「CPU 写完再硬编」（OSD、水印、马赛克），必须显式 end-write 同步，否则表现为**静默灰图**而不是报错。
- **运行期自证（不可省略）**：首次直通会对**同一帧**分别走直通与上传路径并逐字节比对 —— 相等才启用，不等即 `ERROR` 并**永久停用**。这条把「dma_heap 测试缓冲」的验证升级成「真实解码器帧的 fd 语义」验证，而后者只能在真机上完成。另外连续失败 3 次也永久停用，防止「每帧先付 import 开销再回退」的隐性退化。

## 8. 验证命令

```bash
# 1. 检查头文件依赖隔离（仅允许在 native/src/backends/{rknn,onnx}/ 引用）
grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/
grep -rn 'onnxruntime' native/src/ native/sdk/ native/include/

# 2. 检查宿主与动态库依赖（宿主绝对不链接厂商 SDK，RPATH 为空）
readelf -d build/native/cross-rk3568-rknn-rga/libZhulongAlgoRuntime.so | grep -E 'NEEDED|RUNPATH'
readelf -d build/targets/rk3568-rknn-rga/Zhulong | grep NEEDED

# 3. 运行 ONNX Runtime 后端单元测试与 ASan/UBSan 内存审计
python3 native/scripts/build.py configure --inference-backend onnx
cmake --build build/native/host --parallel 8 --target ZhulongOnnxTests
ctest --test-dir build/native/host -R ZhulongOnnx -V

python3 native/scripts/build.py configure --inference-backend onnx --sanitizer address
cmake --build build/native/host-address --parallel 8 --target ZhulongOnnxTests ZhulongFaceRecognitionOnnxTests
ctest --test-dir build/native/host-address -R "ZhulongOnnx|ZhulongFaceRecognitionOnnx" -V

# 4. 运行本地默认 CPU 与 DMA-BUF / IPC SCM_RIGHTS 全量回归单元测试
python3 native/scripts/build.py test

# 4.1 ⚠️ Go 测试缓存不感知运行期 dlopen 的算法插件
#     `libFaceRecognitionPlugin.so` 既不在 Go 构建输入里，也不进 CGO_LDFLAGS 的内容哈希，
#     因此切换推理后端（cpu ↔ onnx）后 `go test` 会直接复用上一次的缓存结果（含 SKIP 结论）。
#     验证真实推理链路时必须显式 -count=1：
python3 native/scripts/build.py --inference-backend onnx go test -race -count=1 ./internal/engine

# 5. 交叉构建并在 RK3568 板端验证硬件解码与全链路零拷贝
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn-rga.json build
scp build/native/cross-rk3568-rknn-rga/ZhulongDecoderTests rk3568:/tmp/
ssh rk3568 "/tmp/ZhulongDecoderTests"

# 6. JPEG 硬编（VEPU）板端验证：自检 + 尺寸矩阵 + 会话池淘汰 + 后端分流
python3 native/scripts/build.py --profile native/profiles/rk3568.json build
scp build/native/cross-rk3568/ZhulongJpegEncoderTests rk3568:/tmp/
ssh rk3568 "/tmp/ZhulongJpegEncoderTests"                      # 无环境变量 = auto，应报 mpp
ssh rk3568 "ZHULONG_JPEG_BACKEND=cpu /tmp/ZhulongJpegEncoderTests"  # 强制软编对照组
```

