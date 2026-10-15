# 实现方案与执行计划：RK3568 全链路 DMA-BUF 硬件零拷贝 (10-15-rk3568-dmabuf-zero-copy)

## 1. 执行清单 (Ordered Checklist)

### 阶段一：DMA-BUF 底层物理分配器实现 (`DmaBufAllocator`)
- [ ] **1.1 实现 `DmaBufAllocator` 核心**：
  - 新建 `native/src/platform/dmabuf/dmabuf_allocator.hpp` 与 `dmabuf_allocator.cpp`；
  - 支持 `/dev/dma_heap/cma` 与 `/dev/dma_heap/system` 的 `DMA_HEAP_IOCTL_ALLOC` 分配；
  - 备选支持 DRM Dumb Buffer ioctl；
  - 实现成对称 CPU Cache 同步（`DMA_BUF_IOCTL_SYNC` 带 `DMA_BUF_SYNC_START` / `END` 及重试机制）；
  - 非 Linux / 桩环境返回 `nullptr` 并优雅降级；
  - *验证*：编译并通过单元测试 `ZhulongDmaBufAllocatorTests`。

### 阶段二：IPC 协议扩展与跨进程 SCM_RIGHTS 透传
- [ ] **2.1 增强 IPC 套接字支持 SCM_RIGHTS 传输**：
  - 在 `native/src/algorithm/ipc/` 中扩展 `send_message_with_fd` 与 `receive_message_with_fd`；
  - 使用 Linux 标准 `msghdr` 与 `cmsghdr` 封装 `SCM_RIGHTS`；
  - 接收端自动设置 `FD_CLOEXEC`，杜绝句柄派生泄露；
  - *验证*：编写跨进程 socket 测试，验证 fd 能在进程间正确透传并读取相同数据。
- [ ] **2.2 升级 `FrameBroker` 槽位管理**：
  - 槽位池支持持有 DMA-BUF 物理内存；
  - `publish_frame` 在可用时使用 DMA 槽位，并在下发 `MSG_FRAME_DISPATCH` 时附带 `dma_buf_fd`；
  - *验证*：FrameBroker 单元测试通过。
- [ ] **2.3 升级 `Runner` 接收与生命周期闭环**：
  - `runner.cpp` 在处理 `MSG_FRAME_DISPATCH` 时提取 `dma_buf_fd`，赋值给 `zhulong_frame_view`；
  - 帧处理完毕或 `MSG_FRAME_ACK` 时负责关闭该局部接收 fd。
  - *验证*：Runner 在有 fd 和无 fd 下均能稳定接收帧。

### 阶段三：C ABI、张量与硬件加速器改造
- [ ] **3.1 扩展 C ABI 与 SDK 视图**：
  - `native/sdk/include/zhulong/plugin.h` 中的 `zhulong_frame_view` 显式增加 `int32_t dma_buf_fd` 字段（带自然对齐）；
  - `native/sdk/include/zhulong/algo.hpp` 的 `zhulong::Image` 增加 `int dma_buf_fd() const noexcept` 访问器；
  - `native/src/algorithm/runtime/tensor.hpp` 增加 DMA-BUF 绑定支持（`Tensor::from_dma_buf(fd, dims, dtype)`）。
  - *验证*：`make native-build` 编译通过。
- [ ] **3.2 重构 `RgaVisionBackend` 适配 DMA-BUF**：
  - 当输入与输出具有有效 `dma_buf_fd` 时，优先使用 `wrapbuffer_fd`；
  - 保留原有虚拟地址模式兜底。
  - *验证*：RGA 原生测试与交叉编译通过。
- [ ] **3.3 重构 `RknnInferenceBackend` 适配零拷贝绑定**：
  - 当输入 `Tensor` 具有 `dma_buf_fd >= 0` 时，调用 `rknn_create_mem_from_fd` 与 `rknn_set_io_mem`；
  - 保留 `rknn_inputs_set` 兜底。
  - *验证*：RKNN 交叉测试通过。

### 阶段四：算法流水线接入与 RK3568 板端真机验收
- [ ] **4.1 适配人脸算法包 `packages/face_recognition`**：
  - 检测模型前处理支持使用 DMA-BUF 输入 Tensor；
  - 通过 `build_package.py` 打包并在板端验证。
- [ ] **4.2 板端实机性能标定与压力测试**：
  - 部署至 `rk3568` 真机，运行 1000 帧全链路零拷贝压测；
  - 观测 CPU 占用率、内存占用（无 fd 泄漏）与端到端延迟变化并记录证据。
- [ ] **4.3 全栈门禁回归**：
  - 运行 `make check`，确保 Web 前端、Go 宿主及 Native 单元测试 100% 绿灯。

---

## 2. 验证命令集 (Validation Commands)

```bash
# 1. 本地 Native 编译与测试 (验证 Fallback 兼容性)
python3 native/scripts/build.py build
python3 native/scripts/build.py test

# 2. RK3568 交叉编译 (编译 RGA+RKNN 硬件后端)
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn-rga.json build

# 3. 部署并在 RK3568 板端执行真实压测与 fd 泄漏监测
scp build/native/cross-rk3568-rknn-rga/zhulong_algo_runner rk3568:/userdata/zhulong-test/
# 板端检查 ls /proc/<pid>/fd/ 是否保持恒定（无 fd 泄漏）

# 4. 全栈门禁收工验证 (必须全绿)
make check
```

---

## 3. 回滚方案 (Rollback Points)

- 硬件零拷贝作为性能优化特性，底层全部保留原有虚拟内存/SHM 通路；
- 若出现特定内核驱动兼容性异常，可通过配置或宏快速关闭 DMA-BUF 传输回退到当前稳定版本。
