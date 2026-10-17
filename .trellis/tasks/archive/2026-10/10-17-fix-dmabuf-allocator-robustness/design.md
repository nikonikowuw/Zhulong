# 原生 DMA-BUF 分配器工业级重构技术设计 (Design)

## 1. 架构定位与模块边界

`native/src/platform/dmabuf/` 是整个 Zhulong 原生层异构硬件零拷贝数据流的基础构件。它位于操作系统内核驱动之上，向公共运行库（`ZhulongAlgoRuntime`）和进程间通信层（`ZhulongAlgoIpc` / `FrameBroker`）提供统一的物理缓冲区抽象。

```txt
┌─────────────────────────────────────────────────────────────┐
│ 业务消费层: FrameBroker / RGA Backend / RKNN Zero-Copy Tensor │
└──────────────────────────────┬──────────────────────────────┘
                               │
               ┌───────────────▼───────────────┐
               │    ScopedCpuSync (RAII 守卫)  │
               └───────────────┬───────────────┘
                               │
               ┌───────────────▼───────────────┐
               │     DmaBuffer (资源描述符)     │
               └───────────────┬───────────────┘
                               │
               ┌───────────────▼───────────────┐
               │  DmaBufAllocator (探测与分配)  │
               └───────────────┬───────────────┘
                               │
       ┌───────────────────────┼───────────────────────┐
       ▼                       ▼                       ▼
/dev/dma_heap/* (动态扫描)    /dev/dri/card* (DRM Dumb)   非 Linux 透明降级
(CMA 连续 / System 散布)     (2D 步长二维化)           (返回 nullptr)
```

---

## 2. 核心技术设计与修复方案

### 2.1 DRM Dumb Buffer 几何二维化与独立 FD 所有权

#### 缺陷根因
`drm_mode_create_dumb` 接收 `width`、`height`、`bpp`。许多 DRM 驱动（i915、Rockchip DRM、Mali 等）在内核中限制 `width <= dev->mode_config.max_width`（通常为 4096 或 8192）。传入 `width = size, height = 1` 会直接触发驱动 `-EINVAL` 拒绝大尺寸申请。

#### 修复策略
1. **尺寸二维化计算**：
   - 固定步长 `width = 4096` 像素（以 8bpp 计算即 4096 字节，对齐大多数体系结构页面大小）；
   - 计算高度 `height = static_cast<uint32_t>((size + 4095) / 4096)`；
   - 内核在 ioctl 返回后填充 `create_req.pitch` 与 `create_req.size`；实际映射长度使用 `static_cast<size_t>(create_req.size)`，确保覆盖驱动实际申请的页边界；
   - `DmaBuffer` 的公开 `size()` 依然返回调用方请求的 `size`，但在析构 `munmap` 时使用真实的映射大小 `mmap_size_`。
2. **独立 DRM 描述符生命周期 (`dup`)**：
   - 构造 `DmaBuffer` 时，若关联 `drm_fd >= 0`，通过 `int owned_drm_fd = ::dup(drm_fd)` 获取独立 fd 并在其自身析构时 `::close(owned_drm_fd)`；
   - 杜绝依赖全局单例中可能已被关闭的共享 `drm_fd_`，消除跨单元析构引发的 UAF 或误关闭复用 fd 的风险。
3. **真实尺寸探测**：
   - 在 `probe()` 探针测试中，采用完整的二维化计算（例如测试申请一个 64KB 的 Dumb Buffer），真实验证驱动在多行几何结构下的响应。

### 2.2 多 BSP `/dev/dma_heap/` 动态遍历探测

#### 探测算法流程
不再硬编码 `/dev/dma_heap/cma` 或 `/dev/dma_heap/system`，而是实现 `scan_dma_heaps()`：
1. 检查 `/dev/dma_heap` 目录是否存在；
2. 若存在，使用 `opendir` / `readdir` 遍历所有节点：
   - 收集包含 `cma`（大小写无关）的设备路径列表（优先候选）；
   - 收集包含 `system`、`sys` 或 `uncached` 的设备路径列表（次优候选）；
