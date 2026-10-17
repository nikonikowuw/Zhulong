# 执行计划：修复 RGA 视觉后端的垂直跨度与硬件探测缺陷

## 1. 任务阶段清单

### 阶段 1：修改 RGA 视觉后端实现 (`native/src/backends/rga/`)
- [x] 1.1 修改 `native/src/backends/rga/rga_vision_backend.cpp` 中的 `resolve_source`：
  - 动态计算 NV12 / YUV420P 的物理垂直跨度 `hstride`；
  - 传递 `hstride` 至 `wrapbuffer_fd` 与 `wrapbuffer_virtualaddr`；
  - 验证：支持 tight 布局与 padded 垂直步长布局。
- [x] 1.2 修改 `native/src/backends/rga/rga_vision_backend.cpp` 中的 `try_rga_letterbox`：
  - 修复 Tensor 复用条件，支持 CPU Tensor 原地复用；
  - 修正 DMA-BUF 复用时的形状对齐；
  - 替换 `improcess` 返回码检查为 `!imcheck_is_ok(status)`。
- [x] 1.3 修改 `native/src/backends/rga/rga_vision_backend.cpp` 中的 `try_rga_resize` 与 `try_rga_crop_scale`：
  - 替换 `improcess` 返回码检查为 `!imcheck_is_ok(status)`。
- [x] 1.4 修改 `native/src/backends/rga/rga_vision_backend.cpp` 中的 `probe`：
  - 改用 `::open("/dev/rga", O_RDWR | O_CLOEXEC)` 校验读写权限；
  - 增加 debugfs 失败时的动态探测与安全保底策略。

### 阶段 2：单元测试扩展与验证 (`native/tests/rga_vision_test.cpp`)
- [x] 2.1 增加带垂直对齐的 NV12 格式（如 1080P vstride=1088）测试用例；
- [x] 2.2 增加 CPU Tensor 连续调用 Letterbox 时的内存地址复用断言测试；
- [x] 2.3 运行原生测试：`python3 native/scripts/build.py test`。

### 阶段 3：规范与文档更新
- [x] 3.1 更新 `.trellis/spec/native/hardware-backends.md` 记录真实垂直步长（hstride）推导规约与权限探针契约；
- [x] 3.2 执行项目收工前全量校验：`make check`。

---

## 2. 验证命令

```bash
# 1. 编译并执行本地 native 单元测试
python3 native/scripts/build.py test

# 2. 运行所有相关测试并检查 Go/CGO
make check
```

---

## 3. 回滚方案

若发现板端兼容性回归，可通过 `git checkout native/src/backends/rga/ native/tests/rga_vision_test.cpp` 快速恢复。
