# 执行计划：实现人脸识别算法包 (10-14-face-algo-package)

## 门禁与前置检查

- [x] 确认工作区基线通过：`make native-test` 与 `make go-check`。
- [x] 确认 `native/src/algorithm/runner/gallery_index.hpp` 与 `native/sdk/include/zhulong/algo.hpp` 状态。

---

## 阶段一：跨动态库底库共享注册表实现 (Step 1)

- [x] **1.1 在 `ZhulongAlgoRuntime` 中暴露 `GalleryRegistry`**：
  - 在 `native/src/algorithm/runtime/`（或公开运行库）中提供线程安全的全局单例 `GalleryRegistry`，支持 `reset`、`upsert`、`remove`、`snapshot` 与 `size`；
  - 导出共享符号供 Runner 和 Plugin 在同一进程空间内交互；
  - 在 `native/src/algorithm/runner/runner.cpp` 中将下行 `MSG_GALLERY_*` 消息同步至 `GalleryRegistry`。

- [x] **1.2 单元测试覆盖**：
  - 编写 C++ 单元测试验证多线程下 `GalleryRegistry` 的 `snapshot` 读写隔离与线程安全。

---

## 阶段二：人脸识别插件实现 (Step 2)

- [x] **2.1 编写 `FaceRecognitionPlugin`**：
  - 在 `packages/face_recognition/src/` 实现人脸识别插件与相关数学辅助模块（`aligner.cpp`、`matcher.cpp`）；
  - 支持 `init(config_json)` 解析 `threshold`（默认 0.40）与 `top_k`（默认 3）；
  - 实现 `detect_persons` 流水线：
    1. 人脸检测定位（归一化坐标与 5 点关键点）；
    2. ArcFace 5 点参考模板对齐与标准化；
    3. 512 维 L2 归一化特征提取（与引擎 C ABI 保持同构特征空间）；
    4. 1:N 底库比对（结合 `GalleryRegistry::instance().snapshot()`）；
    5. 判定 `matched`，填入 `person_id`（业务键）、`person_name`、`similarity` 与 Top-K `candidates`；
    6. 组装 `zhulong::PersonItem` 退化语义返回。
  - 使用 `ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN` 导出。

- [x] **2.2 集成至 CMake 构建**：
  - 在 `native/CMakeLists.txt` 中添加目标 `FaceRecognitionPlugin`，编译 `packages/face_recognition/src/` 源码并链接 `ZhulongAlgoRuntime`；
  - 配置 hidden visibility 与 version script 符号导出限制。

---

## 阶段三：插件单元测试与验包门禁验证 (Step 3)

- [x] **3.1 编写插件单测**：
  - 在 `native/tests/face_recognition_plugin_test.cpp` 中测试：
    - 空底库自测（验包模式，返回 `matched = false`，坐标归一化）；
    - 已录入底库匹配测试（返回 `matched = true`，`person_id` 正确）；
    - 异人不同照拒绝测试（低于阈值，`matched = false`）。
  - 将测试挂入 CTest 套件。

- [x] **3.2 黑盒验包验证**：
  - 使用 `zhulong_algo_runner --verify-plugin ... --sample ...` 执行黑盒验包，确保退出码为 0，输出 JSON 结构符合门禁要求。

---

## 阶段四：算法包打包与 Go 宿主集成测试 (Step 4)

- [x] **4.1 组织算法包工程与打包脚本**：
  - 建立 `packages/face_recognition/` 工程：
    - `targets/cpu/manifest.json` 与 `targets/rk3568/manifest.json`；
    - `sample.jpg`；
    - `config.schema.json`；
    - `scripts/build_package.py` 自动化打包脚本，根据 `--target` 打包生成对应芯片架构的单一目标扁平包 `build/packages/face_recognition-<arch>-<target>.tar.gz`。

- [x] **4.2 Go 侧验证与安装集成测试**：
  - 在 `internal/algorithm/` 中增加测试，验证打包输出的 `face_recognition` 算法包能通过 `verifier.VerifyDirectory` 与 `service.InstallAlgorithm`；
  - 验证端到端 Supervisor 启动该算法包时接收 `MSG_GALLERY_SYNC` 后的运行表现。

---

## 阶段五：全量门禁检查 (Step 5)

- [x] 运行系统全部测试与静态分析：
  ```bash
  make native-test
  make go-check
  make check
  ```
- [x] 验证 ASan / race 干净，确保无内存泄漏与数据竞争。

---

## 回滚计划

1. 插件隔离：`FaceRecognitionPlugin` 为独立动态库与算法包，不修改媒体流与核心引擎的基础行为；
2. 运行库兼容：`GalleryRegistry` 为只追加抽象，不破坏既有检测与分类插件。
