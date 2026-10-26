# Implement: 算法包黑盒化

> 需求见 `prd.md`，技术设计与取舍见 `design.md`。
> **实现顺序说明**：各阶段可独立提交。静态库与 SDK 头文件可先增量构建；这不代表最终宿主兼容旧 `.so` 包。阶段 4 切换宿主后只接受包根 `<manifest.id>` 可执行文件，旧包须重建并重新安装。旧 SHARED/runner target 如被 native 测试依赖，可暂留为内部测试资产，不得用于算法包分发、安装或宿主启动。

## 全局约束（AGENTS.md）

- **门禁 grep 口径（D4）**：本文件所有 `grep` 断言只针对代码 / 构建脚本 / 配置值，统一用 `--include=` 限定并剔除注释行（写法见 `prd.md` 的「门禁通用约定」）。理由：`native/scripts/build.py:229` 这类「为何废除 `cpu_mock`」的 docstring 是**知识**不是**用法**；把注释算进断言，等于把「写下禁令」判成「违反禁令」，门禁会被当噪音关掉。Python docstring 不能靠行首规则剔除，必须显式给豁免（文件 + 处数上限）。

- 改完 C++ **必须** `make native-build`（Air 只监听 Go/TOML）。
- 依赖 `internal/engine` 的 Go 包**绝不**裸跑 `go build/test/vet`，一律走 wrapper：
  ```bash
  python3 native/scripts/build.py go test -race ./internal/algorunner -run TestXxx
  ```
- 不依赖 engine 的包可直接 `go test -race ./internal/algorithm`。
- `internal/algorunner` / `internal/algorithm` 需要 `-race`。
- 收工前 `make check`。
- **交叉只允许编译，不允许执行测试。**

---

## 已拍板（二）：每 target 一套独立实现（R11 / R13）

原「一套源码多平台 + 编译期后端宏 + `InputFacts`/`prepare_input` 契约下沉」**已撤销**（理由见 `prd.md` R11/R13、F21–F24）。本实现按以下形态执行，任何与 `design.md` §4.8.4 / §4.8.6 冲突之处以本节为准：

- 源码目录 = `packages/<id>/<target>/`，target 之间**零源码引用**，允许重复纯数学；
- **不共享测试夹具**：每 target 自带 `tests/`，用包根 `sample.jpg` 跑出 `result.jpg` + 终端明细表；`tests/` 不进归档，可依赖图像库；
- 出包时资产**摊平**到包根，归档内无 `targets/`；
- 主机档位叫 **`onnx`**（不是 `cpu`，见 `prd.md` F25）；`plate_recognition` 只有 `onnx`，`face_recognition` 有 `onnx` 与 `rk3568`（后者只编译、不验包）。

---

## 已拍板（一）：方案 A —— `TaskContext` 门面修复（`design.md` §10.9）

此修复**纳入本任务**，是阶段 3 的前置改动。原因：SDK 分发物是本任务的核心交付物，`detect` 签名一经发布即成为源码级契约。

- [ ] **3.0a** `native/sdk/include/zhulong/algo.hpp`：在 `zhulong` namespace 下公开 `TaskContext { uint64_t task_id; TaskConfig params{}; }`；`TaskConfig` 当前为空值类型，不预设业务字段。所有 `ResultAdapter::infer()` 及 `detect` / `classify` / `detect_persons` / `detect_vehicles` 签名传入 `TaskContext&`。
- [ ] **3.0b** `packages/face_recognition/{onnx,rk3568}/src/*` 与 `packages/plate_recognition/onnx/src/*` 的入口类同步改签名：**只加参数，不读取 `params`**，保证输出与改造前一致。
- [ ] **3.0c** SDK 以模板 `run<PluginClass, ResultAdapter>` 在编译期绑定算法和结果适配器；`ZHULONG_ALGO_MAIN(PluginClass, ResultAdapter, PluginName, PluginVersion)` 生成唯一 `main`。`PluginFactory` 仅为 SDK `detail` 层的 C ABI API-table 构造函数指针，不由算法作者实现；结果适配器以 `zhulong::sdk` 下的四种公开类型提供。
- [ ] **3.0d** `algo_main.cpp` 每个 Worker 持有一路流对应的 `TaskContext`，在帧边界调用空实现 `drain_control_messages(TaskContext&)`；不引入 task map 或多 broker 复用 Worker。

验收口径：AC3 由「代码不变」改为「同参数下输出一致」——允许入口签名变更，不允许 `count`/`class_id`/`text`/`score` 语义变化。

**已知遗留（不在本任务）**：`TaskContext::params` 本任务始终为默认值；从 `MSG_TASK_CONFIG` 填充是后续任务。

---

## 阶段 0：验证前置假设并记录基线（不改代码）

- [x] **0.1 已确认：`brokerResourceDirs()` 不按宿主实例隔离**
      → `resource_lock_linux.go:73-77` 返回 `[ZHULONG_FRAME_BROKER_SOCK 或 /tmp, /dev/shm]`，socket 名为 `<base>/zhulong_broker_<stream_id>.sock`（`frame_broker.cpp:58`），**全局无隔离**。
      → 结论：`design.md` §6.2 原「broker 目录认亲」条**废弃**（对任意实例恒真，零区分力），改用 `exe` ∈ `<dataDir>/algorithms/`。已写回 `design.md` §6.2 / §9。

- [x] **0.2 记录改造前基线**（用于 AC3 行为等价对比），写入 `research/baseline.md`：
      已实测并固化至 `research/baseline.md`：
      - `libPlateRecognitionPlugin.so` 未定义 `zhulong` 符号数：**3**；
      - `libFaceRecognitionPlugin.so` 未定义 `zhulong` 符号数：**9**；
      - 动态依赖与文件大小（runtime 2.0MB, plate 1.6MB, face 2.9MB, runner 2.2MB）。

- [x] **0.3 确认 `plate_recognition` 的 `onnx` 档位可独立验包**（阶段 3 的验收样本）。
      已实测并固化至 `research/baseline.md`：
      - 直接传 `.jpg` 回退合成全黑帧（`sample_decoded: false, count: 0`）；
      - 宿主解码为标准 PPM 后真实推理：`sample_decoded: true, width: 1106, height: 826, result_type: 4, count: 2`（检出 2 处车牌）。

- [x] **0.4 记录宿主侧特征提取的现状基线**（用于 AC9/AC13 的前后对比与参数标定，只记录不修改）：
      已实测并固化至 `research/baseline.md`：
      - `TestExtractFaceFeature_ValidationAndSuccess` PASS (0.71s)，依赖 `build/native/host/libFaceRecognitionPlugin.so`；
      - 板端 RKNN 硬件实测耗时标为「未测」。

**Review gate 0**：0.1/0.2/0.3/0.4/0.5 基线数据已全部采集并写入 `research/baseline.md`，确认无误。已进阶段 1。

- [x] **0.5 记录「包构建寄生」的现状证据**（用于 AC16 前后对比）：写入 `research/baseline.md`：
      - `find packages -name 'CMakeLists.txt'` = 0；
      - `native/CMakeLists.txt:366-370` 直接引入 `packages/` 源码；
      - `find build/native/host -name '*Recognition*'` 产出 5 项目标文件/库（改造后必须为 0）。

- [x] **0.6 基线补录口径（D5，必读）**：阶段 1.0（R14，commit `7aeb3bb`）已先于本阶段落地，基线记录已在 `research/baseline.md` 顶部如实标注采集时间、HEAD `3842359` 及 R14 已生效事实，`make native-test` 28/28 通过。

---

## 阶段 1：`ZhulongAlgoRuntime` 增加静态产物（纯增量）

> 目标：构建 `libZhulongAlgoRuntime.a`。迁移期间旧 SHARED/runner target 可供 native 基线测试使用，但不构成算法包兼容支持；阶段 4 后宿主仅接受包根可执行文件。

- [x] **1.0 把 `cpu_mock` 从产品面移除（R14 / AC22，**必须先做**，否则后面每 target 一份实现时还要多养一个假后端）**

  - [x] **1.0a** `native/src/algorithm/runtime/runtime_factory.cpp`：分支收敛为 `rknn` / `onnx` / `#error "no inference backend selected: pass --inference-backend onnx|rknn"`；删除 `cpu_inference_backend.{hpp,cpp}` 与 `default_inference_backend_name()` 里的 `cpu_mock` 分支（返回 `onnx` / `rknn`）。
  - [x] **1.0b** `native/scripts/build.py`：删除 `default_inference_backend()`（`:233-247`）；`inference_backend` 的 choices 加 `test_stub`，且**只允许**与夹具目标同用；profile 未声明时直接失败并打印「profile 必须声明 inference_backend（host: onnx；板端: rknn）」。
  - [x] **1.0c** 桩搬运：`native/src/algorithm/runtime/cpu_inference_backend.*` → `native/tests/fixtures/stub_inference_backend.{hpp,cpp}`（类名 `StubInferenceBackend`，夹具标记 `test_stub:`）。
        **实现方式与初稿不同（有意）**：没有引入 `ZhulongAlgoRuntimeStub` 这个并行库，而是让 `ZhulongAlgoRuntime` 在 `ZHULONG_INFERENCE_BACKEND=test_stub` 构建里**额外编译**该夹具源。理由：同一构建目录只有一个后端，并行库会导致同一进程里两份运行时（两份 `GalleryRegistry` / `DmaBufAllocator` 单例），正是本任务要消灭的那类 ABI 债。产品构建（onnx/rknn）从不编译该文件，`.so` 内桩符号数为 0。
  - [x] **1.0d** 删除所有 `[SKIP] ... cpu_mock ... rejects real models` 分支（`face_recognition_plugin_test.cpp:51-53`、`plate_recognition_plugin_test.cpp:78,216,256`、`face_feature_extract_test.cpp:35`、`algo_runtime_test.cpp:76`），改为：需要真实模型的用例走真后端；不需要的走夹具 + `test_stub:` 标记（标记字符串只存在于夹具源内，产品侧消失）。夹具构建下的断言改为「真实模型必须被拒」，并接受 `NOT_SUPPORTED|NOT_FOUND` 两种拒绝码（旧代码在「模型找不到」时会假红）。
  - [x] **1.0e** profile 处置（逐个确认，**不要静默保留**）：
        - `rk3568-rknn.json` / `rk3568-rknn-rga.json`：已显式 `rknn`，不动；
        - `rk3568.json`（板端 broker/runner 回归用）：改为夹具 profile，`inference_backend: test_stub`，并在 README/spec 标明「夹具产物，不可交付」；
        - `rk3568-rga.json`（`cpu` + `rga`）：与 `rk3568-rknn-rga.json` 重复且后端是桩 → 删除，或改成 `test_stub` 并改名 `rk3568-stub-rga.json`；
        - `linux-arm64.example.json`（模板）：显式写 `"inference_backend": "onnx"` 并保留「模板不是发布档位」的注释。
  - [x] **1.0f** 验证：
        ```bash
        make native-build && make native-test
        grep -rn 'cpu_mock' native/src native/sdk native/scripts Makefile        # 期望：空
        ar t build/native/host/libZhulongAlgoRuntime.a | grep -c stub             # 期望：0
        python3 native/scripts/build.py --profile native/profiles/rk3568.json build   # 期望：失败（未声明后端前）
        ```
        ⚠️ 本步之后，**没有 `build/deps/onnxruntime` 的机器无法编 native**——这是有意的 fail-closed；错误信息必须直接给出 `make native-deps`。

      **执行记录（2026-10-25）**
      - 桩已迁到 `native/tests/fixtures/stub_inference_backend.{hpp,cpp}`，类名 `StubInferenceBackend`，标记 `test_stub:`；产品 `.so` 内无桩符号（`nm -C libZhulongAlgoRuntime.so | grep -c StubInference` = 0）。
      - `runtime_factory.cpp` 无默认分支（`#else #error`）；`default_inference_backend_name()` 返回 `onnx|rknn|test_stub|unselected`。
      - `onnx_inference_backend.cpp` 删除 `get_cpu_fallback_backend()` / `is_mock_model_spec()` 及两处委派（`grep -c` = 0）。
      - `build.py` 删除 `default_inference_backend()`，新增 `resolve_inference_backend()`：host 缺省 `onnx`，交叉未声明即 `ValueError`（含可操作文案）。
      - profile：`rk3568.json` → `rk3568-stub.json`（`test_stub`，name 同步）；删除 `rk3568-rga.json`（与 `rk3568-rknn-rga.json` 重复且推理侧是桩）；`linux-arm64.example.json` 显式 `onnx`。
      - 测试：删除全部 `[SKIP] ... cpu_mock` 运行期分支，改为编译期（`ZHULONG_INFERENCE_TEST_STUB`）与 CMake 注册条件；顺带修掉「模型找不到时旧断言会假红」的脆弱点（现在接受 `NOT_SUPPORTED|NOT_FOUND` 且强制 `model == nullptr`）。
      - `algo.hpp` 的 `TARGET_MODEL_EXT` 不再按 `__arm__` 猜测，改由 `ZHULONG_INFERENCE_RKNN` 唯一决定。
      - 验证：`make native-build` + `make native-test` **28/28 通过**（host/onnx）；`test_stub` 独立构建目录 `ZhulongAlgoRuntime`/`AlgoRuntime`/`PlatePlugin`/`FacePlugin` 编译通过且 3 项用例通过；CMake 无后端时 `FATAL_ERROR`；交叉 `rk3568-stub` 与 `rk3568-rknn` configure 均通过；未声明后端的 profile 由 `build.py` 拒绝；`python3 native/scripts/build.py go build ./...` 与 `go test -race ./internal/algorithm ./internal/app` 通过。

