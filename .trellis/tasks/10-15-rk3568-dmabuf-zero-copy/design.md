# 架构与技术设计：RK3568 全链路 DMA-BUF 硬件零拷贝 (10-15-rk3568-dmabuf-zero-copy)

## 1. 架构总览与拓扑设计

本设计在 Zhulong 的独立进程算法架构（`One Package, One Process`）中，引入 Linux 底层 **DMA-BUF 物理连续内存** 与 **UNIX Domain Socket 文件描述符传递（`SCM_RIGHTS`）**，实现从生产端到消费端、前处理硬件及推理加速器的端到端硬件零拷贝数据通路。

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│ 宿主主进程 (Native Engine / FrameBroker)                                    │
│                                                                             │
│  [DmaBufAllocator] ──从 /dev/dma_heap/cma 分配物理连续 DMA-BUF 槽位池          │
│          │                                                                  │
│          ▼                                                                  │
│  [FramePublisher] ──写入解码帧 (软解帧通过 DMA_BUF_SYNC 刷入物理显存)        │
│          │                                                                  │
│          ▼                                                                  │
│  [UNIX Domain Socket] ──sendmsg() 携带 SCM_RIGHTS 将 dma_buf_fd 跨进程传递   │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │ Unix Domain Socket
                                       │ 二进制 IPC 控制包 + SCM_RIGHTS 文件描述符
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 算法 Runner 独立子进程 (zhulong_algo_runner)                                 │
│                                                                             │
│  [recvmsg(SCM_RIGHTS)] ──接收 dma_buf_fd 并置入 zhulong_frame_view (带 CLOEXEC)│
│          │                                                                  │
│          ▼                                                                  │
│  [算法插件: FaceRecognitionPlugin]                                          │
│    - 原帧图像包装: zhulong::Image (持有 dma_buf_fd)                          │
│    - 模型输入张量: Tensor (可绑定输出端的 DMA-BUF fd)                        │
│          │                                                                  │
│          ├───────────────────────────────┬──────────────────────────────────┤
│          ▼                               ▼                                  │
│  [RGA 视觉前处理后端]           [RKNN 神经网络推理后端]                     │
│    - wrapbuffer_fd(src_fd)        - rknn_create_mem_from_fd(dst_fd)         │
│    - wrapbuffer_fd(dst_fd)        - rknn_set_io_mem(ctx, mem)               │
│    - 硬件执行 CSC / 缩放          - NPU 直接读取显存，零内核搬移            │
│          │                               │                                  │
│          └───────────────────────────────┴──────────────────────────────────┤
│                                          ▼                                  │
│  [归还租约] ──close(dma_buf_fd) + 发送 MSG_FRAME_ACK(generation) 归还槽位    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 核心模块与职责划分

| 模块 | 核心文件 | 核心职责 |
| --- | --- | --- |
| **DMA 内存分配器** | `native/src/platform/dmabuf/dmabuf_allocator.hpp`, `.cpp` | 探测与管理 `/dev/dma_heap` / DRM Dumb Buffer，提供带 RAII 和 Cache 同步的物理内存管理 |
| **IPC 协议扩展** | `native/src/algorithm/ipc/ipc_protocol.hpp`, `ipc_socket.hpp` | 增强 `IpcSocket` 支持 `send_msg_with_fd` / `recv_msg_with_fd`（基于 `SCM_RIGHTS`） |
| **Broker 槽位升级** | `native/src/algorithm/ipc/frame_broker.hpp`, `.cpp` | 维护带 DMA-BUF 的槽位池；分发时传递槽位 fd；ACK 确认及异常回收 |
| **C ABI & 视图增强** | `native/sdk/include/zhulong/plugin.h`, `algo.hpp` | `zhulong_frame_view` 增加 `int32_t dma_buf_fd`；`zhulong::Image` 暴露 fd 查询 |
| **张量容器升级** | `native/src/algorithm/runtime/tensor.hpp`, `.cpp` | `Tensor` 支持外部 DMA-BUF 绑定（带虚地址映射与 fd 句柄） |
| **RGA 零拷贝算子** | `native/src/backends/rga/rga_vision_backend.cpp` | 全面适配 `wrapbuffer_fd` / `importbuffer_fd`，实现全硬件加速管线 |
| **RKNN 零拷贝绑定** | `native/src/backends/rknn/rknn_inference_backend.cpp` | 使用 `rknn_create_mem_from_fd` 与 `rknn_set_io_mem` 替代 `rknn_inputs_set` |

---

## 3. 详细子系统设计

### 3.1 跨进程 FD 传输（IPC SCM_RIGHTS）

