# RKNN 推理后端：实施计划

前置：本任务为父任务 `10-10-algorithm-plugin-architecture` 阶段四的一半。兄弟任务 `10-13-algo-rga-vision-backend` 依赖本任务先建立的后端选择机制（见 §1 阶段 A 步骤 3 的说明）。

**执行顺序**：

```txt
10-13-algo-broker-host  →  本任务  →  10-13-algo-rga-vision-backend
```

- ⚠️ **本任务的真链路验证依赖 `10-13-algo-broker-host`**。该任务补齐 FrameBroker 宿主与帧发布通路（当前 `FrameBroker` 类完整但生产代码零构造点）。未完成时，本任务只能靠 `--verify-plugin` 单次黑盒验包路径验证，**AC4（1/2/4 worker 真机并发）无法走真实帧通路**，只能直接调用后端 API 做并发测试。
- 若 `10-13-algo-broker-host` 未完成而需先开工，AC4 应如实标注为「后端 API 级并发测试」，不冒充真链路验收。
- ⚠️ 本任务与 broker 任务**共享构建文件**：两者都改 `native/CMakeLists.txt` 与 `native/scripts/build.py`，因此**必须串行**（broker 任务在前，它建立的归档链接方向是本任务的前提）。本任务另改 `native/src/backends/rknn/` 与 `runtime_factory.cpp`；broker 任务另改 `engine.h`、`pipeline/`、`ipc/` 与 Go 侧接线。

**与 RGA 任务的串行要求**：两者都会触碰 `native/CMakeLists.txt` 与 `native/scripts/build.py`，必须串行，不可并行编辑。

## 1. 实施清单

### 阶段 A：构建接入机制（先做，RGA 任务依赖）

- [ ] 在 `native/CMakeLists.txt` 新增两个正交 cache 变量：`ZHULONG_INFERENCE_BACKEND`（`cpu` | `rknn`）与 `ZHULONG_VISION_BACKEND`（`cpu` | `rga`），默认均为 `cpu`。
  - `rknn` 分支要求 `-DZHULONG_RKNN_ROOT`，缺失时 `FATAL_ERROR`。
  - ⚠️ `ZHULONG_VISION_BACKEND` 此处只声明变量，视觉侧分支由 RGA 任务添加。
  - 验证：`cmake -DZHULONG_INFERENCE_BACKEND=rknn` 无 `ZHULONG_RKNN_ROOT` 时报错退出。
- [ ] 新建 `native/src/backends/rknn/` 目录；CMake 中该目录 include 一律 `PRIVATE`。
  - 验证：`grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/` 只命中 `backends/rknn/`。
- [ ] 扩展 `native/scripts/build.py`：支持 RKNN 前缀的获取与 SHA-256 校验（`rknn_api.h` = `c48e11a6…`，`librknnrt.so` = `d31fc19c…`），并把该前缀加入 `configure()` 的 `CMAKE_FIND_ROOT_PATH`。
  - ⚠️ 保持**离线**：从板端 `scp` 或本地已有副本，不联网下载。
  - ⚠️ 不把库塞进 `build/sysroots/rk3568`（会污染 `tree_digest` 缓存身份）。
  - 验证：SHA-256 不匹配时构建失败。
- [ ] `runtime_factory.cpp` 用 `#ifdef ZHULONG_INFERENCE_RKNN` 选择推理后端。
  - 验证：CPU 构建 `make native-test` 通过；`algo_runtime_test.cpp` 未改动仍通过。

### 阶段 B：RKNN 后端实现

- [ ] 从板端 `rknn_api.h` 读取真实 `RKNN_ERR_*` 枚举名与数值，写 `rknn_error_map.cpp`；设备不可用映射 `NOT_SUPPORTED`，参数/模型/上下文非法映射 `INVALID_ARGUMENT`，其余 `INTERNAL`。
  - 验证：单测覆盖每个已映射的枚举值。
- [ ] 实现 `RknnModelAsset`：`rknn_init` + `rknn_query` 取输入/输出 tensor 属性与 `RKNN_QUERY_MEM_SIZE`。
  - ⚠️ tensor 属性必须从 Runtime 查询，不从文件猜测（R9）。
  - ⚠️ 无法取得属性时拒绝该模型。
- [ ] 实现 `RknnSessionContext`：`rknn_dup_context(&root, &ctx)`，加 `dup_mutex` 串行化。
  - ⚠️ 两个参数都是指针。
  - ⚠️ 不假定可并发调用。