- [x] **1.1** 把 `native/CMakeLists.txt:234` 的 `ZhulongAlgoRuntime` 源文件列表抽成 `ZhulongAlgoRuntimeObjects`（OBJECT 库），显式设：
      - `POSITION_INDEPENDENT_CODE ON`（⚠️ 现 target **没有**设，对比 `:154` / `:296` / `:314`；`SHARED` 会自动开、OBJECT 不会，漏了会在链接 PIE 时报 `R_X86_64_32 ... can not be used when making a PIE object`）；
      - ⚠️ **不要**设 `CXX_VISIBILITY_PRESET hidden`（2026-10-25 修正，原文错误）：1.2 让旧 `ZhulongAlgoRuntime`（SHARED）仍从这批对象构建，而 `native/CMakeLists.txt:377` / `:406` 的 plugin 还 `target_link_libraries(... ZhulongAlgoRuntime)`，hidden 会让 `.so` 不再导出那 9 个符号 → plugin **链接期 `undefined reference`**，阶段 1 走不到 1.4。符号不进可执行文件 `.dynsym` 由「可执行文件默认不导出未被动态对象引用的符号」+ 链接侧 `-Wl,--exclude-libs,ALL` 共同满足（`design.md` §4.1.1 第 3 条）。若确实要 hidden，唯一正确做法是加显式导出宏并让 OBJECT 编两遍——等于两份实现，本任务不做。

      **执行记录（2026-10-25）**：见下方「阶段 1 执行记录」。

- [x] **1.1b** 把 `src/platform/dmabuf/dmabuf_allocator.cpp` 收敛为 OBJECT 库的**唯一归属**，`ZhulongAlgoIpc`（`:346`）改为消费 OBJECT 库、不再自行编译它。
      ⚠️ 现在它同时存在于 `ZhulongAlgoRuntime`（`:237`）与 `ZhulongAlgoIpc`（`:346`），靠动态符号介入才合成一个实例；全静态后链接结果取决于扫描顺序，最坏是两份 `DmaBufAllocator` 单例（同类 ABI 债）。见 `design.md` §4.1.1。

      **实现方式与初稿不同（有意）**：`dmabuf_allocator.cpp` 的归属是**独立的** `ZhulongPlatformObjects`（OBJECT 库），`ZhulongAlgoIpc` 以 `target_sources(... $<TARGET_OBJECTS:ZhulongPlatformObjects>)` 把同一批对象**归档**进 `.a`。没有用 `target_link_libraries(ZhulongAlgoIpc PUBLIC ZhulongPlatformObjects)`：CMake 不会把「被链接的 OBJECT 库」的对象装进静态归档，只会传递给消费者；而宿主 CGO 链接行是 `build.py:517-522` 写死的 `engine.a + ipc.a + log.a`，传递性对象永远到不了 Go 链接行 → `DmaBufAllocator` 未定义符号。独立平台对象库的另一个作用：`ZhulongAlgoRuntime`/`ZhulongAlgoRuntimeStatic` 与 `ZhulongAlgoIpc` 三方共享**同一批对象文件**，同一构建树里该翻译单元只编译一次。

- [x] **1.1c** `native/src/algorithm/runtime/tensor.hpp`：`data<T>()` / `data() const` 增加 dtype 校验——`dtype_` 与 `T` 不匹配时抛 `std::invalid_argument`（`design.md` §4.8.5）。当前实现明确不校验（`tensor.hpp:163-175`），而算法侧直接 `data<float>()`；ONNX 后端保留原始 dtype，非浮点输出会被静默读成垃圾。校验失败由 ABI 边界的 `try/catch` 转状态码，不得让异常穿越 C ABI。
      验收：新增单测覆盖 float/int64 互读；`make native-test` 全绿（现有 UINT8 像素读取与 FLOAT32 输出不受影响）。

- [x] **1.2** `ZhulongAlgoRuntime`（SHARED）改为从 `$<TARGET_OBJECTS:ZhulongAlgoRuntimeObjects>` 构建，保持 `SKIP_BUILD_RPATH` / `BUILD_RPATH ""`（`:263` / `:272` / `:289`）与全部 `target_link_libraries` 不变。

- [x] **1.3** 新增 `ZhulongAlgoRuntimeStatic`（STATIC，`OUTPUT_NAME ZhulongAlgoRuntime`，产物 `libZhulongAlgoRuntime.a`），同样从 OBJECT 库取对象；把 onnxruntime / rknn 的依赖以 `PUBLIC` 传递（静态库不「链接」依赖，只能传递给最终消费者）。

- [x] **1.4** 验证：
      ```bash
      make native-build
      make native-test
      ar t build/native/host/libZhulongAlgoRuntime.a | head
      readelf -d build/native/host/libZhulongAlgoRuntime.so | grep -E "NEEDED|RUNPATH"   # 与阶段 0 基线一致
      nm -D --undefined-only build/native/host/libFaceRecognitionPlugin.so | grep -c zhulong  # 仍为 9（变 0 或链接失败 ⇒ 1.1 误设了 hidden）
      # ⚠️ 断言改写（2026-10-25）：原先写的是 `grep -c "DmaBufAllocator::instance"` = 1，
      #    但基线实测该写法就是 4（guard variable / 定义 T / .cold 克隆 / 静态 inst）。
      #    真正要防的是「两份定义」，因此收紧到定义行本身：期望 1。
      nm -C build/native/host/libZhulongAlgoRuntime.so | grep -c "T zhulong::platform::DmaBufAllocator::instance()"
      ```

**Review gate 1**：`make native-test` 全绿 + SO 的 NEEDED/RUNPATH 与基线逐字一致。**rollback = revert 本阶段 commit。**

### 阶段 1 执行记录（2026-10-25）

**目标图（`native/CMakeLists.txt` §4.1）**

```
ZhulongPlatformObjects   OBJECT  src/platform/dmabuf/dmabuf_allocator.cpp
ZhulongAlgoRuntimeObjects OBJECT  cpu_vision_backend / gallery_registry / letterbox_geometry / runtime_factory
                                  + <backend>_inference_backend.cpp（条件编译，只在此处声明）
        │
        ├─ ZhulongAlgoRuntime        SHARED  $<TARGET_OBJECTS:两个 OBJECT 库>
        ├─ ZhulongAlgoRuntimeStatic  STATIC  同上，OUTPUT_NAME ZhulongAlgoRuntime
        └─ ZhulongAlgoIpc            STATIC  $<TARGET_OBJECTS:ZhulongPlatformObjects> + ipc 源
```

- 后端差异只累积到 `ZHULONG_ALGO_RUNTIME_{PRIVATE_LIBS,PUBLIC_DEFS}` 两个变量，最后用一个 `foreach` 施加到四个目标，避免同一份 include/define 设置抄四遍。
- 编译定义（`ZHULONG_INFERENCE_ONNX|RKNN|TEST_STUB`）对 OBJECT 库是 `PRIVATE`（用于编译）、对两个最终产物是 `PUBLIC`（插件侧 `algo.hpp` 的 `TARGET_MODEL_EXT` 依赖它）；`ZHULONG_VISION_RGA` 仍是纯编译期私有定义。
- 后端链接项：SHARED 保持 `PRIVATE`（与阶段 0 行为一致，下游靠 NEEDED 解析）；STATIC 用 `PUBLIC`（静态库无法「链接」，最终链接需求必须交给消费者，对应 1.3）。

**实测结果**

| 断言 | 期望 | 实测 |
| --- | --- | --- |
| `make native-test`（host/onnx） | 28/28 | **28/28 PASS**（25.8s） |
| `ar t libZhulongAlgoRuntime.a` | 含运行时 + 平台对象 | `cpu_vision_backend / gallery_registry / letterbox_geometry / runtime_factory / onnx_inference_backend / dmabuf_allocator` |
| `.so` NEEDED/RUNPATH | 与基线逐字一致 | 完全一致（`libonnxruntime.so.1.21`、`libstdc++.so.6`、`libm.so.6`、`libgcc_s.so.1`、`libc.so.6`），**无 RUNPATH** |
| `nm -D --undefined-only libFaceRecognitionPlugin.so \| grep -c zhulong` | 9 | **9** |
| 同上 plate | 3 | **3** |
| `nm -C .so \| grep -c "T …::DmaBufAllocator::instance()"` | 1 | **1** |
| `dmabuf_allocator.cpp` 编译次数 | 1 | 1（只有 `CMakeFiles/ZhulongPlatformObjects.dir/build.make` 拥有编译规则；`ZhulongAlgoRuntime.dir/link.txt`、`…Static.dir/link.txt`、`ZhulongAlgoIpc.dir/link.txt` 均直接引用同一 `.o`） |
| 交叉 `rk3568-stub` / `rk3568-rknn` 编译 | 通过 | 通过；两档 `.a` 分别是 `stub_inference_backend.cpp.o` / `rknn_inference_backend.cpp.o`，产品档无桩符号 |
| rknn `.so` NEEDED | `librknnrt.so` 且无 RUNPATH | 一致 |
| `build.py go build ./...` | 通过 | 通过（CGO 链接未受归档内容变更影响） |
| `go test -race ./internal/{engine,algorithm,algorunner}` | 全绿 | 全绿（4.2s / 11.0s / 5.6s） |

