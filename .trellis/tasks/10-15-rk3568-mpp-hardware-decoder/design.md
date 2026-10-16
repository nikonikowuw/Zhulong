# Rockchip MPP 硬件解码节点与端到端零拷贝贯通技术设计 (design.md)

## 1. 架构总览与核心分层

本设计旨在为 Zhulong 原生流媒体管线引入高性能、低功耗的硬件解码节点 `MppDecodeNode`，替代单流 CPU 软解。配合 Child 1 已经交付的 `FrameBroker` 零拷贝穿透分支，打通从 RTSP 网络拉流到 NPU 深度学习推理的端到端真正零拷贝闭环。

```
[RTSP 接收/有界包队列]
        │
        ▼ (H.264 / H.265 NALU)
[MppDecodeNode] ── (继承 IDecodeNode 统一两段式契约)
        │
        ├── 1. send_packet(data, size, pts): 零拷贝包借用 ➔ mpi->decode_put_packet
        │      └── 0 字节包作为 EOS / Drain 信号处理
        ├── 2. receive_frame(out_frame): 非阻塞轮询 ➔ mpi->decode_get_frame
        │      ├── if (info_change) ──> 动态捕获几何跨度, 配置 BufferGroup, 发送 READY
        │      └── 正常帧 ────────────> 提取 MppBuffer, 导出 dma_fd >= 0
        └── 3. 组装 HardwareFrame:
               ├── format = FRAME_FORMAT_NV12
               ├── stride = hor_stride (硬件行跨度), vstride = ver_stride (虚高跨度)
               ├── dma_fd = mpp_buffer_get_fd(buf)
               └── release_fn = [buf] { mpp_buffer_put(buf); } ── (RAII 自动归还显存)
        │
        ▼ (HardwareFrame::Ptr)
[FrameBroker::publish_frame] ── (触发 Child 1 零拷贝穿透，零 memcpy 跨进程传递)
        │
        ▼ (UDS SCM_RIGHTS 传输 dma_fd)
[Runner 进程 (RGA 视觉前处理 ➔ RKNN NPU 推理直绑)]
```

---

## 2. 详细技术实现方案

### 2.1 动态加载与平台隔离 (Portable Edge Binary via `dlopen`)

为杜绝因强链接 `libmpp.so` 导致非 RK 平台（macOS、x86_64 CI/开发机）在启动时动态链接器报错崩溃（`shared library not found`），采用基于 `dlopen` 的动态装载隔离机制：
1. **统一契约头文件**：在 `native/src/nodes/decode/mpp_api_types.hpp` 中声明 Rockchip MPP 核心数据结构（`MppCtx`, `MppApi`, `MppFrame`, `MppPacket`, `MppBuffer` 等）与常量命令字，无需安装宿主机 SDK 即可在全平台干净编译；
2. **运行时探针与符号加载**：
   - 优先检查硬件设备节点 `/dev/mpp_service` 是否存在；
   - 动态加载 `dlopen("libmpp.so.1", RTLD_NOW)` 或 `dlopen("libmpp.so", RTLD_NOW)`；
   - 获取 `mpp_create`、`mpp_init`、`mpp_destroy`、`mpp_packet_*`、`mpp_frame_*`、`mpp_buffer_*` 函数指针；
3. **工厂方法无缝回退**：
   在 `IDecodeNode::create` 中：
   ```cpp
   if (backend == DecoderBackend::AUTO || backend == DecoderBackend::MPP_HW) {
       if (MppDecodeNode::is_supported()) {
           try {
               return std::make_unique<MppDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den);
           } catch (...) {
               if (backend == DecoderBackend::MPP_HW) throw;
           }
       }
   }
   // 优雅降级回退至通用静态 FFmpeg 软解
   return std::make_unique<FFmpegDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den);
   ```

### 2.2 两段式异步状态机契约 (`IDecodeNode`)