UNIX Domain Socket 允许在相同 Linux 实例的进程间安全传递文件描述符副本：
1. **发送端 (`FrameBroker`)**：
   ```cpp
   struct msghdr msg = {};
   struct iovec iov = { payload_buf, payload_len };
   msg.msg_iov = &iov;
   msg.msg_iovlen = 1;

   char cmsg_buf[CMSG_SPACE(sizeof(int))];
   if (dmabuf_fd >= 0) {
       msg.msg_control = cmsg_buf;
       msg.msg_controllen = sizeof(cmsg_buf);
       struct cmsghdr* cmsg = CMSG_FIRSTHDR(&msg);
       cmsg->cmsg_level = SOL_SOCKET;
       cmsg->cmsg_type = SCM_RIGHTS;
       cmsg->cmsg_len = CMSG_LEN(sizeof(int));
       *((int*)CMSG_DATA(cmsg)) = dmabuf_fd;
   }
   sendmsg(sock_fd, &msg, MSG_NOSIGNAL);
   ```
2. **接收端 (`Runner`)**：
   - 提取 `SCM_RIGHTS` 描述符，并立即设置 `FD_CLOEXEC` 防范进程派生泄漏；
   - 将接收到的 `fd` 挂载至 `zhulong_frame_view.dma_buf_fd`；
   - 帧推理完成后，Runner 必须显式执行 `::close(dma_buf_fd)` 释放本进程内的文件句柄副本，并回传 `MSG_FRAME_ACK`。

### 3.2 物理内存分配与 CPU 缓存一致性（`DmaBufAllocator`）

在 RK3568 上，针对多媒体视频管线，Linux 内核挂载了 CMA 堆 `/dev/dma_heap/cma` 与散布堆 `/dev/dma_heap/system`：
1. **分配策略**：
   - 首选 `/dev/dma_heap/cma`（连续物理内存，最高 DMA 性能）；
   - 次选 `/dev/dma_heap/system`（配合 IOMMU）；
   - 次选 DRM Dumb Buffer (`/dev/dri/renderD128` 或 `card0`)；
   - 桩环境：回退普通 POSIX 共享内存。
2. **CPU 读写 Cache 保护契约**：
   CPU 写入物理内存必须成对包裹 `DMA_BUF_IOCTL_SYNC`：
   ```cpp
   struct dma_buf_sync sync = { DMA_BUF_SYNC_START | DMA_BUF_SYNC_WRITE };
   ioctl(fd, DMA_BUF_IOCTL_SYNC, &sync);
   // CPU 写入像素数据...
   sync.flags = DMA_BUF_SYNC_END | DMA_BUF_SYNC_WRITE;
   ioctl(fd, DMA_BUF_IOCTL_SYNC, &sync);
   ```
   自动处理 `-EINTR` 与 `-EAGAIN` 中断重启。

### 3.3 视觉前处理硬件直通 (`RgaVisionBackend`)

当图像与张量具备 DMA-BUF 时，RGA 采用全硬件导入：
```cpp
rga_buffer_t src = wrapbuffer_fd(src_fd, w, h, src_fmt, stride);
rga_buffer_t dst = wrapbuffer_fd(dst_fd, dst_w, dst_h, dst_fmt, dst_stride);
// 设置 RGA 核心亲和性与裁剪缩放
im_opt_t opt = {};
imset_core_mask(RGA_CORE_MASK_ALL);
imcrop_and_resize(src, dst, crop_rect, opt);
```
完全在硬件层面完成 CSC（NV12 ➔ RGB）与双线性缩放，输出直写目标 Tensor 的 DMA-BUF 显存。

### 3.4 NPU 推理零拷贝绑定 (`RknnInferenceBackend`)

当输入张量具备 `dma_buf_fd` 时：
```cpp
rknn_tensor_mem* mem = rknn_create_mem_from_fd(
    ctx, tensor.dma_buf_fd(), tensor.raw_data(), tensor.byte_size(), 0
);
rknn_set_io_mem(ctx, mem, &attr);
rknn_run(ctx, nullptr);
rknn_destroy_mem(ctx, mem);
```
NPU 通过 IOMMU 直接从同一物理内存块取数，**无任何内核态与用户态 `memcpy`**！

---

## 4. 兼容性与优雅降级方案 (Host Fallback)

| 环境 | DMA 分配机制 | 进程间传输 | RGA 行为 | RKNN 行为 |
| --- | --- | --- | --- | --- |
| **RK3568 板端** | `/dev/dma_heap/cma` | SCM_RIGHTS 传 fd | `wrapbuffer_fd` 硬件加速 | `rknn_create_mem_from_fd` 零拷贝 |
| **Host 开发机 / CI** | 回退 POSIX SHM (`fd=-1`) | 常规 UDS 仅传槽位信息 | 自动降级 CPU 软件缩放 | 自动降级 CPU Mock 前向 |

保证开发机 `make check`、单元测试及全平台构建 100% 绿色无障碍。