**1.1c 细节**：`tensor.hpp` 新增 `data_type_name()` 与编译期映射 `tensor_dtype_of<T>`（float/uint8/int8/int32/int64），未特化的 `T` 直接编译失败；`data<T>()` 去掉 `noexcept` 并在 `require_dtype()` 抛 `std::invalid_argument("tensor dtype mismatch: requested X, actual Y")`；`raw_data()` 仍是免检字节视图。单测加在 `native/tests/algo_runtime_test.cpp` 第 1.2 节：非 const 读错类型、const 重载读错类型、int64↔float 双向拒绝，四个方向全覆盖。全仓 `data<T>()` 实例化只有 `float` / `uint8_t` 两种（各 27 / 24 处），无一处的 dtype 与 T 不匹配（`make native-test` 全绿即为证）。

**已知遗留（转交阶段 3/4）**：`dmabuf_allocator.cpp.o` 现在同时存在于 `libZhulongAlgoRuntime.a` 与 `libZhulongAlgoIpc.a`（同一目标文件，归档两份）。普通链接只会拉入一份，但**算法包可执行文件绝不能对这两个归档同时用 `--whole-archive`**，否则回到两份单例。SDK 静态链接的归档清单与 `--exclude-libs,ALL` 一起在阶段 3.9 / 7.2 定稿并验证。

---

## 阶段 2：SDK 头文件收拢（纯增量）

> 目标：`sdk/include` 自足，不再需要 `-I native/src`。

- [x] **2.1** 新建 `native/sdk/include/zhulong/runtime/`，移入 5 个公共头：`runtime_factory.hpp`、`vision_backend.hpp`、`tensor.hpp`、`gallery_registry.hpp`、`inference_backend.hpp`。
      - **不移入**实现细节头：`cpu_inference_backend.hpp`、`cpu_vision_backend.hpp`、`letterbox_geometry.hpp`。
      - 保持头内相对 `#include "tensor.hpp"` 同目录可用。
      - **实际移入 7 个（有意扩大）**：除上述 5 个外，`yuv_sampling.hpp` → `zhulong/runtime/`、`dmabuf_allocator.hpp` → `zhulong/platform/`。理由：二者在生产路径上就被包代码直接 `#include`，不搬则 2.3 的「移除 `-I src`」物理上做不到：
        - `packages/plate_recognition/src/plate_recognizer.cpp` 用 `yuv_sampling.hpp` 的采样数学（`algo-plugin-contract.md` §2 明令「YUV 采样只有一份实现」）；
        - `packages/face_recognition/src/detector.{hpp,cpp}` 用 `DmaBufAllocator` / `DmaBuffer`（`prd.md` F1 已把它们的符号算进 plugin↔host 耦合清单，黑盒后必须由包自身解析）。
        另：`yuv_sampling.hpp` 纯头（只依赖 `<algorithm>/<cmath>/<cstddef>/<cstdint>`）、`dmabuf_allocator.hpp` 纯头（只依赖 std 头），搬进 SDK 不会把 `src` 依赖带进分发物。

- [x] **2.2** 改 `native/sdk/include/zhulong/algo.hpp:21-24` 的 include 为 `<zhulong/runtime/...hpp>`。

- [x] **2.3** 改 `native/CMakeLists.txt:355-386`：`FaceRecognitionPlugin` / `PlateRecognitionPlugin` / `MockDetector` / `MockClassifier` / `MockPersonStructure` 的 `target_include_directories` **移除** `${CMAKE_CURRENT_SOURCE_DIR}/src`（若某处仍需要，说明还有外溢，先修完再移除）。
      > 本阶段是增量改造：包 target 仍暂住 `native/CMakeLists.txt`，到 **3.9** 才整段删除。Review gate 2 的「两个包编译通过」只在本阶段成立。
      ⚠️ **只改这 5 个 target 不够**：`ZhulongAlgoRuntime` / `ZhulongAlgoRuntimeStatic` 的 `target_include_directories` 原本把 `${CMAKE_CURRENT_SOURCE_DIR}/src` 以 `PUBLIC` 暴露，包 target 通过 `target_link_libraries` 又把它继承回来（实测 `FaceRecognitionPlugin.dir/flags.make` 里仍有 `-I…/native/src`）。所以同时把运行库产物的用法要求收敛为 `PUBLIC sdk/include` + `PRIVATE src`。

- [x] **2.4** 改写 `native/src/algorithm/runtime/runtime_factory.hpp:7-10` 的注释：算法包只使用静态链接运行时；若仓库暂留 SHARED target，只限 native 测试与迁移基线，不作为算法包交付形态。
      （文件已随 2.1 移到 `native/sdk/include/zhulong/runtime/runtime_factory.hpp`。）

- [x] **2.5** 验证：
      ```bash
      make native-build && make native-test
      grep -rn "algorithm/runtime" native/sdk/ || echo "OK: sdk 无 src 外溢"
      ```

**Review gate 2**：现有两个包编译通过、`native-test` 全绿、`grep` 为空。**rollback = revert 本阶段 commit。**

### 阶段 2 执行记录（2026-10-25）

**头文件布局（`native/sdk/include/zhulong/`）**

```
plugin.h                         纯 C ABI
algo.hpp                         C++17 门面（include 改为 <zhulong/runtime/...>）
runtime/runtime_factory.hpp      ← 移动
runtime/vision_backend.hpp       ← 移动
runtime/tensor.hpp               ← 移动
runtime/gallery_registry.hpp     ← 移动
runtime/inference_backend.hpp    ← 移动
runtime/yuv_sampling.hpp         ← 移动（外溢：plate_recognizer.cpp 生产路径依赖）
platform/dmabuf_allocator.hpp    ← 移动（外溢：face detector.{hpp,cpp} 生产路径依赖）
```

留在 `native/src/algorithm/runtime/` 的只剩实现细节：`cpu_vision_backend.{hpp,cpp}`、`letterbox_geometry.{hpp,cpp}`、`*_inference_backend.cpp`、`runtime_factory.cpp`、`gallery_registry.cpp`。头内同目录相对 include（`"tensor.hpp"` 等）原样保留。

**连带改动**：`native/src/**` 与 `native/tests/**` 全部 26 处 include 改为 `<zhulong/runtime/...>` / `<zhulong/platform/dmabuf_allocator.hpp>`；`frame_broker.hpp`、`dmabuf_allocator.cpp`、`cpu_vision_backend.{hpp,cpp}`、`runtime_factory.cpp` 同步。`.trellis/spec/native/algo-plugin-contract.md` 新增 §3.0「公共头布局」并把 YUV 采样路径指到新位置。

**实测**

| 断言 | 结果 |
| --- | --- |
| `make native-build` + `make native-test` | 28/28 PASS（25.7s） |
| `grep -rn "algorithm/runtime" native/sdk/` | 空 |
| `grep -rn '#include <\(algorithm\|platform\|common\)/' packages` | 空（原先 plate_recognizer / face detector 各 1 处） |
| 包 target 的 `-I`（`flags.make`） | `FaceRecognitionPlugin` = `sdk/include` + 包自身；`PlateRecognitionPlugin` 同；3 个 mock = 只有 `sdk/include` |
| 交叉 `rk3568-stub` / `rk3568-rknn` | 编译通过 |
| `build.py go build ./...` / `make go-check` | 通过 |

---

## 阶段 3：SDK main 骨架 + plate/face 算法包可执行文件

> 本阶段不动宿主。产出物先手工验证。

- [ ] **3.0e** 包源码按 target 拆目录（R11；**取代原「输入契约下移到后端」**）：
      - 把 `packages/<id>/src/` 拆成 `packages/<id>/<target>/src/`：`face_recognition` 拆 `onnx/` 与 `rk3568/` 两份，`plate_recognition` 只建 `onnx/`（**不要**为 `rk3568`/`rk3588` 建空目录，见 target 资产门禁）；
      - 每份实现内**删除** `#if defined(__arm__) || defined(__aarch64__)` 与 `targets/<t>/` 路径候选（`face_recognition.cpp:26-40`、`plate_recognition.cpp:79-82`），模型只在包根 `models/`，找不到即失败；
      - 每份实现内**删除**跨平台判断：`is_onnx_` / `set_is_onnx` / `!is_onnx_ && dma_buf_fd>=0` 全部消失——`onnx/` 实现只有 ONNX 一条路径，`rk3568/` 实现只有 RKNN 一条路径，没有分支可写；
      - 归一化常量与默认阈值保留在各自实现内（face det 640 `127.5/128`、extractor 112 `127.5/127.5`、plate recognizer `(x/255-0.588)/0.193`），**每处常量只允许一个定义点**，禁止在同实现内重复内联；
      - 新增 **dev-only 开发期测试**（取代原「共享 golden 向量」）：
        - `packages/<id>/<target>/tests/dev_infer.cpp`：用 `stb_image` 读**包根 `sample.jpg`** → 调本 target 产品代码 → 归一化坐标 × 宽高 → 画框 + label/score + landmark 连线 → `stb_image_write` 写 **`result.jpg`** → 终端打一张明细表（sample / count / label / score / box / text / cost_ms）；
        - CMake：`add_executable(<id>_devtest EXCLUDE_FROM_ALL tests/dev_infer.cpp src/*.cpp)` + `target_link_libraries(... Zhulong::AlgoRuntime)`；**不设 INSTALL_RPATH、不进安装规则** → 物理上进不了归档；
        - 依赖：`stb_image.h` / `stb_image_write.h` 放 `tests/third_party/`（header-only，零动态依赖，也可改用系统 libjpeg——开发期工具不受 `ldd` 约束）；
        - `make test` = 构建 `devtest` + 跑 `sample.jpg` + 断 `result.jpg` 存在；`result.jpg` 写到构建目录（`build/<target>/out/`），**不写安装目录**；
        - 文件名带身份：`result-<id>-<target>.jpg`，避免 onnx 与 rk3568 互相覆盖，也便于板端回传时引用；
        - **交叉构建的 `devtest` 允许在板端执行**（宿主不行）：这是 RKNN 档位唯一的行为验收方式，回传 `result.jpg` 并记入 `research/baseline.md`；
        - 不要做的事：不要用 ctest 做像素级 diff（字体/渲染顺序一改就炸），也不要把量化的期望值写进用例——看图判断，跨 target 不比结果。
      - 验收：
        ```bash
        grep -rn '\.\./onnx\|\.\./rk3568\|targets/' packages/*/*/src            # 期望：空
        grep -rn 'is_onnx_\|set_is_onnx\|__aarch64__\|__arm__' packages/*/*/src   # 期望：空
        make -C packages/face_recognition/onnx test && make -C packages/face_recognition/rk3568 test
        ls packages/*/*/build/*/out/result-*-*.jpg                                # 期望：每 target 一张
        find packages -path '*vectors*'                                            # 期望：空（向量机制已撤销）
        ```
      - ⚠️ **不引入** `InputFacts` / `Session::prepare_input` / `models/preprocess.json`（R13 已撤销）。`prepare_input` 的复用槽只作为独立性能任务，不在本阶段。
      - ⚠️ 两边 `make test` 在交叉 profile 下只能编译（AGENTS.md），RKNN 侧的行为验收在板端，`research/baseline.md` 标「未测」。

