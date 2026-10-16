# 算法前处理 RGA 硬件加速与 FrameBroker 零拷贝穿透技术设计 (design.md)

## 1. 架构总览与核心切面

本设计旨在打通从 `FrameBroker` 到算法插件内部的硬件级零拷贝闭环，涵盖两个核心层级的重构：
1. **分发层 (`FrameBroker`)**：增加对硬件帧（`dma_fd >= 0`）的零拷贝穿透分支，彻底剔除 CPU `std::memcpy`；
2. **算法层 (`FaceDetector` / `packages/face_recognition`)**：废弃手写双重 CPU 循环，接入统一视觉前处理算子 `zhulong::ops::letterbox`，利用 RGA 2D 硬件直接从源 DMA-BUF 输入生成目标张量 DMA-BUF，并修正后处理反算几何。

```
[上游 HardwareFrame] (带有效 dma_fd >= 0)
        │
        ▼
[FrameBroker::publish_frame]
        │
        ├── if (frame.dma_fd >= 0) ──> [零拷贝穿透路径]: 规避 memcpy, 直接 SCM_RIGHTS 投递 dma_fd
        └── else (软解保底) ─────────> [常规拷贝路径]: memcpy 进 slot_data
        │
        ▼ (UDS SCM_RIGHTS)
[Runner 进程 (zhulong_frame_view)]
        │
        ▼
[FaceDetector::detect]
        │
        ├── 1. 获取/分配预设 DMA-BUF 输入张量 (input_tensor 绑定 dma_input_buf_)
        ├── 2. 调用 zhulong::ops::letterbox(image, input_tensor, letterbox_params)
        │      └── 硬件模式: RgaVisionBackend::letterbox (wrapbuffer_fd ➔ improcess ➔ 114 补边)
        │      └── 降级模式: CpuVisionBackend::letterbox (SIMD/标量保底)
        ├── 3. RKNN NPU 前向推理: session.forward({input_tensor})
        │      └── rknn_create_mem_from_fd + rknn_set_io_mem 硬件直绑
        └── 4. decode_scrfd 几何反算 (扣除 pad_left / pad_top，除以 scale)
```

---

## 2. 详细技术实现方案

### 2.1 算法前处理重构 (`FaceDetector::detect`)

* 废弃 `packages/face_recognition/src/detector.cpp` 中的手写逐像素循环：
  ```cpp
  // ❌ 淘汰的手写 CPU 循环
  for (int dy = 0; dy < unpad_h; ++dy) {
      for (int dx = 0; dx < unpad_w; ++dx) { ... }
  }
  ```
* 替换为基于 `zhulong::ops::letterbox` 的标准算子流：
  ```cpp
  zhulong::LetterboxParams params;
  params.target_width = model_w;
  params.target_height = model_h;
  params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

  zhulong_status_t status = zhulong::ops::letterbox(image, input_tensor, params);
  if (status != ZHULONG_PLUGIN_OK) {
      // 容错回退
      return detect_cpu_fallback(image);
  }
  ```
* **DMA-BUF 缓存复用保护**：
  若运行环境支持硬件 DMA-BUF 且 `image.dma_buf_fd() >= 0`，持续复用 `dma_input_buf_`。`ops::letterbox` 在 RGA 后端检测到 `input_tensor.dma_buf_fd() >= 0` 时，自动采用 `wrapbuffer_fd` 构造 `dst_buffer`，RGA 芯片直接对显存发起 DMA 搬运。

### 2.2 居中 Letterbox 几何反算统一 (`decode_scrfd`)

* `letterbox_geometry.cpp` 规范定义了居中补边几何：
  $$\text{pad\_left} = \frac{\text{target\_w} - \text{unpad\_w}}{2}, \quad \text{pad\_top} = \frac{\text{target\_h} - \text{unpad\_h}}{2}$$
* `decode_scrfd` 增加 `pad_left` 与 `pad_top` 入参，后处理还原原图坐标时严格扣除补边量：
  $$px_1 = \text{clamp}\left(\frac{cx - \text{bbox}_0 \times \text{stride} - \text{pad\_left}}{\text{scale}}, 0, \text{orig\_w}\right)$$
  $$py_1 = \text{clamp}\left(\frac{cy - \text{bbox}_1 \times \text{stride} - \text{pad\_top}}{\text{scale}}, 0, \text{orig\_h}\right)$$
  5 点关键点同理统一调整。
* **双向兼容**：若调用方为靠左上对齐（`pad_left = 0, pad_top = 0`），该公式自动退化为原有逻辑，数学上完全兼容。

### 2.3 FrameBroker 零拷贝直通设计

* 在 `native/src/algorithm/ipc/frame_broker.cpp` 的 `publish_frame` 中：
  ```cpp
  const bool is_zero_copy_frame = (frame.dma_fd >= 0);
  int dma_fd_to_send = -1;
  uint8_t* slot_data = nullptr;

  if (is_zero_copy_frame) {
      // 硬件帧直通路径：不碰像素，杜绝任何 memcpy
      dma_fd_to_send = frame.dma_fd;
  } else if (has_dma_slots_) {
      // 软解回退：拷入预分配的 DMA-BUF 槽位
      slot_data = static_cast<uint8_t*>(dma_slots_[slot_idx]->mmap_addr());
      dma_fd_to_send = dma_slots_[slot_idx]->fd();
      dma_slots_[slot_idx]->sync_start(zhulong::platform::DMA_SYNC_WRITE);
      // 执行原有的安全分平面 memcpy...
  }
  ```
* 避免了当输入帧本身已经是外部 DMA-BUF 时（如后续 MPP 硬解输出），还被冗余拷入内部 `dma_slots_` 的严重性能缺陷。

---

## 3. 错误边界与降级策略

1. **RGA 硬件不可用或入参越界**：
   `ops::letterbox` 内部自动调用 `CpuVisionBackend`，若依然失败则平滑返回 `detect_cpu_fallback`，保证业务路径永不 panic。
2. **跨平台兼容**：
   在 x86_64 macOS/Linux 开发机上，`ZHULONG_VISION_BACKEND` 为 `cpu`，`ops::letterbox` 无缝由 CPU 浮点/双线性算法执行，单测保持 100% 通过。
