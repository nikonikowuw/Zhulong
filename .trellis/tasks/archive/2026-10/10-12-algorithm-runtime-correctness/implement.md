# 实施计划

## 1. 插件结果类型与 ABI 边界

- [x] 扩展 `algo_runner_test`：断言 MockClassifier 的 result type 和 classification item 布局，并保留 detector 断言。
- [x] 重构 `algo.hpp` 注册胶水为共享的内部 adapter/result traits；保留 detector 宏并新增 classifier 宏。
- [x] 将 MockClassifier 改为实现 `classify()` 并使用新宏；确认 detector 插件源兼容。

验证：MockDetector/MockClassifier 构建通过；Runner 集成测试通过，覆盖插件加载、结果类型和 payload。

## 2. Tensor 与视觉预处理

- [x] 为 Tensor 增加零、负维度的失败测试（保留默认空 Tensor 的零元素语义）；使 `element_count()` 对非正维度显式失败。
- [x] 增加 CPU vision 测试：带 padding stride、BGR/RGB 转换、不支持格式拒绝及双线性已知像素值。
- [x] 更新 resize 接口按源/目标 stride 运行；校验尺寸、stride、格式后再读源内存。
- [x] 实现 RGB24/BGR24 双线性缩放和目标通道顺序转换；输出直接写入 letterbox Tensor 区域，移除额外的 `resized_temp` 全帧缓冲；不扩展 Session/forward workspace API。
- [x] 将 letterbox 和 resize 注释统一为实际双线性语义；将 NMS 注释改为准确的标量复杂度描述。

验证：runtime 测试通过；ASan 全套 Native 测试通过。

## 3. Runner 输出与动态符号

- [x] 为 JSON 字符串 quoting helper 编写包含引号、反斜杠、换行和控制字符的往返测试。
- [x] Runner 所有字符串字段统一调用 helper。
- [x] 定义插件导出宏，设置示例插件 hidden visibility；确保唯一 ABI 入口仍为可见符号。
- [x] 在 Linux 构建验证动态符号表只定义预期入口；现有集成测试验证入口可通过 `dlsym` 加载。
- [x] 用 Linux export map 将除 `zhulong_plugin_get_api` 外的定义符号设为 local；ASan 验证发现并覆盖弱 RTTI 符号泄漏。

验证：JSON、Runner 和符号表测试通过。

## 4. 全量验证与收尾

- [x] 运行 `make native-test`。
- [x] 检查 sanitizer/测试输出、完整 diff、ABI 兼容性及已有未提交改动未被覆盖。
- [x] 按质量检查结果修复本任务引入的问题；更新算法插件规范中缺失的执行契约。

验证命令：

```bash
make native-test
python3 native/scripts/build.py --sanitizer address test
```

结果：两组命令均 14/14 通过。

Linux 动态符号检查：

```bash
nm -D --defined-only <MockPlugin.so>
```

MockDetector 和 MockClassifier 均只定义 `zhulong_plugin_get_api`。

## 风险与回滚点

- **预处理数值差异**：最近邻改为双线性会改变推理输入。测试固定像素中心采样约定；与项目既有算法 SDK 设计一致。
- **不支持格式错误码**：NV12/YUV420P/RGBA32 改为明确拒绝；调用端需上游转成 RGB24/BGR24。
- **插件符号可见性**：hidden 模式和 Linux export map 会隐藏其他定义符号；导出宏和 `dlsym` 集成测试必须同批提交。
- **Tensor 动态维度**：当前固定 shape API 对非正维度抛出异常；C ABI adapter 必须拦截异常，不允许越过 ABI。
- 如跨平台导出属性或动态符号检查不兼容，先只保留 Linux/Clang/GCC 支持路径和现有 CMake 平台判断；不改变 ABI 表布局。回滚时仅撤销本任务涉及的 SDK/runtime/runner/CMake/test 文件改动。