- [x] **3.0f** target 命名迁移：`cpu` → `onnx`（`prd.md` F25；**先做这一步再拆目录**，否则目录名会把错误叫法固化）：
      - 资产：`packages/*/targets/cpu/` → `packages/*/targets/onnx/`；`manifest.platform.target` `"cpu"` → `"onnx"`（`arch` 仍是 `x86_64`）；
      - 归档名随之变成 `<id>-x86_64-onnx.tar.gz`（`pack_package.py` 只读 manifest，无需硬编码）；
      - Go 侧默认值：`config.toml` 的 `[algorithm].target`、`internal/app/config.go` 的 `AlgorithmTarget` 默认值都改为 `"onnx"`；`internal/algorithm/verifier.go:100-105` 的 `normalizeHostTarget("")` 返回 `"onnx"`，注释同步（原文「cpu 或加速器家族」改为「onnx 或加速器家族」）；
      - 安装期门禁：`manifest.platform.target == "cpu"` → 稳定错误码 `PLATFORM_TARGET_CPU_RETIRED`（`internal/algorithm/verifier.go` + `internal/httputil/locale.go` 文案），提示重建为 `onnx` 档位；**不做静默映射**；
      - 验收：
        ```bash
        grep -rn '"cpu"' packages/*/*/manifest.json internal/app/config.go config.toml   # 期望：空
        go test -race ./internal/algorithm ./internal/app                                # 含 HostPlatform/normalize 用例
        ```
      - ⚠️ 这一步之后 `cpu` 在仓库里只剩两个含义：`ZHULONG_INFERENCE_BACKEND=cpu`（`cpu_mock` 桩，测试用）与视觉后端的 CPU 实现。任何新增 `cpu` 档位都视为命名回退。

      **执行记录（2026-10-26）**

      - **资产**：`git mv packages/face_recognition/targets/cpu → …/targets/onnx`、`packages/plate_recognition/targets/cpu → …/targets/onnx`；两份 `manifest.json` 的 `platform.target` 改为 `onnx`（`arch` 仍是 `x86_64`）。`rk3568` 档位资产不动。
      - **归档名**：`pack_package` 只读 manifest，重建后自动变成 `face_recognition-x86_64-onnx.tar.gz` / `plate_recognition-x86_64-onnx.tar.gz`（已实测产出）。
      - **Go 默认值**：`internal/app/config.go` `AlgorithmTarget` 与仓库 `config.toml` 均改为 `"onnx"`；`verifier.normalizeHostTarget("")` 返回 `"onnx"`，`HostPlatform.Target` / `NewVerifier` 注释同步。
      - **退役门禁**：新增哨兵 `ErrPlatformTargetRetired`，在 `VerifyDirectory` 的静态平台门禁**最前面**判断（早于 arch/target 比对，避免被 `ErrPlatformMismatch` 吞掉），错误串指出「按 onnx 档位重建」；`normalizeInstallError` 映射为 HTTP 400 + 稳定码 `PLATFORM_TARGET_CPU_RETIRED`。文案注册在 **`internal/algorithm/locale.go`**（不是计划里写的 `internal/httputil/locale.go`——通用码在 httputil，算法域码一直是本包注册），并加进 `error_codes_test.go` 的 `algorithmErrorCodes` 本地化清单。
      - **`cpu` 字面量的其余位置（都改了）**：两个 `build_package.py`（`--target` 默认值与 choices、host 构建目录判定、`--verify` 条件）、`scripts/test_infer.py` 的模型默认路径、两个包源码的 `targets/cpu/` 模型目录候选、`native/src/abi/engine.cpp` 的插件搜索目录、`native/tests/face_recognition_onnx_test.cpp` 的模型目录、`README.md` 配置示例、`.trellis/spec/backend/algorithm-guidelines.md` 的 manifest 示例与 `platform.target` 枚举说明（并写明 `cpu` 已退役、不做静默映射）。
      - **测试夹具**：12 个测试文件里的 `Target: "cpu"` / `TargetDevice: "cpu"` / `"target":"cpu"` / `--target cpu` / `x86_64-cpu.tar.gz` 一并改为 `onnx`（唯一保留的 `Target: "cpu"` 是新增的退役门禁用例）。新增 `TestVerifierRejectsRetiredCPUTarget` 与 `internal/algorithm/error_mapping_test.go`（断言码与 400，且 `ALGO_PLATFORM_MISMATCH` 不被合并）。
      - ⚠️ **验收 grep 的路径写错了**（原稿 `packages/*/*/manifest.json`）：manifest 在 `packages/<id>/targets/<target>/manifest.json`，glob 应写成 `packages/*/targets/*/manifest.json`，否则断言恒真（`No such file` 也算「空」）。
      - ⚠️ **陈旧归档会掩盖重建**：`build/packages/face_recognition.tar.gz`（10-22 用 cpu 档位打的）会被 `findFaceRecognitionArchive` 的兜底候选先命中，测试于是拿旧包去验，报 `PLATFORM_TARGET_CPU_RETIRED`——这正好证明门禁生效。重建 `--target onnx` 后通过；排队顺序里 onnx 归档优先于无后缀候选。
      - **验证**：`grep -rn '"cpu"' packages/*/targets/*/manifest.json internal/app/config.go config.toml` 为空；`make native-test` 28/28；`python3 native/scripts/build.py go test -race ./internal/algorithm`、`./internal/app` 通过（⚠️ 这两个包依赖 engine，**不能**裸跑 `go test`：直接跑会因缺 `CGO_LDFLAGS` 报一片 `undefined reference to Zhulong_*`）；`make go-check` 30 个包全绿。

- [ ] **3.1** 从 `native/src/algorithm/runner/runner.cpp` 抽出 broker 循环到 `native/sdk/src/algo_main.cpp` + `native/sdk/include/zhulong/algo_main.hpp`。
      ⚠️ **逐字保留**仍适用于新可执行入口的 stage token：`connect`、`name_check`、`shm_init`、`gallery_sync`、`model_create`、`worker_create`、`task_open`、`process`、`broker_disconnected`、`args`。旧 `dlopen` stage 不再由算法包 runner 发出；进程 `exec` 失败由 Go spawn 层记录，不作为 runner stage。对照 `internal/algorunner/process.go:432` 的 allowlist。
      ⚠️ **worker 循环必须留出帧边界控制消息的位置**（`design.md` §10.8）：
      ```cpp
      while (client.next_frame(&frame)) {
          drain_control_messages(task_context);  // 本任务为空实现：MSG_TASK_CONFIG 尚未定义
          auto result = algo.process(session, frame, task_context);
          client.publish(result);
      }
      ```
      每个 Worker 只关联一个 broker 和一个 `TaskContext`。

- [ ] **3.2** `Options` 支持重复 `--broker`、`--name`、`--algorithm-id`、`--verify --sample`。固定一 broker 一 Worker 线程，所有传入 broker 都须订阅；不接受或传递 `--max-workers`（该参数在现状 runner 中并不存在，此约束意为「不得新增」），不做线程 clamp。**不得**新增 `--config-file` 或任何 argv 配置通道；`model_create` 继续传 `"{}"`，与现状逐字一致。
      - **`--verify` 必须输出明细，不能只给 `count`**：复用现有的 `runner::serialize_inference_result`（`runner.cpp:115`，已在 IPC 路径 `:867` 用着），把 `task_type` + `detections[{box,label,score}]` / `classifications[{label,score}]` / `persons[{track_id,body_box,body_score,faces[{box,quality,landmarks,matched,similarity,person_id,person_name,candidates}]}]` / `vehicles[{track_id,vehicle_box,vehicle_score,vehicle_type,vehicle_color,plates[{box,score,plate_number,plate_color,plate_type}]}]` 一并输出。
        - **定位：`--verify` 的输出是 IPC 结果载荷的「超集」**（多出 `status` / `sample_decoded` / `width` / `height`），不是另一套格式。三个消费者共用同一份载荷形状：broker 长驻路径、一次性推理（验包 / 将来的宿主侧「试用」）、`algo_probe.py`。**禁止**为某个消费者另起字段名；
        - **字段只允许追加，不允许改名或改语义**（契约在 `algo-plugin-contract.md`，见 7.4）。改名前先问：宿主的三个消费者能不能同时改？不能就别改；
        - **明细必须可选**：`internal/algorithm.VerificationReport` 用指针/slice 接收（缺字段即 nil）。新宿主读旧包 → 跳过明细比对并打一条 warn，**不得判失败**——否则升级顺序被钉死；
        - **不搬 IPC 专属字段**：`stream_id` / `pts` / `capture_time_ms` 在验包场景无意义，不输出（或恒 0）；`cost_ms` 保留，用于时延观测；
        - `count` 与载荷数组并存（`count` = 载荷元素数），保持旧解析器的向后兼容；
        - 归一化框坐标 + `width`/`height` 已足够外部工具（Go / 前端 canvas / `algo_probe.py`）画回原图，**不需要包内任何图像库**；
        - ⚠️ **`sample` 字段语义澄清**：Go 侧 `verifier.go:301` 会把 `--sample` 换成解码后的**临时 PPM 路径**，所以该字段报的是 PPM 而非 `sample.jpg`。渲染方要用原始 `sample.jpg` 作底图（内容相同、尺寸相同），不要拿这个路径去显示/引用；
      - **一次性推理的语义边界（写进输出与文档，别留给用户猜）**：不连 `--broker` → 收不到 `MSG_GALLERY_SYNC`，因此 `matched` 恒 false、`person_name` / `candidates` 恒空；无连续帧 → `track_id` 恒 0。验包与将来的 UI 试用都受此限，输出里不要伪造这两类字段。

- [ ] **3.2b SDK 可安装：`make sdk-install`**（方案 A 的前提，`design.md` §4.5）
      - 新增 `ZhulongAlgoSdkStatic`（STATIC，`OUTPUT_NAME zhulong_algo_sdk`）：源集合 = `native/sdk/src/algo_main.cpp` + `native/src/algorithm/ipc/*` + `gallery_index/gallery_parser` 非 inline 部分 + `src/common/log.cpp`（`design.md` §4.4；**最容易漏 log.cpp**）；
      - install 规则：`include/zhulong/**`、`lib/{libzhulong_algo_sdk.a,libZhulongAlgoRuntime.a}`，导出 `ZhulongAlgoSDKConfig.cmake`，其中定义两个 imported target `Zhulong::AlgoSDK`（在前）与 `Zhulong::AlgoRuntime`（在后）并固定链接顺序；
      - **config 必须可搬迁**（`design.md` §4.5.0）：`IMPORTED_LOCATION` 用 `${CMAKE_CURRENT_LIST_DIR}` 推算 prefix；第三方依赖用 `find_library(... HINTS ${ONNXRUNTIME_ROOT}/lib)` 在使用端解析，不得写入构建机绝对路径；缺库时报可操作错误；
      - 产物落在 `build/sdk/prefix/`（**不安装到系统路径**，不联网）；
      - 额外产出 `VERSION` 与 `ZhulongAlgoSDKConfigVersion.cmake`（`write_basic_package_version_file`），与 `abi.json` 的 `api_version`/`protocol` 同源生成（`design.md` §4.5.2）；
      - **包目录内不得出现 SDK 头/库副本**：一律通过 prefix 引用，发布物里不含任何 SDK 文件（`design.md` §4.5.1）；
      - 验收：`cmake -B /tmp/probe -S <一个临时最小工程> -DCMAKE_PREFIX_PATH=build/sdk/prefix` 能 `find_package(ZhulongAlgoSDK 1.0 REQUIRED)` 成功，且 `ar t` 能看到 `algo_main` 与 `log` 的目标文件；把 prefix 整体拷到 `/tmp/other-prefix` 后同一命令仍能 configure（可搬迁性）。