- [ ] 实现 `forward`：`rknn_inputs_set` → `rknn_run` → `rknn_outputs_get` → 拷贝进 `Tensor` → `rknn_outputs_release`。
  - ⚠️ 同步完成语义：返回时设备已不再使用输入（父任务 design §4 第 4 条）。
- [ ] 实现销毁路径：session 先释放自己的 tensor memory，model 在所有 session 回收后 `rknn_destroy(&root)`。
  - 验证：创建中途失败时已分配资源全部回滚（AC5）。
- [ ] 加异常捕获：`forward`/`create_session`/`load_model` 内部 `try/catch(...)` 转换为状态码，输出清零。

### 阶段 C：真机验证

- [ ] 部署到 RK3568（`scp`，精简传输）。
- [ ] 查询并记录 `RKNN_QUERY_SDK_VERSION` 的实际 `api_version` / `drv_version`。
  - 验证：数值写入任务记录，与已知的 2.3.2 库 / 0.9.8 驱动对照。
- [ ] 对照组实验：N 次独立 `rknn_init` vs 1 次 `rknn_init` + (N-1) 次 `rknn_dup_context`。
  - 采集：输出一致性（定容差）、`RKNN_QUERY_MEM_SIZE`、RSS/PSS、销毁行为。
  - ⚠️ 不把逻辑权重相加当物理占用。
  - ⚠️ 共享不成立 → 标记阻塞并报告，**不静默初始化 N 份权重**。
- [ ] 1/2/4 worker 对照：输出一致性、并发稳定性、重复启停无 FD/内存增长。
  - ⚠️ RK3568 单 NPU 核，不声称线性扩展。
  - ⚠️ **若 `10-13-algo-broker-host` 未完成**，本项只能直接调用后端 API 做并发测试，不走真实帧通路；验收记录需如实标注为「后端 API 级」，不冒充端到端。
- [ ] 用真实 `.rknn` 模型（`facelivtv2_s_rk3568_fp.rknn`）跑通一次真实推理。

### 阶段 D：验收核对

- [ ] AC1–AC7 逐项核对。
- [ ] 产物 `readelf -d` 检查：`ZhulongAlgoRuntime.so` 依赖 `librknnrt.so`，宿主 `Zhulong` 不依赖。
- [ ] 确认未链接 `librknn_api.so`（1.5.2）。
- [ ] 更新 `.trellis/spec/native/algo-plugin-contract.md` 记录实际 RKNN 合同。
- [ ] 记录残余风险（共享是否成立、编译器代差、磁盘、功耗）。

## 2. 验证命令

```bash
# CPU profile 回归（不依赖板卡）
make native-test

# 头文件隔离
grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/

# 宿主无 RKNN 依赖（交叉产物）
readelf -d build/targets/rk3568/Zhulong | grep NEEDED

# 交叉构建（走 wrapper，不裸跑 go build）
CROSS_PROFILE=native/profiles/rk3568.json CROSS_OUTPUT=build/targets/rk3568/Zhulong make native-build

# 收工门禁
make check
```

⚠️ 交叉**只允许编译，不允许执行测试**（AGENTS.md）。

## 3. 回退点

- 阶段 A 失败：删除 `ZHULONG_INFERENCE_BACKEND` 相关改动，回到硬编码 CPU 后端。既有功能不受影响。
- 阶段 B 失败：保留 `backends/rknn/` 骨架但默认 `cpu` profile，后端实现标记未完成。
- 阶段 C 共享验证不成立：任务范围收窄为「后端可用 + 共享阻塞报告」，R3 转为阻塞项，不假装完成。
- 任何阶段：不修改 `IInferenceBackend` 接口、`plugin.h`、`tensor.hpp`，保证既有插件 ABI 与 CPU 路径零回归。

## 4. 已知风险

- ⚠️ 板端磁盘仅剩 **2.9G**，部署与临时产物可能失败。
- ⚠️ 编译器代差（宿主交叉 14.2.0 / sysroot libstdc++ 10 / 板端 10.2.1）。
- ⚠️ `librknn_api.so`(1.5.2) 残留可能通过 `ldconfig` 顺序意外被解析。
- ⚠️ `rknn_dup_context` 行为未验证，是最大不确定性。
- ⚠️ `/dev/rga` 为 `root:video`，运行进程需在 `video` 组（与 RGA 任务共用部署约束）。
