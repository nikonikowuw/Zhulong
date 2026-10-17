# 修复 RGA 视觉后端的垂直跨度与硬件探测缺陷

## Goal

修复 RgaVisionBackend 中 NV12 垂直步长硬编码导致的硬件解码帧误拒与色彩错位、Tensor 堆分配热路径抖动、debugfs 依赖与 /dev/rga 权限探针问题，提升异构平台在非 root 容器与板端生产环境下的稳定性和性能。

## Requirements

- **REQ-1 (NV12 / YUV420P 垂直步长动态推导)**：
  - 在 `resolve_source` 中，针对 `NV12`（2 planes）格式，当 `p1 > p0` 且 `(p1 - p0) % y_stride == 0` 时，动态推导实际垂直物理步长 `detected_hstride = (p1 - p0) / y_stride`；
  - 校验 `detected_hstride >= src.height`；
  - 将推导出的真实垂直步长传入 `wrapbuffer_fd` / `wrapbuffer_virtualaddr` 作为 `hstride`，使 librga 内部依据 `wstride × hstride` 能精准定位 UV 平面；
  - 针对 `YUV420P`（3 planes）同理支持基于实际平面跨度校验与连续推导；
  - 彻底支持 MPP / V4L2 解码器输出的 16 行对齐帧（如 1080P 视频 `height=1080, vstride=1088`）。
- **REQ-2 (Letterbox 热路径 Tensor 原地复用)**：
  - 修正 `try_rga_letterbox` 中对 `dst` 的复用条件：当传入 CPU `Tensor` 且容量满足 `byte_size() >= target_h * target_w * 3` 时，直接复用内存，禁止每帧重复执行 `dst = Tensor(...)` 堆分配；
  - 复用 DMA-BUF 或 CPU Tensor 时确保其形状与目标尺寸一致。
- **REQ-3 (运行期对齐探测去 debugfs 强依赖)**：
  - 优化 `probe()`：当 `/sys/kernel/debug/rkrga/hardware` 因系统权限（如普通用户运行、debugfs 0700）不可读时，改用 `imcheck_t` 动态探针探测 `stride_align_`，或安全保底为平台标准值（4 字节），避免在非 root 生产环境静默瘫痪退化。
- **REQ-4 (精确 `/dev/rga` 读写权限探针)**：
  - 将 `std::fopen("/dev/rga", "rb")` 替换为 POSIX `open("/dev/rga", O_RDWR | O_CLOEXEC)`，直接验证 RGA ioctl 所需的真实读写权限，避免 stdio 缓冲与只读误判。
- **REQ-5 (统一 `improcess` 状态码校验)**：
  - 在 `try_rga_letterbox`、`try_rga_resize`、`try_rga_crop_scale` 中，统一使用 `imcheck_is_ok(status)` 判定 `improcess` 返回值（同时支持 `IM_STATUS_SUCCESS` 与 `IM_STATUS_NOERROR`）。
- **REQ-6 (测试覆盖与回归验证)**：
  - 在 `native/tests/rga_vision_test.cpp` 扩充用例：验证带垂直步长对齐的 NV12 帧（如 1080P vstride=1088）能被正确识别和处理、CPU Tensor 复用测试、状态码映射测试。

## Acceptance Criteria

- [x] AC-1: `resolve_source` 正确支持 `vstride > height`（如 1080P height=1080, vstride=1088）的 NV12 连续帧，推导出的 `hstride` 传递给 RGA，消除 UV 偏移错位与误回退。
- [x] AC-2: `try_rga_letterbox` 传入容量充足的 CPU Tensor 时原地复用底层内存，热路径零堆分配。
- [x] AC-3: `/sys/kernel/debug/` 不可读时，`probe()` 仍能成功确定合法的 `stride_align_`，不静默禁用 RGA。
- [x] AC-4: `/dev/rga` 设备探针以 `O_RDWR` 校验读写权限，无 stdio 缓冲分配开销。
- [x] AC-5: `improcess` 返回 `IM_STATUS_NOERROR` 时被判定为成功，与 `imcheck_t` 及 `map_rga_status` 规约一致。
- [x] AC-6: `native/tests/rga_vision_test.cpp` 所有已有用例与新增垂直步长测试均通过（PASS）。
- [x] AC-7: 更新 `.trellis/spec/native/hardware-backends.md` 规范。

## Notes

- 遵循零 O(N) 原则：垂直步长推导为 O(1) 算术计算，绝不在内存中重排搬移像素。
- 遵循 C ABI 异常隔离原则：所有 RGA 入口严格保持 `noexcept`/`try-catch` 保护。