- [ ] **3.2c SDK 模板 + 脚手架**（3.3/3.6 的蓝本，`design.md` §4.7）：
      - 建 `native/sdk/templates/`：`CMakeLists.txt`（`find_package(ZhulongAlgoSDK)` + `add_executable(<id>)`）、`Makefile`（`make` / `make test` / `make package` / `make verify`）、`manifest.json`（`id`/`name`/`version`/`task_type`/`category`/`platform{arch,target}`/`resources{compute_cost,memory_mb}`）、`config.schema.json`（`type:object, properties:{}`）、`src/<id>.cpp`（`ZHULONG_ALGO_MAIN` + 空 `init`/`create_session`/`detect`）、`tests/dev_infer.cpp`（读 `sample.jpg` → 空结果 → 写 `result.jpg`，证明「空实现 = 0 检出」而不是「跑不起来」）+ `tests/third_party/stb_image*.h`、`scripts/build_package.py`（薄壳）、`README.md`、`sample.jpg` 占位图；
      - 建 `native/sdk/tools/new_package.py`：参数 `--dir` / `--target` / `--task-type` / `--category` / `--version` / `--sdk-min-version` / `--force`；生成物落在 `<dir>/<id>/<target>/`（每 target 一个实现目录，对已存在的 `<id>` 再跑一次只新增 target 目录，不覆盖兄弟目录）；`id` 校验**复用** `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` 并显式拒绝 `plugin.so`（错误信息指向宿主规则，不让用户到安装期才发现）；
      - 生成的包**不拷 SDK**：`README.md` 写清「SDK 是构建期依赖，用 `SDK_PREFIX` 指向已安装的 SDK；发布物里没有 SDK」；
      - 根 `Makefile` 新增 `new-package PKG=<id> TARGET=<t>`（薄封装，落到 `packages/<id>/<t>/`）与 `sdk-selftest`（临时目录生成 → `make` → `make verify` → `make package`）；`sdk-selftest` 进 `make check`（仅在 host profile 下运行，交叉 profile 下 SKIP，避免执行目标架构产物）；
      - 验收：生成到 `mktemp -d` 后 `cd demo/onnx && make && make test && make verify && make package` 全绿，`verify` 为 `{"status":"passed","count":0}`，归档内无 `plugin.so`。

- [ ] **3.3** `packages/plate_recognition/onnx/`（**该 target 自己的构建定义**，两个文件）：
      - `CMakeLists.txt`：只依赖 `find_package(ZhulongAlgoSDK 1.0 REQUIRED)`，禁止 `if(TARGET ...)` 双分支、禁止引用 `../native` 源码路径、禁止引用 `../rk3568` 等同 id 另一 target 目录；
      - `add_executable(plate_recognition ...)`，`OUTPUT_NAME` 必须等于 `manifest.id`（包根入口名 = id，见 R1）；链接 `Zhulong::AlgoSDK Zhulong::AlgoRuntime`，加 `-Wl,--exclude-libs,ALL`；不得链接任何 SHARED 形态的 runtime；
      - 四个入口 `make` / `make test` / `make verify` / `make package`，**在该目录内跑完整生命周期**，不需要回仓库根；`TARGET` 由目录名决定（`onnx`），不作为 Makefile 差异；
      - **第三方运行时（`onnx` 档位）**：`INSTALL_RPATH "$ORIGIN/lib"`；`make package` 时由 `pack_package.py --bundle-runtime <ort-lib-dir>` 把 ORT 闭包拷入 `lib/` 并附 `NOTICE`（`design.md` §4.5.4）；将来做 rknn target 时用 `--no-bundle-runtime` + 声明 BSP 依赖 + 启动探测；
      - 目录形态先用脚手架生成再替换源码：`make new-package PKG=plate_recognition TARGET=onnx` 后把已有 `src/*.cpp` 搬入（模板本身不含业务代码）；
      - **不得新增平台分叉**：`onnx/` 实现里不得出现 `ZHULONG_INFERENCE_*` / `ZHULONG_VISION_*` 宏、`is_onnx_` 判断、`targets/` 路径候选或 `__aarch64__` 兜底（见 3.0e）；
      - `src/plate_recognition.cpp` 用 `ZHULONG_ALGO_MAIN(...)`；
      - **`rk3568/` / `rk3588/` 目录不创建**：plate 目前只有 `onnx` 资产（F23）。`scripts/build_package.py --target rk3568` 必须明确失败并列出缺失的 `manifest.json` / `models/`，不得静默回落到 `onnx` 资产。

- [ ] **3.4** 手工验收（AC1 + AC7 的一半）：
      ```bash
      make native-build && make sdk-install && make packages-build
      PLATE_BIN=build/packages/plate_recognition/plate_recognition
      nm -D --undefined-only $PLATE_BIN | grep -c zhulong        # 期望 0
      ldd $PLATE_BIN | grep -ci zhulong                          # 期望 0
      nm -D $PLATE_BIN | grep -c zhulong                         # 期望 0（导出表也不许泄，见 design §4.1.1）
      mkdir -p /tmp/pkgroot && cp $PLATE_BIN /tmp/pkgroot/plate_recognition \
        && cp -r packages/plate_recognition/targets/onnx/models /tmp/pkgroot/ \
        && cp packages/plate_recognition/sample.jpg /tmp/pkgroot/
      (cd /tmp/pkgroot && ./plate_recognition --verify --sample sample.jpg)
      # 期望 exit 0 + {"status":"passed","count":<与阶段 0.3 基线一致>,"detections":[...]}
      # ⚠️ 必须真的校验 count 与 detections，不能只看 status：dladdr 找不到 models/ 时若后端允许合成帧，会“通过”但什么都没算。
      ```
      > 为什么要拷到 `/tmp/pkgroot`：两个包的模型定位都靠 `dladdr` 取模块所在目录再拼 `models/`（`plate_recognition.cpp:32-53`）。可直接在 build 目录里跑会命中源码树候选，**掩盖包根约束缺陷**。

- [ ] **3.5** 手工连真实 broker（AC3 的一半）：
      ```bash
      # 宿主在跑并已给 plate 算法分配任务时
      $PLATE_BIN --broker <实际 socket 路径> --algorithm-id plate_recognition --name 车牌识别
      # 期望：stdout 打印 connect/shm_init/gallery_sync 阶段日志，无 broker_disconnected 循环空转
      # 用 SDK Worker 日志/线程标记确认 Worker 数 = 成功订阅的 broker 数；ps -L / top -H 仅验证线程实际存在（不把辅助线程算作 Worker）
      ```

- [ ] **3.6** `packages/face_recognition/{onnx,rk3568}/` 同构：各自 `CMakeLists.txt` + `Makefile`（两者与 plate 只差源文件列表与 `ZHULONG_ALGO_MAIN(...)`）；只链接 `Zhulong::AlgoSDK` / `Zhulong::AlgoRuntime`。
      - `onnx/` 侧：`models/*.onnx`，输入 NCHW f32、实现内自己归一化、host 内存；
      - `rk3568/` 侧：`models/*.rknn`，输入 NHWC u8 + `pass_through=0`（converter 归一化）+ DMA-BUF，阈值独立标定；
      - 两份实现**不共享任何源码、也不共享测试夹具**：各自 `tests/dev_infer.cpp` + `tests/third_party/stb_*`，各自出 `result-<id>-<target>.jpg`；阈值标定就靠改常量→重出图→看漏检/误检；
      目录内闭环验收（**不回仓库根**）：
      ```bash
      make -C packages/face_recognition/onnx    all test package verify
      make -C packages/face_recognition/rk3568  all test package CROSS_PROFILE=rk3568   # 只编译，不 verify；devtest 拷到板端跑出图
      ```
      并校验各 target 的 Makefile 与 SDK 模板只差一行：
      ```bash
      diff native/sdk/templates/Makefile packages/*/*/Makefile | grep -v SDK_PREFIX   # 期望：空
      ```

- [ ] **3.7** 打包实现收编为**一份**（`design.md` §4.6）：
      - 新增 `native/sdk/tools/pack_package.py`（唯一实现）：读 `manifest.json` 得 `<id>`/`<version>`/`arch`/`target`，把 `--build-dir/<id>` 拷到 staging **包根** 并置 `0755`，**摊平**拷入该 target 目录下的 `manifest.json`、`config.schema.json`、`sample.jpg`、`models/`、`QUANTIZATION.md`、`CONVERSION.md`（归档内不得出现 `targets/`，见 3.7 资产摊平），默认运行 `<id> --verify --sample sample.jpg`（`--no-verify` 可关），产出到 `--out-dir`（默认 `dist/`）命名为 `<id>-<arch>-<target>.tar.gz`；
      - **调用方是包自己的 Makefile**（`make package`），不是根 Makefile；根 Makefile 只逐包 `$(MAKE) -C` 并将归档聚合到 `build/packages/`（保留 Go 测试既有路径）；
      - **约束**：不 import 本仓库模块、不读 `native/`——它要能在干净容器里单独跑（AC7）；
      - **版本门禁**：模板 `Makefile` 声明 `SDK_MIN_VERSION`，configure 前读 `$(SDK_PREFIX)/VERSION` 校验，不匹配时报「需要 ≥ X，当前 Y」并退出；禁止跳过该校验去“先编一下试试”；
      - 两个 `packages/<id>/scripts/build_package.py` 改为 **10 行薄壳**（保留现有 Go 测试的调用路径），不再包含 staging/验包/归档逻辑；
      - **第三方运行时绑定**（`design.md` §4.5.4）：`--bundle-runtime <dir>` 把 ORT 闭包与 `NOTICE` 拷到 `lib/`，输出打印 `runtime: bundled|system (<lib> @ <path>)`；`--no-bundle-runtime` 用于 rknn target；缺 `NOTICE` 拒绝出包；
      - **target 一致性**：归档内的模型扩展名必须与 `--target` 匹配（`onnx` → `.onnx`，`rk3568` → `.rknn`），混入即失败；
      - **target 资产门禁（fail-closed）**：入参必须是 `packages/<id>/<target>/` 目录；缺 `manifest.json`、缺该 target 应有的模型、含 `.rknn` 却缺 `QUANTIZATION.md`、缺 `CONVERSION.md`（或转换脚本）→ 立即失败并列出缺失项；`--target` 取值收敛为「目录实际存在」；
      - **量化元信息**（`design.md` §4.8.5 → 已由 R12 收窄）：`QUANTIZATION.md` 逐模型写精度模式 + 校准集 + 工具版本；`CONVERSION.md` 是 `mean/std` 的唯一来源，`.rknn` 内嵌 `attrs` 只用于 drift 检查（不一致时报错退出，**不用它生成任何配置**）；
      - **资产摊平**：把 `<target>/models/`、`manifest.json`、`config.schema.json`、`QUANTIZATION.md` 落到 staging **包根**；归档内不得出现 `targets/` 路径（`tar tzf | grep -c 'targets/'` 期望 0）；
      - 检查项：归档里不得出现 `plugin.so`（任何位置）；入口文件名为 `<manifest.id>` 且 `0755`；manifest 原样复制（本任务**不新增任何字段**）；**归档内不得出现 `tests/`、`result*.jpg`、`stb_image*`**（开发期产物零泄漏，一条 `tar tzf | grep -cE` 拦住）。