3. 优先级匹配：
   - 首先尝试按顺序打开 CMA 候选节点（如 `/dev/dma_heap/cma`、`/dev/dma_heap/rk-dma-heap-cma`、`/dev/dma_heap/linux,cma`）；若成功打开且具备读写权限，判定为 `DMA_HEAP_CMA`；
   - 若无可用的 CMA 节点，再尝试打开 System 候选节点（如 `/dev/dma_heap/system`、`/dev/dma_heap/system-uncached`）；若成功打开，判定为 `DMA_HEAP_SYS`；
4. 若均不可用，尝试 fallback 至 `/dev/dri/card0`（DRM Dumb）；
5. 最终仍不可用时，标记为 `NONE`，实现完全的透明降级。

### 2.3 CPU Cache 同步防死锁与 `ScopedCpuSync` RAII 守卫

#### 忙等退避与超时防护
在 `sync_start` 与 `sync_end` 中：
```cpp
constexpr int MAX_RETRIES = 50;
constexpr auto RETRY_BACKOFF = std::chrono::microseconds(100);

int ret = 0;
int retries = 0;
do {
    ret = ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
    if (ret == -1 && (errno == EINTR || errno == EAGAIN)) {
        if (errno == EAGAIN) {
            if (++retries > MAX_RETRIES) {
                // 超时放弃，防止无界死循环
                record_error("DMA_BUF_IOCTL_SYNC timed out with EAGAIN");
                return false;
            }
            std::this_thread::sleep_for(RETRY_BACKOFF);
        }
    }
} while (ret == -1 && (errno == EINTR || errno == EAGAIN));
```

#### RAII ScopedCpuSync 守卫设计
新增头文件公开类 `ScopedCpuSync`：
```cpp
class ScopedCpuSync {
public:
    ScopedCpuSync(DmaBuffer& buffer, uint64_t flags = DMA_SYNC_RW)
        : buffer_(buffer), flags_(flags), locked_(buffer_.sync_start(flags)) {}

    ~ScopedCpuSync() {
        if (locked_) {
            buffer_.sync_end(flags_);
        }
    }

    [[nodiscard]] bool is_locked() const noexcept { return locked_; }

    ScopedCpuSync(const ScopedCpuSync&) = delete;
    ScopedCpuSync& operator=(const ScopedCpuSync&) = delete;
    ScopedCpuSync(ScopedCpuSync&&) noexcept = default;
    ScopedCpuSync& operator=(ScopedCpuSync&&) noexcept = default;

private:
    DmaBuffer& buffer_;
    uint64_t flags_;
    bool locked_{false};
};
```

### 2.4 可观测性与错误诊断

1. **结构化轻量级错误记录**：
   - 在 `DmaBufAllocator` 中维护 `std::string last_error_`，提供线程安全的错误信息存取；
   - 捕获底层系统调用错误并翻译为易懂的工业诊断信息：
     - `ENOMEM`：“CMA 物理连续内存池耗尽或物理碎片化严重”；
     - `EMFILE`/`ENFILE`：“进程/系统文件描述符达到上限”；
     - `EACCES`：“设备节点权限不足，未加入 video 或 render 用户组”；
     - `EINVAL`：“参数或几何对齐不满足驱动限制”。

---

## 3. 兼容性与跨层契约影响评估

- **对外接口兼容性**：
  - `DmaBuffer` 与 `DmaBufAllocator` 原有公有方法（`fd()`, `size()`, `mmap_addr()`, `sync_start()`, `sync_end()`, `allocate()`, `is_supported()`, `type()`, `type_name()`）完全保留，入参和返回值语义不变。
  - 新增 `ScopedCpuSync` 为纯增量特性，不破坏任何老代码调用约定。
- **上层调用方影响**：
  - `native/src/algorithm/ipc/frame_broker.cpp`：零破坏，无缝受益于动态扫描与 DRM 修复。
  - `tests/dmabuf_allocator_test.cpp`：扩展测试用例，覆盖大尺寸分配（如 16MB）与 `ScopedCpuSync`。

---

## 4. 回滚与风控计划

- 本模块自包含在 `native/src/platform/dmabuf/`，改动集中且无外部动态库依赖；
- 单元测试与构建测试完全由原生构建脚本覆盖；若发生意外回归，可通过 git 直接 revert，不污染 Go 宿主层或前端层。