* **`send_packet` 实现**：
  - 排空信号处理：当 `data == nullptr || size == 0` 时，调用 `mpp_packet_set_eos` 标记尾包并送入，通知底层 VPU 冲刷所有驻留参考帧；
  - 零拷贝借用：通过 `mpp_packet_init(&packet, (void*)data, size)` 包装输入指针，设置 `pts`，调用 `mpi->decode_put_packet(ctx, packet)`；调用完成后立即 `mpp_packet_deinit(&packet)`，不持久持有调用方内存；
  - 背压感知：若底层返回 `MPP_ERR_BUFFER_FULL` 或超时，返回相应重试码。
* **`receive_frame` 与 `info_change` 事件流转**：
  - 调用 `mpi->decode_get_frame(ctx, &frame)`，若返回空帧则返回 `1`（`EAGAIN` 语义）；
  - 若 `mpp_frame_get_info_change(frame)` 为真：
    - 读取物理几何：`width = mpp_frame_get_width`, `height = mpp_frame_get_height`；
    - 读取对齐步长：`hor_stride = mpp_frame_get_hor_stride`，`ver_stride = mpp_frame_get_ver_stride`；
    - 配置内部 DRM 缓冲池：`mpp_buffer_group_get_internal(&grp, MPP_BUFFER_TYPE_DRM)` 并绑定 `MPP_DEC_SET_EXT_BUF_GROUP`；
    - 发送就绪信号：`mpi->control(ctx, MPP_DEC_SET_INFO_CHANGE_READY, nullptr)`；
    - 立即归还该通知帧 `mpp_frame_deinit(&frame)`，并继续拉取后续解码图像帧。

### 2.3 内存生命周期管理与防死锁租约闭包 (Buffer Leasing)

* ⚠️ **核心死锁陷阱**：MPP 的 `MppBufferGroup` 容量通常只有 16~24 帧。若下游节点长期持有 `MppFrame` 不释放，会导致解码缓冲池耗尽，解码器报 `MPP_ERR_TIMEOUT` 并死锁挂起！
* **解决方案**：
  1. 解码出有效帧后，通过 `mpp_frame_get_buffer(frame)` 提取底层显存块 `MppBuffer`；
  2. 调用 `mpp_buffer_inc_ref(buf)` 递增其显存引用计数；
  3. 通过 `mpp_buffer_get_fd(buf)` 提取内核 DMA-BUF 文件描述符回填至 `HardwareFrame::dma_fd`；
  4. 绑定专属回收器：
     ```cpp
     out_frame->release_fn = [buf](HardwareFrame*) {
         mpp_buffer_put(buf); // 线程安全递减计数，归还给 MPP 缓冲池
     };
     ```
  5. 立即对当前外层元数据帧执行 `mpp_frame_deinit(&frame)`，外层帧容器归还，仅保留底层连续物理页的引用租约。

### 2.4 硬件对齐与格式契约 (Stride & Vstride)

* **像素格式**：固定声明为 `FRAME_FORMAT_NV12`；
* **几何步长映射**：
  - `stride = hor_stride`（RK3568 硬件解码输出通常为 16 或 64 字节对齐）；
  - `vstride = ver_stride`（通常为 16 行对齐，例如 1080P 对应 1088）；
  - `y_plane_size()` 自动按 `stride * vstride` 计算，精确对齐 UV 平面基地址偏移，杜绝画面出现绿条或色彩错位。

---

## 3. 错误边界与降级矩阵

| 场景 | 处理策略 | 预期行为 |
| :--- | :--- | :--- |
| x86_64 / macOS 开发环境 | `MppDecodeNode::is_supported()` 返回 false | 自动优雅降级为 `FFmpegDecodeNode` 软解，单测全绿 |
| RK3568 `/dev/mpp_service` 权限不足或缺失 | 探针失败，自动捕获异常 | 自动降级回退至静态 FFmpeg 软解，系统不崩溃 |
| 码流含不支持的色彩剖面（如 10-bit YUV422） | MPP 返回初始化/解码失败 | 返回不可恢复错误或由调用方重试回退 |
| 丢包严重导致解码花屏或参考帧损坏 | 调用 `flush()` 清空 DPB 缓冲 | 清理参考帧并等待下一个完整 IDR 关键帧重新同步 |