- [ ] **3.8** SDK main 增加 `--extract-feature`（协议与弱符号方案见 `design.md` §11.2/§11.3）：
      - stdin 帧流（28 字节头 + 裸像元）→ stdout 逐帧一行 JSON；日志只能走 stderr；
      - 弱引用 `zhulong_face_extract_feature`：face 包自然解析，plate 包报 `UNSUPPORTED`（exit 5，不是构建错误）；
      - 帧级错误（无脸/输入非法）**不退出进程**；`request_id` 必须原样回显；
      - **不新增公共门面**、不改 `plugin.h`、不引入图像解码依赖（像素在 Go 侧已展平）；
      - 禁止为目标添加 `-rdynamic` / `--export-dynamic`（会使 AC1 第三条失效）。
      - ⚠️ **弱符号必须由包自身对象文件提供（T1，2026-10-25 补充）**：GNU ld **不会**为满足「弱未定义引用」抽取静态归档成员。`zhulong_face_extract_feature` 的定义必须位于 `add_executable` 直接编译的源码里（`packages/face_recognition/src/face_recognition.cpp:570`）；若被移进 `libZhulongAlgoRuntime.a`，face 包会**静默**返回 `UNSUPPORTED`（exit 5），表现为「底库登记功能消失」且无错误日志。
        门禁：用 face 包喂 1 帧，必须**不**返回 `UNSUPPORTED`（与 plate 包的 `UNSUPPORTED` 用例互为对照，两条都要有）。
      手工验收（一次性执行，**不落成仓库测试资产**；帧协议是 SDK 公开契约，验证工具属于 SDK 分发物，见下）：
      ```bash
      make native-build
      # 1 帧成功 + 1 帧无脸：应输出 2 行 JSON、进程不退出、退出码 0
      python3 - "build/packages/face_recognition/face_recognition" <<'EOF'
      import struct, subprocess, sys
      # 组帧（28 字节头 + RGB24）并喂 stdin，逐行打印 stdout
      ...  # 现场写 20 行即可，不入库
      EOF
      # plate 包不支持该能力：exit 5 + code=UNSUPPORTED
      # （同样用一个内联片段喂 1 帧）
      nm -D build/packages/face_recognition/face_recognition | grep -c zhulong   # 期望 0
      ```
      > ⚠️ **不要把协议验证脚本放进 `native/tests/`**。`native/tests/` 是随 `make native-test` 跑的仓库内测试资产，而帧协议是第三方也要实现的 SDK 公开契约——那样会同时造成「第三方拿不到工具」与「仓库又多一处协议知识」。长期回归的位置只有两处：
      > 1. `internal/faceextract` 的 Go 单测，用 **stub 可执行文件**（不依赖真实包，见 4.8）；
      > 2. SDK 分发物自带的 `tools/algo_probe.py`（阶段 7.1 产出，第三方与仓库共用同一份）。

- [ ] **3.9** 移除宿主构建树里的包定义，包级测试随包走（AC16）：
      - `native/CMakeLists.txt`：删除 `FaceRecognitionPlugin` / `PlateRecognitionPlugin` 的 `add_library`、`target_include_directories`、`target_link_libraries` 段落；
      - 把 `native/tests/face_recognition_plugin_test.cpp`、`plate_recognition_plugin_test.cpp`、`face_recognition_onnx_test.cpp` 迁到对应 target 目录（`packages/face_recognition/<target>/tests/`、`packages/plate_recognition/onnx/tests/`），**不得**再 `#include` `native/src`；迁完后它们以 `devtest` 的可选子用例形式存在（或直接并入 `dev_infer.cpp` 的断言），由 `make packages-test` 驱动；不能迁的部分（依赖旧 `.so` 形态的断言）改写为 `dev_infer.cpp` 的检查项，不要为了保留旧断言而把 `.so` 形态抬成受支持路径；
      - `ZhulongFaceFeatureExtract` / `ZhulongFacePluginTtl`：前者随 R10 删除（改由 Go 侧覆盖），后者改测新的 `--extract-feature` 进程（用 mock 可执行文件，不依赖真实包）；`MockDetector` / `MockClassifier` / `MockPersonStructure` / `MockFaceExtractPlugin` 属夹具，留在 native；
      - `Makefile`：新增四个**根目录入口**——`make`（默认，产物齐全）/ `make test`（全量测试）/ `make package`（聚合各 target 的归档）/ `make check`（发布门禁）；`packages-*` 内部分解目标**只做委派**（`$(MAKE) -C packages/<id>/<target>`，target 列表来自目录枚举），不得重写编译或打包逻辑（`design.md` §4.5）；
      - `go-check: native-build packages-build`（`internal/algorithm/*_pkg_test.go` 依赖包产物，否则只能 skip）；`packages-package` 末尾把 `packages/*/dist/*.tar.gz` 聚合到 `build/packages/`；
      - `CROSS_PROFILE` 同时驱动 native 与 packages，输出 `build/targets/<name>/{native,packages/<id>}`，只编译不测试，且把 `TOOLCHAIN` 传给包（包 Makefile 据此自动 `--no-verify`）；
      - 新增两个便捷入口（`design.md` §4.8.3）：`make cross CROSS_PROFILE=<p>`（只构宿主到 `build/targets/<p>/`）与 `make package-all TARGETS="onnx rk3568"`（逐 target 构建并出归档）；交叉验收只用静态检查（`file` 架构 / `readelf NEEDED` / 无 zhulong 符号 / 归档无 `.onnx` 混入），**不得**在宿主执行目标架构二进制，也不得用 qemu 冒充；
      - 验收命令简化成三条：
        ```bash
        make            # 编完宿主 + 两个包
        make test       # 全量测试
        make package    # 出归档（默认验包）
        ```
      - `build_contract_test.py` 的 Host/Cross 约束扩展到包 prefix（禁止 Host 头/归档被交叉包使用）。
      验收：
      ```bash
      make native-build && find build/native/host -name '*Recognition*'   # 期望：空
      grep -rn 'packages/' native/CMakeLists.txt                          # 期望：空
      make packages-build && make packages-test
      ```

7f. 把 dev-only 测试（`tests/` / `result.jpg` / `stb_image*`）装进归档，或把图像库链进交付物 `<manifest.id>` → 撞 AC12/AC18 的 `ldd` 门禁；也没必要：只有开发期看图才需要图像库，交付形态只吃宿主喂的裸像素。

**Review gate 3**：`make native-build` 不再产出任何 `*Recognition*`；两个包仅靠 `build/sdk/prefix` 构建成功（`grep packages/ native/CMakeLists.txt` 为空）；plate 验包 `count` 与阶段 0.3 基线一致；**每个 target 的 `make test` 都产出 `result-<id>-<target>.jpg` 与终端明细表，且归档内无 `tests/`**；两者 `nm` / `ldd` / 导出表三项黑盒检查通过；真实 broker 上 Worker 数与订阅数一致；`--extract-feature` 帧流三项（多帧/无脸续跑/plate 报 UNSUPPORTED）通过，且特征向量与阶段 0.4 基线余弦相似度 ≥ 0.999。

**本阶段的 `make check` 预期状态（T3，2026-10-25 补充）**：3.9 把三个 native 用例（`ZhulongFaceRecognitionPlugin` / `ZhulongFaceRecognitionOnnx` / `ZhulongFaceFeatureExtract`）迁走前，它们仍链接旧 `FaceRecognitionPlugin` / `PlateRecognitionPlugin`，因此本阶段 `make native-test` **允许短暂为红**，但必须把红项**逐条列在本阶段的 PR 描述里**，每一条对应到 3.9 或 4.8 的替代用例；`make check` 的最终口径以 gate 7 为准。不允许「红了不记」——那是后续无人收口的唯一来源。

**rollback = 删除新增 SDK main、SDK install 规则及两个包目录下的 CMakeLists。**

---

## 阶段 4：宿主仅启动包根可执行文件（manifest 零改动）

- [ ] **4.0** `internal/algorunner/runtime.go` 新增 `ResolveEntry(pkgDir, id string) (path string, err error)`，只解析一种形态：
      - 校验 `id` 符合安全 token 且不等于 `plugin.so`；
      - `<pkgDir>/<id>` 必须是由 `Lstat` 确认的普通文件，拒绝目录与符号链接；
      - 包根存在 `plugin.so` 时返回明确的旧形态不支持错误；否则入口缺失返回 `ErrMissingEntry`；
      - 安装期和运行期共用此解析逻辑。权限修复在确认普通文件后由 verifier 设为 `0755`。
      - 同一处拒绝 `platform.target == "cpu"`（`PLATFORM_TARGET_CPU_RETIRED`，见 3.0f）；
      表驱动单测覆盖：合法入口、缺失、旧入口单独存在、旧入口与新入口同时存在、非法 ID、符号链接、`target == "cpu"` 的旧包。


- [ ] **4.2** `internal/algorithm/verifier.go`：先解析并校验 manifest，再调用 `ResolveEntry` 验证 `<manifest.id>` 入口；对确认是普通文件的入口规范权限为 `0755`。旧 `plugin.so` 明确映射为不支持错误，缺失新入口映射为 `ErrMissingEntry`。
- [ ] **4.3** `internal/algorithm/uploader.go`：移除按 basename 为 `plugin.so` 补执行权限的旧逻辑；归档安全检查保持不变，入口权限由 verifier 在验证包内普通文件后统一规范。
- [ ] **4.4** `internal/algorunner/process.go`：用 `ProcessConfig.ExecutablePath` 作为命令路径，argv 只包含重复 `--broker`、`--name`、`--algorithm-id`；不传 `--plugin` 或 `--max-workers`。移除通用 `RunnerPath` / `PluginPath` spawn 分支。
- [ ] **4.5** `internal/algorithm/verifier.go` 的 `runDynamicVerification` 直接执行已解析的包入口，参数为 `--verify --sample <image>`，`cmd.Dir` 固定为包目录。
- [ ] **4.6** 更新 `Runtime` 与所有 supervisor 路径：启动、升级、回滚、崩溃恢复都使用当前包根可执行文件；移除 `DefaultAlgorithmRunnerPath` 对算法包进程的依赖。
      已拍板方案（T5）：supervisor 启动前用 `ResolveEntry` 校验每个已启用包。对于旧 `.so` 包或缺失入口的包，标记为 `unavailable` 并上报稳定错误码 `ALGO_ENTRY_INVALID`，**直接进入「不自动重试」状态**，杜绝 5s 退避重试风暴；由运维在前端或管理端重新安装/重建为可执行文件形态。

- [ ] **4.7** 验证：
      ```bash
      python3 native/scripts/build.py go test -race ./internal/algorunner
      go test -race ./internal/algorithm
      # 端到端：安装并运行 plate 与 face 的可执行文件包；plugin.so-only 包必须在安装期被拒绝
      ```

- [ ] **4.8** 宿主特征提取改道到包可执行文件（`design.md` §11）：
      - 新增 Go 侧 client（建议独立包 `internal/faceextract`，不得在 `internal/person` 内直接依赖 native/exec 细节）：帧流写入 + 逐行 JSON 解析 + `request_id` 校验；
      - 热进程缓存：并发 1、请求超时 30s（到点 `kill(-pid)` + `Wait()`）、空闲 60s 回收、单帧最多重试 1 次；缓存 key 含 `<pkgDir>` 与 exe `(dev,ino,size,mtime)`；
      - **spawn 必须复用** `runnerSysProcAttr()` + `LockOSThread` helper，禁止裸 `exec.Command`；
      - `internal/app/adapters.go` 用新实现替掉 `eng.ExtractFaceFeature`；新增配置 `[person].extractor_algorithm`（默认 `face_recognition`，viper 严格模式要同步）；
      - 删除 `native/src/abi/engine.cpp` 的 `FacePluginLoader` 与 `Zhulong_engine_extract_face_feature`，删 `internal/engine.ExtractFaceFeature` 及对应测试；
      - 错误码映射逐条对照 `design.md` §11.8（严格 fail-closed，不得写零向量）。
      - **单测不得依赖真实算法包**：客户端逻辑（组帧、`request_id` 校验、超时 kill 进程组、重试 1 次、空闲回收、版本失效 retire）全部用 stub 可执行文件驱动（`t.TempDir()` 里写一段 shell/Python，按帧读 stdin 后吐固定 JSON）。真实包的集成验收只在 3.8 做一次，不引入 Go 测试对 `build/packages/face_recognition/face_recognition` 的硬依赖。
      - 建议覆盖的表驱动用例：`request_id` 错配、进程中途退出、超时不响应、stdout 洪泛带日志行、`feature_dim=0/513`、包目录 mtime 变化触发 retire。
