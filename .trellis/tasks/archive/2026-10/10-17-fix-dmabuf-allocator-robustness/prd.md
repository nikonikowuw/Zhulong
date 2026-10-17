# 重构与修复原生 DMA-BUF 分配器工业级缺陷 (PRD)

## Goal

将 `native/src/platform/dmabuf/` 从功能验证型原型（MVP）提升至严苛的工业级（Production-Ready）水准，修复 DRM Dumb 几何尺寸超限导致的分配失败与假阳性、动态扫描适配多样化 Linux BSP 的 `/dev/dma_heap/*`、消除 CPU Cache 同步忙等死循环隐患，并引入 `ScopedCpuSync` RAII 守卫与现场故障诊断机制。

## Background & Confirmed Facts

1. **代码现状与调用路径**：
   - 核心代码位于 `native/src/platform/dmabuf/dmabuf_allocator.hpp` 与 `dmabuf_allocator.cpp`。
   - 依赖方包括公共运行库 `ZhulongAlgoRuntime`（RGA、RKNN）、静态库 `ZhulongAlgoIpc`（`FrameBroker`）、算法运行器 `runner.cpp`，以及单元测试 `ZhulongDmaBufAllocatorTests`。
2. **基线平台**：
   - 边缘芯片基线为 Rockchip RK3568（Debian 11 arm64 / Linux 内核 5.10.226），宿主开发机支持 Linux x86_64 及 macOS（降级环境）。
3. **定位与契约规范**：
   - 对齐 `.trellis/spec/native/hardware-backends.md` 第 4.1 节定义的底层物理分配器契约。

## Requirements

1. **DRM Dumb 几何尺寸二维化与探测修正** (`native/src/platform/dmabuf/dmabuf_allocator.cpp:286-302, 222-237`)
   - 修复将 1D 内存直接映射为 `width = size, height = 1` 导致驱动校验 `width > max_width` 返回 `-EINVAL` 的致命缺陷。
   - 采用标准 2D 尺寸二维化计算：固定 `width = 4096` 像素（4KB 步长对齐），计算 `height = (size + 4095) / 4096`，计算总尺寸 `alloc_size = width * height`，确保大内存（1080p、4K 帧缓冲，如 16MB）可正常分配。
   - 修复 `probe()` 探测：探测时必须使用真实可能出现的有效尺寸（例如使用同等二维化逻辑）进行验证，杜绝 64 字节假阳性。

2. **DmaBuffer 生命周期与 FD 独立所有权** (`native/src/platform/dmabuf/dmabuf_allocator.cpp:86-107`)
   - 消除 `DmaBuffer` 析构时直接使用 `DmaBufAllocator::drm_fd_` 导致的跨生命周期 UAF（Use-After-Close）与文件描述符混淆隐患。
   - 在创建 `DmaBuffer` 时，若关联 DRM 节点，必须通过 `::dup(drm_fd_)` 持有独立的描述符并由 `DmaBuffer` 自身 RAII 析构时安全关闭；或保持 allocator 引用语义并增加防护。

3. **动态扫描适配多样化 BSP dma-heap 路径** (`native/src/platform/dmabuf/dmabuf_allocator.cpp:200-220`)
   - 移除硬编码 `/dev/dma_heap/cma` 与 `/dev/dma_heap/system` 的局限性。
   - 动态扫描 `/dev/dma_heap/` 目录：
     - 优先级 1（物理连续 CMA 堆）：匹配含 `cma` 的节点（如 `cma`、`rk-dma-heap-cma`、`linux,cma`）；
     - 优先级 2（系统散布内存堆）：匹配含 `system`、`sys` 或 `uncached` 的节点；
   - 记录探测到的设备绝对路径，并在失败时优雅降级至 DRM Dumb。

4. **CPU 缓存同步安全与 ScopedCpuSync 守卫** (`native/src/platform/dmabuf/dmabuf_allocator.cpp:142-175`)
   - 消除 `sync_start` / `sync_end` 在 `errno == EAGAIN` 时无退避的极速忙等死循环；增加最大重试次数（如 50 次）与微秒级退避（`std::this_thread::sleep_for(std::chrono::microseconds(100))`）。
   - 提供 RAII 包装类 `ScopedCpuSync`：构造时自动执行 `sync_start`，析构时自动执行 `sync_end`，支持 `is_locked()` 状态检查，杜绝异常与提前退出导致的 Cache 脏数据残留。

5. **现场故障可观测性与诊断支持** (`native/src/platform/dmabuf/dmabuf_allocator.cpp:251-320`)
   - 提供错误原因查询或清晰的诊断日志，记录系统调用返回失败的具体 `errno`（尤其是 CMA 耗尽 `ENOMEM`、系统描述符耗尽 `EMFILE`、非法参数 `EINVAL`）。
   - 保留非 Linux 平台的透明降级，`is_supported()` 恒定返回 `false` 且不输出干扰性错误。

## Key Decisions

- **聚焦现代 Linux 5.6+ 内核**：排除 Linux 4.x legacy `/dev/ion`，避免引入已被内核废弃的技术债，完全契合 RK3568（Linux 5.10）及现代嵌入式主流基线。
- **DRM Dumb 二维化计算策略**：固定宽度为 4096 字节（兼顾 4K 对齐与 DRM `max_width` 边界安全），计算高度向上取整，并按照实际 `create_req.size` 进行 `mmap`。
- **独占 DRM FD 所有权**：`DmaBuffer` 内部持有独立的 `dup` 描述符，彻底隔离析构顺序导致的潜在 UAF 风险。

## Acceptance Criteria

- [x] 1. DRM Dumb 分配支持任意合法大尺寸（如 16MB 4K 帧缓冲），不再因 `width > max_width` 触发 `-EINVAL`。
- [x] 2. `DmaBuffer` 析构时不依赖单例的外部裸 `drm_fd_`，消除跨生命周期悬空操作风险。
- [x] 3. `/dev/dma_heap/` 实现自适应动态扫描，优先命中各类命名规则的 CMA 连续堆，次选系统散布堆。
- [x] 4. `sync_start` / `sync_end` 在遇到 `EAGAIN` 时具有可控重试与自旋退避，杜绝 100% CPU 忙等死锁。
- [x] 5. 提供 `ScopedCpuSync` RAII 守卫，异常安全且析构自动回写 Cache。
- [x] 6. 单元测试 `ZhulongDmaBufAllocatorTests` 覆盖大尺寸分配、Scoped 守卫与降级分支，全部通过。
- [x] 7. 现有依赖方 `FrameBroker` 与上层代码完全兼容，零编译警告与破坏性断裂。

## Out of Scope

- 废弃的 Linux 4.x `/dev/ion` 接口实现。
- 硬件 DMA-BUF 环形池化（Buffer Pool）由业务上层/流水线管理，本模块专注 Allocator 与 Buffer RAII 本身。
