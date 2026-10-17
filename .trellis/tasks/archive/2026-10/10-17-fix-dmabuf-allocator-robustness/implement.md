# 原生 DMA-BUF 分配器工业级重构执行计划 (Implement)

## 1. 执行清单 (Checklist)

### 阶段一：头文件与接口定义扩充
- [x] 1.1 在 `native/src/platform/dmabuf/dmabuf_allocator.hpp` 中新增 `ScopedCpuSync` RAII 守卫类。
- [x] 1.2 在 `DmaBuffer` 中补充真实映射尺寸 `mmap_size_`，修改构造函数以支持传入真实映射大小并由自身管理 DRM 描述符生命周期。
- [x] 1.3 在 `DmaBufAllocator` 中补充 `last_error()` 诊断接口与动态扫描私有辅助函数。

### 阶段二：底层实现重构 (CPP)
- [x] 2.1 重写 `DmaBufAllocator::probe()`：实现 `/dev/dma_heap` 目录动态扫描，根据 `cma` 与 `system` 关键词自适应匹配节点。
- [x] 2.2 修复 DRM Dumb 分配：实现 2D 几何尺寸二维化计算（4096 步长与高度折算），按真实 `size` 映射，并使用 `::dup(drm_fd)` 传递独立的描述符给 `DmaBuffer`。
- [x] 2.3 修复 `DmaBuffer` 析构与生命周期：析构时独立关闭持有的 `drm_fd_`，消除 UAF 风险；使用 `mmap_size_` 执行 `munmap`。
- [x] 2.4 优化 CPU 同步：在 `sync_start` / `sync_end` 中增加 50 次重试上限与 100 微秒自旋退避，防止 `EAGAIN` 紧凑死循环。
- [x] 2.5 补充详细错误描述注入（`last_error_`），覆盖 `ENOMEM`、`EMFILE`、`EACCES` 等。

### 阶段三：测试套件扩展与验证
- [x] 3.1 在 `native/tests/dmabuf_allocator_test.cpp` 中新增大内存分配测试（如 16MB 4K 帧缓冲），验证二维化计算在各环境下的安全性。
- [x] 3.2 增加 `ScopedCpuSync` RAII 自动回写与异常退出的单元测试。
- [x] 3.3 验证非 Linux / 降级环境下的降级行为保持稳定。
- [x] 3.4 执行本地测试：`python3 native/scripts/build.py test`。
- [x] 3.5 执行全库依赖项构建验证：`make native-build` 与完整原生测试回归。

---

## 2. 验证命令集

```bash
# 1. 编译并运行原生模块的 DMA-BUF 测试用例
python3 native/scripts/build.py test

# 2. 检查编译告警与代码风格（必须零警告）
make native-build
```

---

## 3. 风险点与回滚策略

- **受影响的关键文件**：
  - `native/src/platform/dmabuf/dmabuf_allocator.hpp`
  - `native/src/platform/dmabuf/dmabuf_allocator.cpp`
  - `native/src/algorithm/ipc/frame_broker.cpp`
  - `native/tests/dmabuf_allocator_test.cpp`
- **回滚方式**：
  若出现意外的跨平台编译断裂，可直接对上述文件执行 `git checkout -- <file>` 瞬时恢复，不破坏任何上层业务逻辑。