- [ ] **4.9** 门禁与文档同步：`native/tests/check_plugin_symbols.py` 去掉 face 双符号白名单并新增 exe「导出表为空」断言；`cgo-contract.md §6`、`algo-plugin-contract.md:49`、`hardware-backends.md:258` 按 `design.md` §11.9 重写。

**Review gate 4**：AC4 —— 两个算法包均能安装、启动、停止、重启及回滚；旧 `.so` 包明确拒绝，且不进入启动重试；AC9-AC13 全过；`grep -n "dlopen\|dlsym" native/src/abi/engine.cpp` 为空。

---

## 阶段 5：孤儿认亲适配

- [ ] **5.1** 按 `design.md` §6.2 改 `internal/algorunner/orphan_linux.go:201-241`：
      - `findOrphanCandidates` 增加 `algorithmsDir` 入参：逐 pid 读 `cmdline` 后，再 `readlink /proc/<pid>/exe`；
      - 唯一认亲条件为 `/proc/<pid>/exe` 严格落在当前实例 `algorithmsDir` 下，不再通过统一 `runnerBase` 接受旧 runner；
      - `exe` 先 `TrimSuffix(" (deleted)")`；`algorithmsDir` 先 `filepath.Abs` + `EvalSymlinks`，再用 `filepath.Rel` + `escapesInstallRoot` 判严格后代；
      - `parentIsReaper` 必要条件**原样保留**——它在 `ReapOrphanRunners` 主循环（`:96-101`），不在 `findOrphanCandidates` 里，两处都要顾到；
      - `readlink` / `cmdline` 的 `EACCES`、`ENOENT` 静默跳过；
      - **不得**新增「broker 目录归属」判据或退化为「父进程不是我」式反向判定。
- [ ] **5.1b** `ReapOrphanRunners` 签名加 `algorithmsDir string`；调用侧 `internal/algorithm/module.go:119` 的 `algorithmLifecycleImpl` 增一个字段（取 `rt.AlgorithmsDir`），`algorunner` 不反向 import。

- [ ] **5.2** 单测（`orphan_linux_test.go`）：
      - 新形态孤儿：`writeFakeRunner` 产出的副本放进临时 `algorithmsDir`、被 init 收养 → 被回收；
      - **本实例之外**的同名可执行文件（另一个 `algorithmsDir`）被 init 收养 → **不被触碰**；
      - 活宿主监督的进程 → 不被触碰；
      - `exe` 读不到（`EACCES` / 已消失）→ 静默跳过，不 panic；
      - 现有 `TestReapOrphanRunners_KillsReparentedRunner`、`TestReapOrphanRunners_NeverKillsSupervisedRunner`、`TestReapOrphanRunners_SkipsCleanupWhenLockIsBusy`、`TestRemoveUnheldShmSegments_*` 仍通过。
      > 实况提醒：`writeFakeRunner` 把 `/bin/sleep` 拷到 `t.TempDir()` 并 exec，故 `/proc/<pid>/exe` 指向该副本；测试需把副本放进临时 `algorithmsDir` 并传入该目录。

- [ ] **5.3** 验证：
      ```bash
      python3 native/scripts/build.py go test -race ./internal/algorunner -run 'TestReapOrphan'
      python3 native/scripts/build.py go test -race ./internal/algorunner -run 'TestRemove'
      ```

**Review gate 5**：AC5 全部用例通过。**rollback = revert 本阶段 commit。**

---

## 阶段 6（**不执行**；PRD R7 已降级）：底库去进程内单例

> **降级理由**：在「可执行文件 + 静态链接运行时」形态下，`algo_main` 与算法代码在**同一二进制内**，`GalleryRegistry::instance()` 自然只有一份，`GalleryIndex`（写入）与 `face_recognition.cpp:184`（快照读取）使用同一个对象；因此本阶段没有功能收益。
>
> **2026-10-25 同步**：`prd.md` R7 / AC6 已按此判断降级为「保持现状，不改代码」，本阶段因此从「可选」变为**不执行**；6.1–6.4 保留为历史方案，供「将来同一进程加载两份算法代码」时取用。
> 旧 SHARED/runner target 如为 native 测试暂留，不代表支持将静态 runtime 链入 `plugin.so` 并由宿主加载。
> 若执行，顺序不变（6.1-6.4），并在 SDK 发布说明里标注 `GalleryRegistry::instance()` 已弃用。

- [ ] **6.1** `native/sdk/include/zhulong/runtime/gallery_registry.hpp`：`GalleryRegistry` 从「全局单例」改为「可由 SDK main 注入的实例」，算法包通过 `zhulong::gallery()` 读取只读快照。保留 `snapshot()` 语义与线程安全（现有 `std::mutex`）。

- [ ] **6.2** `algo_main.cpp` 在 `gallery_sync` 阶段把快照写入该实例；分片聚合逻辑复用现有 `accumulate_sync_shard`（`gallery_parser.hpp`），**不要重写**分片语义。

- [ ] **6.3** 检查所有读取路径：`grep -rn "GalleryRegistry::instance" packages/ native/`，逐一改为 `zhulong::gallery()`。

- [ ] **6.4** 验证：
      ```bash
      make native-build && make native-test
      # AC6：10k 底库推送后比对命中率与基线一致；64KB 分片边界用例通过
      ```

**Review gate 6**：（若执行）AC6 通过，且 `grep "GalleryRegistry::instance"` 为空。**rollback = revert 本阶段 commit。**

---

## 阶段 7：SDK 分发 + 干净环境验证

- [ ] **7.1** `Makefile` 新增 `sdk-dist` target（内容 = `make sdk-install` 的 prefix **原样打包** + 样例 + 模板 + 工具，禁止另建一份头文件列表）：
      ```
      zhulong-algo-sdk-<ver>-<arch>-<target>.tar.gz
      ├── include/zhulong/{plugin.h,algo.hpp,algo_main.hpp,runtime/*.hpp}
      ├── lib/{libzhulong_algo_sdk.a,libZhulongAlgoRuntime.a}
      ├── tools/algo_probe.py                  # 帧流协议自查（--verify / --extract-feature）
      ├── tools/pack_package.py                # 打包（与仓库内同一份，不另写模板副本）
      ├── tools/new_package.py                 # 脚手架（一条命令生成包骨架）
      ├── examples/plate_recognition/onnx/     # 最小可用样例（含自己的 Makefile，单 target 目录形态）
      ├── templates/{CMakeLists.txt,Makefile,manifest.json,config.schema.json}   # 生成到 <id>/<target>/
      ├── templates/tests/{dev_infer.cpp,third_party/stb_image.h,third_party/stb_image_write.h}   # dev-only，不进包
      └── abi.json                             # api_version / protocol / 依赖声明
      ```
      必须写清的三件事（否则第三方一定卡住）：
      1. **链接顺序**：`libzhulong_algo_sdk.a` 在前、`libZhulongAlgoRuntime.a` 在后（静态库循环依赖），并在 `templates/CMakeLists.txt` 里固定；
      2. **`ZhulongLog` / `ZhulongAlgoIpc` 不再单独分发**：它们的目标文件已被 `libzhulong_algo_sdk.a` 吸收（`design.md` §4.4）；
      3. 第三方动态依赖声明：`libonnxruntime.so.1.21` / `librknnrt.so` **不由 SDK 提供**，写清来源与安装方式。
      4. **打包入口只有一个**：`tools/pack_package.py`；`templates/` 不得再放第二份 `pack_package.py`（副本必然漂移，且第三方会不知道改哪份）。

- [ ] **7.2** 干净容器验证（AC7 + AC17）—— 唯一的「独立性」证明（从零生成 → 编译 → 测 → 打包，全在包目录内）：
      ```bash
      # 容器内只有 sdk-dist 解压内容，没有本仓库
      export SDK=/opt/zhulong-algo-sdk
      python3 $SDK/tools/new_package.py demo --target onnx  # AC17：脚手架 → demo/onnx/
      cd demo/onnx
      make SDK_PREFIX=$SDK          # 编译（TARGET 取自目录名）
      make test SDK_PREFIX=$SDK     # dev-only 开发期测试：出 result-<id>-<target>.jpg + 终端明细表
      make package SDK_PREFIX=$SDK  # 验包 + 归档
      tar tzf dist/demo-*.tar.gz | head
      tar tzf dist/demo-*.tar.gz | grep -c 'targets/'   # 期望 0（资产已摊平）
      ```
      期望：`verify` 输出 `{"status":"passed"}` 且 `count` 为 0（空实现，可接受）；归档里入口名为 `demo`、**无 `plugin.so`**。另一个更严的变体：把 `$SDK/examples/plate_recognition/onnx/` 整套跑一遍，`count` 应与阶段 0.3 基线一致。**任一步失败即视为 SDK 仍有仓库依赖**（最常见：某个头仍 `#include` `native/src`、`pack_package.py` 读 `native/`、或模板 Makefile 引用了仓库路径）。

- [ ] **7.3** 全量回归（`make check` 已包含 `packages-build` + `packages-test`）：
      ```bash
      make check
      ```

- [ ] **7.4** Spec 同步（`make check` **不覆盖**这些文档，不写就会漂）：
      - `AGENTS.md` 的「命令」段：把日常入口改成 `make` / `make test` / `make package` / `make check`，并保留一行说明内部分解目标仅供 CI；不要列出 `cmake -B …` 这类实现细节；
      - `.trellis/spec/backend/algorithm-guidelines.md`：包根 `<manifest.id>` executable、`0755`、`models/` 同级、旧 `plugin.so` 拒绝及 `--verify --sample` 验包命令；并补「包自己的 `CMakeLists.txt` + `find_package(ZhulongAlgoSDK)` 构建、`make native-build` 不编包」；
      - `.trellis/spec/native/algo-plugin-contract.md` 补一节「结果 JSON = 唯一跨边界结果契约」：载荷形状与 `serialize_inference_result` 一致，三个消费者（broker 长驻 / 一次性推理 / `algo_probe.py`）共用；**只允许追加字段**；一次性推理不含底库与跨帧跟踪（`matched` 恒 false、`track_id` 恒 0）；`--verify` 输出是它的超集（带 `status` / `sample_decoded` / `width` / `height`）；
      - 同一份 spec 还要补 **R11 形态**：源码目录 = `packages/<id>/<target>/`、target 间零引用（含 CMake 路径）、**无共享测试夹具**（每 target 自带 `tests/`，`make test` 出 `result.jpg` 与终端表；`tests/` 不进归档）、出包资产摊平（归档内无 `targets/`）、`--target` 取值必须对应真实目录；并写明「允许重复、禁止抽象」的 review 口径；
      - `.trellis/spec/native/algo-plugin-contract.md`：说明宿主直接启动包可执行文件；保留 C ABI 函数表作为 SDK 内部桥接契约，但标明 `.so` 加载不再是受支持的算法包交付形态；
      - 同步 `TaskContext { task_id, params }`、四种公开结果适配器、`ZHULONG_ALGO_MAIN(PluginClass, ResultAdapter, PluginName, PluginVersion)`、头文件模板 `run<...>` 及 detail 层 `PluginFactory` 的边界，以及一 broker 一 Worker 的约束；不记录 `max_workers`。
      - `.trellis/spec/native/cgo-contract.md §6` / `hardware-backends.md:258`：按 `design.md` §11.9 改写成「特征提取经包可执行文件一次性通道」（若阶段 4.9 已改，此处只做复查）。
