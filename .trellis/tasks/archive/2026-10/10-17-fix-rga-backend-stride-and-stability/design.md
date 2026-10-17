# 设计方案：修复 RGA 视觉后端的垂直跨度与硬件探测缺陷

## 1. 架构与边界说明

本修复集中在 `native/src/backends/rga/` 模块，涉及：
- `rga_vision_backend.cpp`：数据源解析 `resolve_source`、能力探测 `probe`、推理预处理 `try_rga_letterbox`、缩放 `try_rga_resize`、裁剪 `try_rga_crop_scale`；
- `rga_vision_backend.hpp`：接口与注释同步；
- `native/tests/rga_vision_test.cpp`：板端与宿主端单元测试补充。

所有改动严格限制在原生硬件后端内部，不修改公开 C ABI（`IVisionBackend` 与 `zhulong_frame_view`），不向外溢出厂商头文件。

---

## 2. 详细技术方案

### 2.1 NV12 / YUV420P 多平面垂直步长动态推导（解决 1080P 垂直对齐帧误拒与色彩错位）

#### 问题剖析
硬件解码器（Rockchip MPP / V4L2）产出的 NV12 格式图像通常带有 **16 行垂直对齐**（例如 1080P 视频：`width=1920, height=1080, hor_stride=1920, ver_stride=1088`）。
此时 UV 平面起点 `p1` 位于 `p0 + 1920 * 1088`，导致原有的 `p1 == p0 + y_stride * height` 连续性断言失败，且硬编码 `hstride = src.height` 传给 RGA 导致内部 UV 寻址错位。

#### 方案设计
在 `resolve_source` 中，针对多平面连续性校验，利用 `(p1 - p0)` 动态计算垂直步长：
```cpp
int hstride = src.height;

if (spec.plane_count == 2) {
    if (!src.planes[1]) {
        return res;
    }
    const uintptr_t p0 = reinterpret_cast<uintptr_t>(src.planes[0]);
    const uintptr_t p1 = reinterpret_cast<uintptr_t>(src.planes[1]);
    if (p1 <= p0) {
        return res;
    }
    const size_t y_plane_bytes = p1 - p0;
    if (y_plane_bytes % static_cast<size_t>(y_stride) != 0) {
        return res; // 非整行跨度，无法以单一 hstride 描述
    }
    const int detected_hstride = static_cast<int>(y_plane_bytes / y_stride);
    if (detected_hstride < src.height) {
        return res; // UV 平面覆盖了有效 Y 数据区，内存非法
    }
    hstride = detected_hstride;
    if (src.strides[1] > 0 && src.strides[1] != y_stride) {
        return res;
    }
} else if (spec.plane_count == 3) {
    if (!src.planes[1] || !src.planes[2] || src.width % 2 != 0 || src.height % 2 != 0 || y_stride % 2 != 0) {
        return res;
    }
    const uintptr_t p0 = reinterpret_cast<uintptr_t>(src.planes[0]);
    const uintptr_t p1 = reinterpret_cast<uintptr_t>(src.planes[1]);
    const uintptr_t p2 = reinterpret_cast<uintptr_t>(src.planes[2]);
    if (p1 <= p0 || p2 <= p1) {
        return res;
    }
    const size_t y_plane_bytes = p1 - p0;
    if (y_plane_bytes % static_cast<size_t>(y_stride) != 0) {
        return res;
    }
    const int detected_hstride = static_cast<int>(y_plane_bytes / y_stride);
    if (detected_hstride < src.height || detected_hstride % 2 != 0) {
        return res;
    }
    const size_t uv_w = static_cast<size_t>(y_stride) / 2;
    const size_t uv_h = static_cast<size_t>(detected_hstride) / 2;
    const size_t expected_v_offset = uv_w * uv_h;
    if (p2 != p1 + expected_v_offset) {
        return res;
    }
    hstride = detected_hstride;
}
```
随后将推导出的真实 `hstride` 传入 `wrapbuffer_fd` / `wrapbuffer_virtualaddr`：
```cpp
if (src.dma_buf_fd >= 0) {
    res.buffer = wrapbuffer_fd(src.dma_buf_fd, src.width, src.height, spec.rga_format, wstride_px, hstride);
} else {
    res.buffer = wrapbuffer_virtualaddr(const_cast<uint8_t*>(src.planes[0]), src.width, src.height, spec.rga_format, wstride_px, hstride);
}
```

---

### 2.2 Letterbox 内存原地复用（消除热路径堆抖动）

在 `try_rga_letterbox` 中，精细化区分 DMA-BUF 复用、CPU Tensor 复用与新分配：
```cpp
const size_t required_bytes = static_cast<size_t>(target_h) * target_w * 3;
const std::vector<int64_t> target_shape{target_h, target_w, 3};

const bool dma_reusable = (dst.dma_buf_fd() >= 0 && dst.byte_size() >= required_bytes);
const bool cpu_reusable = (dst.dma_buf_fd() < 0 && dst.byte_size() >= required_bytes &&
                           dst.dtype() == DataType::UINT8 && dst.shape() == target_shape);

if (dma_reusable) {
    if (dst.shape() != target_shape) {
        dst = Tensor::from_dma_buf(dst.dma_buf_fd(), target_shape, DataType::UINT8, dst.raw_data());
    }
} else if (!cpu_reusable) {
    dst = Tensor(target_shape, DataType::UINT8);
}
```
当外部传入匹配形状的 CPU Tensor 时，直接原地复用其缓冲，执行 `memset(114)` 并送入 RGA 写入，完全规避堆内存重新分配。

---

### 2.3 权限与探测稳健性增强

1. **去 debugfs 强依赖**：
   - 保留 `/sys/kernel/debug/rkrga/hardware` 探测；
   - 若打开失败（非 root 权限返回 NULL），通过 `imcheck_t` 动态扫描并提供 RK 平台通用基准安全保底值（4 字节），避免直接将 `rga_usable_` 置为 false。
2. **精确 `/dev/rga` 探针**：
   - 替换 `std::fopen("/dev/rga", "rb")` 为 `int fd = ::open("/dev/rga", O_RDWR | O_CLOEXEC)`，精准验证 `O_RDWR` 权限。
3. **统一 `improcess` 状态码校验**：
   - 替换 `status != IM_STATUS_SUCCESS` 为 `!imcheck_is_ok(status)`，支持 `IM_STATUS_NOERROR` (2)。

---

## 3. 测试与验证策略

1. **单元测试 (`native/tests/rga_vision_test.cpp`)**：
   - 扩展 `case_formats`：加入 1080P NV12 含 1088 垂直步长（`vstride=1088`）的测试用例，验证 RGA 能正常接收并不回退 CPU。
   - 新增 `case_tensor_reuse`：验证连续对同一个 CPU Tensor 调用 `letterbox` 时，`raw_data()` 指针保持一致，验证零堆分配复用。
   - 验证 `map_rga_status` 与 `imcheck_is_ok` 对各错误码的一致性。
2. **构建与回归**：
   - 运行本地原生测试：`python3 native/scripts/build.py test`。
   - 收工前全套校验：`make check`。
