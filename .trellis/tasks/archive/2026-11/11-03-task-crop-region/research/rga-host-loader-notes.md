# 研究摘要：宿主侧 RGA 窗口拷贝（裁剪任务）

> 本文件是 `.trellis/spec/native/hardware-backends.md`（46KB）与
> `.trellis/spec/native/algo-plugin-contract.md`（63KB）中与 RGA 窗口物化相关条款的任务级摘要。
> 完整规范仍以原文件为准；本摘要用于避免上下文注入被 32KB 截断。

## 1. 归属与构建

- 厂商头文件 `rga/im2d.h` 等只允许出现在 `native/src/backends/rga/` 内的 TU。
  宿主侧新增 `rga_host_loader.cpp` 也必须放在该目录。
- 宿主 `ZhulongEngine` 需要 `vendor/rga/include` 作为 **PRIVATE** include；
  **只 dlopen `librga.so.2`，绝不链接 `librga.so`**，否则 `Zhulong` 会多出 NEEDED，
  破坏单产物/平台无关交付。
- 算法运行库的 RGA 后端（`ZHULONG_VISION_BACKEND=rga`）与宿主窗口 loader 是两条独立路径：
  不要复用 `ZhulongAlgoRuntimeObjects`，也不要把 `window_crop.cpp`/`rga_host_loader.cpp`
  放进 `ZhulongAlgoIpcObjects`（那批对象会归档进 `libzhulong_algo_sdk.a`）。

## 2. 窗口拷贝规则

- 源 `HardwareFrame` 可能是：
  - MPP dmabuf：`dma_fd >= 0`，`host_planes` 为空；
  - FFmpeg 软解：`host_planes[0..2]` 或 `host_ptr`；
  - 打包格式：`host_ptr`，`stride` 含 padding。
- RGA 同格式窗口拷贝用 `srect{view.x,view.y,view.w,view.h}` → `drect{0,0,w,h}`；
  **目标指针绝不偏移**，内容区偏移只能由 `drect` 表达（`rga_vision_backend.cpp` R12 教训）。
- 每次调用前先 `imcheck_t` 预检；预检失败/库缺失/`/dev/rga` 不可访问 → 该帧走 CPU，
  几何不变。
- 运行期 `improcess` 失败（预检通过但驱动拒绝/部分写）时，不得把半截 slot 广播出去；
  该帧应 `fail_publish`（或仅在确认未写入时安全回退 CPU）。
- `librga.so.2` 与 vendored header（1.10.1）的 ABI/版本必须板端验证；`imcheckHeader` 失败即 CPU。
- 多路 `FramePublisher` 并发 `improcess` 的线程安全/硬件串行未证明；spike 通过前
  串行化窗口拷贝或每 stream 独立 context。

## 3. 紧打包与缓存一致性

- 窗口 slot 必须紧打包：
  - BGR24/RGB24：`dst_stride = align_up(w*3, 192)`；
  - NV12：`dst_stride = align_up(w, 64)`，`offsets[1] = dst_stride*h`；
  - YUV420P：`dst_stride = align_up(w, 64)`，U/V 平面按 `dst_stride/2`。
- 这组 stride 必须同时满足客户端 `resolve_source` 的：
  `y_stride % plane0_bpp == 0`、`y_stride % stride_align == 0`、
  `deduce_continuous_hstride` 推出的 `hstride == h`。
- RGA 写 **shm**：没有 `DMA_BUF_IOCTL_SYNC` 可用，必须 spike 读回一致性；不行就 CPU。
- RGA 写 **dma slot**：客户端读 fd 已有 `DMA_BUF_IOCTL_SYNC(START|READ)`，
  但仍要验证驱动写入后的可见性。
- CPU 写 dma slot：用 `dmabuf_sync.hpp` 里的**裸 fd RAII**（`ScopedFdSync(fd, DMA_SYNC_WRITE)`）
  覆盖整个写入窗口；⚠️ `ScopedCpuSync` 只接受 `DmaBuffer&`，不能拿裸 fd 构造。
- CPU 读 dmabuf：`mmap` + `DMA_BUF_IOCTL_SYNC(START/END)` 成对；在
  `native/src/platform/dmabuf/dmabuf_sync.hpp` 抽成同一个 `ScopedFdSync`，与 motion detector 共用。

## 4. CPU 行拷贝

- NV12/YUV420P 的 UV 平面起点必须用 `HardwareFrame::y_plane_size()`：
  `stride * (vstride > 0 ? vstride : height)`，**不是** `stride * height`。
  否则 `vstride > height` 时色度错位/越界。
- 源行跨度优先 `plane_strides[i]`，为 0 才回退 `stride`；YUV420P 的 U/V 跨度通常是
  `stride/2`，但软解可能给独立 plane stride。
- dmabuf 源 mmap 大小要覆盖 Y + UV（`y_plane_size + stride*((height+1)/2)` 或对应格式）。
- 目标窗口的 `h` 必为偶数（窗口计算保证），但代码仍应防御奇数输入。

## 5. 可观测性与降级

- 计数器：`window_rga_count` / `window_cpu_count`，日志按流限频；板端必须能一眼看出
  “一直在走 CPU”。
- 任何降级都不得改变几何；RGA 与 CPU 必须写同一个 `ViewWindow`、同一个 packed 布局。
- 不引入缩放：算法包自己的 letterbox 负责缩放；宿主只做窗口裁剪。