- [ ] **7.5** 清理边界审查：确认算法包 builder、verifier、supervisor 均不再产出、接受或启动 `plugin.so`；旧 SHARED/runner target 如仍被 native 测试引用，标注为测试专用且不得进入 SDK 分发物或宿主生产路径。

**Review gate 7**：7.2 容器内通过 + `make check` 全绿。

---

## 最终验收对照

| AC | 验证命令/方式 | 阶段 |
| :--- | :--- | :--- |
| AC1 黑盒判据 | `nm -D --undefined-only <bin> \| grep -c zhulong` = 0；`ldd <bin> \| grep -ci zhulong` = 0；**附加** `nm -D <bin> \| grep -c zhulong` = 0 | 3.4 |
| AC2 线程/进程不回退 | `ps -L` Worker 线程数与 broker 数一致；进程数 = 启用算法包数；所有 broker 均成功订阅 | 3.5 |
| AC3 行为等价 | 验包 `count` 与阶段 0.3 基线一致；broker 帧计数同量级 | 0.3 / 3.5 |
| AC4 单一形态端到端 | face 与 plate 可执行包正常运行；旧 `.so` 包安装明确失败；`make check` | 4.7 / 7.3 |
| AC5 孤儿清扫 | `TestReapOrphan*` 全套 | 5.3 |
| AC6 底库不回退 | 分片边界用例全绿；快照与改造前同源（不测 10k 命中率——同代码同数据，测不出差异） | 6.x 不执行 |
| AC7 SDK 自足 | 干净容器编译 + 验包 | 7.2 |
| AC8 门面 task/config 补齐 | 签名审阅 + `make native-test` + 验包与基线逐字一致 | 3.0a-c |
| AC9 特征提取语义等价 | 特征向量余弦相似度 ≥ 0.999；四类 HTTP 语义不变 | 0.4 / 3.8 / 4.8 |
| AC10 特征提取失败可诊断 | 包缺失/崩溃/超时/非法 JSON/维度越界各一用例，不 panic、不写零向量 | 4.8 |
| AC11 宿主不再加载算法代码 | `grep dlopen\|dlsym native/src/abi/engine.cpp` 为空；`nm -D` 无 zhulong | 4.8 / 4.9 |
| AC12 帧流协议 | 3 帧（含无脸）→ 3 行 JSON、`request_id` 对应、进程不退出；无新增解码依赖 | 3.8 |
| AC13 热进程复用与回收 | 5 次登记 1 次 spawn；空闲 60s 归零；宿主 SIGKILL 后子进程 **≤3s**（轮询断言）内消失；超时无僵尸 | 4.8 |
| AC14 包版本失效 | 替换包后首请求必起新进程并记录 retire 原因 | 4.8 |
| AC15 现有 spawn 契约 | `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` + `make check` | 4.8 / 7.3 |
| AC16 包独立构建 | `make native-build` 不产 `*Recognition*`；包仅靠 `build/sdk/prefix` 构建；`grep packages/ native/CMakeLists.txt` 为空；交叉 prefix 不混用；`diff templates/Makefile packages/<id>/<target>/Makefile` 仅差 `SDK_PREFIX` | 0.5 / 3.2b / 3.9 |
| AC17 脚手架可用 | 一条命令生成 `<id>/<target>/` 骨架 → `make`/`make test`/`make verify`/`make package` 全绿；非法 id 生成期被拒；`make sdk-selftest` 进 `make check` 且交叉 profile 下 SKIP | 3.2c / 7.2 |
| AC18 第三方运行时处置 | onnx：ORT 在包内 `lib/`、`ldd` 指向包内、归档含 `NOTICE`；rknn：不绑定且启动探测；验包输出 `bundled\|system` | 3.3 / 3.7 / 7.1 |
| AC19 每 target 独立实现与产物 | 实现目录间零引用（`grep ../onnx\|../rk3568\|targets/` 为空）；各 target 独立构建互不覆盖；**每 target 的 `make test` 产出 `result.jpg` + 终端明细表，且归档内无 `tests/`、`ldd` 无图像解码库**；无 `vectors/*.json`；归档无 `targets/`、无 `.onnx` 混入 rknn；prefix target 断言生效；缺资产时 `build_package.py --target rk3588` 明确失败 | 3.0e / 3.2b / 3.3 / 3.6 / 3.7 |
| AC20 量化契约 | 算法 src 无 `zero_point`/`quant`；rknn 归档含 `QUANTIZATION.md`（逐模型精度模式）与 `CONVERSION.md`；`Tensor::data<T>()` dtype 校验生效；RKNN 输出恒 `FLOAT32`；`.rknn` 内嵌 attrs 仅用于 drift 检查 | 1.1c / 3.7 / 7.4 |
| AC21 平台事实不做公共抽象 | `InputFacts`/`prepare_input`/`preprocess.json`/`set_is_onnx` 在仓库中归零；`pass_through = (io_attr.type ...)` 隐式判断删除；归一化常量每实现仅一处定义点；**`rknn` / `test_stub` prefix 下**脚手架空实现包 `readelf -d` 无 onnxruntime（onnx 档位必然带 ORT，见 AC18） | 1.1c / 3.0e / 3.7 |
| AC22 桩后端退出产品面 | `cpu_mock` 在 `native/src`/`native/sdk`/`native/scripts`/`Makefile` 的**代码与配置**中归零（允许 `build.py` 的 docstring 保留 1 处历史说明，见门禁通用约定）；`default_inference_backend` 删除；未声明后端的 profile configure 失败；`ar t` 产品存档无 stub；`[SKIP]` 伪装用例归零；`test_stub` 拒绝出包 | 1.0a-f |

---

## 极重要的遗漏防护

本任务**不**修参数热更新（`design.md` §10），但有三处容易在实现时被顺手弄坏：

1. 为了「让参数生效」擅自给 `model_create` 传真配置 → 破坏 AC3 行为等价，且引出第二套参数体系。
2. 为了「少重启」把 `ScheduleAlgorithmRestart` 改成原地更新 → 超出本任务范围，且需要 ABI 变更。
3. `algo_main.cpp` 的循环写成不肯留控制消息位置的死循环 → 阻塞后续热更新落地。
4. 将可执行文件放进 `bin/` 以求“目录整洁” → `dladdr` 找不到 `models/`（`design.md` §3.1）。
5. 为了“少一次 exec”把特征提取做成常驻守护进程 + 自定义 IPC → 等于再造一套 broker，把本任务消灭的跨边界机制原样长回来（`design.md` §11.7）。
6. 在 `engine.cpp` 里 `posix_spawn` / 在 handler 里裸 `exec.Command` → 拿不到 `dataDir`，或者绕开 `Pdeathsig`+`LockOSThread` 契约把健康子进程 SIGKILL 掉（`design.md` §11.4 第 6 条）。
7. 为了让 `make native-build` “一次编完”，在包 CMakeLists 里写 `if(TARGET ZhulongAlgoRuntimeStatic)` 回退到 in-tree target，或改回顶层 `add_subdirectory(../packages/x)` → 仓库包与第三方包重新分叉，AC7/AC16 同时失效（`design.md` §4.5）。
7b. 为了「少写两遍 decode」把两个 target 目录合并、或让 `onnx/` 的 CMakeLists 引用 `../rk3568/` → 跨 target 隔离失效，AC19 直接红；重复是**被接受的代价**，用 `make test` 出 `result.jpg` 看图兑正确性（不是把两份实现拧回一份）。
7c. 为了「统一归一化」重新引入 `InputFacts` / `prepare_input` / `preprocess.json` / 配置表 → R13 已撤销，这是把分支搬进 SDK 的负收益抽象，review 直接拒绝。
7d. 把主机档位重新叫回 `cpu`（或新增 `cpu` 档位）→ 与 build 侧 `cpu_mock` 桩后端同名不同义，运维会以为「CPU 推理」，而实际包跑的是 ORT/f32（`prd.md` F25）。
7e. 为「省事」把桩后端留在产品面（`cpu` 作为可选后端/默认值/profile 档位）→ 交叉或不带 ORT 的机器会产出"编译成功但不可推理"的产物，包级测试用 `[SKIP]` 假装通过（F26/F27）；桩只能存在于夹具目标。
8. 让 `build_package.py` 继续“顺便帮你编一下”（`cmake --build`） → 包的构建入口变成两个，AC16 无法验证。
9. 在 Makefile 里用 `cp`/`tar`/`jq` 重写一遍打包，或在 `templates/` 里再放一份 `pack_package.py` → 三份实现必然漂移，第三方也不知道改哪份（`design.md` §4.6）。
10. 让脚手架生成「能编但过不了验包」的骨架（漏 `sample.jpg`、空实现却打了 `--no-verify`、或合成假检测框） → 第三方第一次体验就是失败，或模板把“合成结果”当成合法做法教出去（AC17 / `algo-plugin-contract.md`）。
11. 为了「目录干净」把 ORT 放到宿主级共享目录（`<dataDir>/algorithms/.runtime/`）或全局 `/usr/lib` → 路径依赖安装深度/需要 root，升级后静默指向旧版本；正确做法是随包 `lib/` + `RUNPATH=$ORIGIN/lib`（`design.md` §4.5.4）。
12. 「顺手」在算法里加反量化（`want_float=0` + 自己乘 scale）或以 int8 直出为由绕过后端 → 算法重新按后端分叉，「一套代码」立刻失效；量化只允许留在模型资产与后端内部（`design.md` §4.8.5）。
13. 为将来的「UI 上传图片试用」另造一套结果 JSON（或给 `--verify` 起一套与 IPC 不同的字段名）→ 跨边界契约立刻分叉成两份，而这个任务的全部意义就是终结协议漂移。正确做法：明细就是 `serialize_inference_result` 的载荷，只许追加字段（3.2 / 7.4）。
14. 为了「让 UI 好看」在一次性推理里伪造 `matched` / `person_name` / `track_id` → 一次性进程没有底库、没有跨帧状态，伪造会让人拿它当「识别不准」的证据；正确做法是如实返回 false / 0 并在输出与 UI 上标注（`design.md` §3.4）。

---

## 未决 / 延后

- **RKNN target 迁移**：本任务只做 `onnx` 档位（`design.md` §9.3）。RKNN 版需在板端验证，交叉只能编译。`packages/face_recognition/rk3568/` 在本任务中按 R11 建成独立实现目录、**只编译**，行为验收与阈值标定留待板端任务。
- **`Session::prepare_input` 复用槽（性能）**：R13 撤销后，每帧 `~4.9MB` 分配与 HWC→CHW 重排的优化移出本任务，作为独立性能任务（需先有 host 侧分配计数基线）。
- **跨 target 纯数学收敛（`core/`）**：若板端具备 smoke 回归，再把 decode/NMS/align 的重复收敛为共享目录；现在不做，R11 已写明理由与禁止项。
- **`--config-file`**：**已定不引入**（`design.md` §10）。现状 `model_create("{}")` 恒空是下发链路未打通，不是缺口位置错；任务级参数的正确载体是 `task_open`，而它在 ABI v1 里没有 config 位置。参数下发作为独立任务。
- **`manifest.protocol` 版本校验**：本任务不做；等旧包二进制与 SDK 出现第一次真实协议漂移时再加。
- **批量人脸导入**：`--extract-feature` 的帧流协议已支持「一进程 N 帧」，但本任务不新增批量导入接口、不做进程池与调度；将来加批量导入时**不需要改协议**。
- **RKNN 板端特征提取标定**：`--extract-feature` 在 RKNN target 上的冷启动/单帧耗时需在板端实测（开发机无法执行交叉产物，AGENTS.md 明确规定）；未实测前不得给出性能结论。
