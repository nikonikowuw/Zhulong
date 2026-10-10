# 执行计划：Native 异构硬件解码帧对象与多算法零拷贝管线

## 任务拆解与验证步骤

- [ ] **Step 1: 创建帧对象核心头文件**
  - 文件：`native/include/Zhulong/frame.hpp`
  - 交付：实现 `HardwareFrame` 结构体、移动语义防重复释放、`y_plane_size` 步长对齐计算以及 `release_fn` 闭环。
  - 验证命令：`python3 native/scripts/build.py check-headers` 或包含该头文件测试编译无报错。

- [ ] **Step 2: 编写单元测试套件**
  - 文件：`native/tests/frame_test.cpp`
  - 交付：
    1. 字段属性与步长计算测试（特别是 1080P NV12 的 `stride * vstride` UV 偏移验证）；
    2. 1:N 模拟多算法多线程共享与生命周期测试（验证最后一个 holder 释放时 `release_fn` 仅触发一次）；
    3. 移动语义防 double free 测试。
  - 验证命令：测试用例逻辑完备。

- [ ] **Step 3: 集成至 CMake 构建与测试套件**
  - 文件：`native/CMakeLists.txt`
  - 交付：增加 `frame_test` 可执行文件与 CTest 登记。
  - 验证命令：`cmake --build build/native/host --target frame_test`

- [ ] **Step 4: 全项测试与构建回归**
  - 执行：`make native-build && make native-test`
  - 验收：所有测试通过，无内存泄漏与死锁。
