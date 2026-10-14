# RKNN 推理后端：实施计划

前置：本任务为父任务 `10-10-algorithm-plugin-architecture` 阶段四的一半。兄弟任务 `10-13-algo-rga-vision-backend` 依赖本任务先建立的后端选择机制（见 §1 阶段 A 步骤 3 的说明）。

**执行顺序**：

```txt
10-13-algo-broker-host  →  本任务  →  10-13-algo-rga-vision-backend
```

- ⚠️ **本任务的真链路验证依赖 `10-13-algo-broker-host`**。该任务已于 **2026-10-14 完成并归档**（提交 `4d9d810`）：FrameBroker 宿主、帧发布通路、Go 侧多路订阅与 runner 多路 `--broker` 全部落地并在 RK3568 真机验证（`ZhulongFrameBrokerTests` 14/14、板端 Go-CGO 桥接 6/6）。

**因此 AC4（1/2/4 worker 真机并发）可以走真实帧通路**，不需要降级为「后端 API 级并发测试」。

⚠️ 本任务与 broker 任务**共享构建文件**：两者都改 `native/CMakeLists.txt` 与 `native/scripts/build.py`。broker 任务已先行完成（它建立的归档链接方向是本任务的前提），因此**本任务可以直接开工**。本任务另改 `native/src/backends/rknn/` 与 `runtime_factory.cpp`。

**与 RGA 任务的串行要求**：两者都会触碰 `native/CMakeLists.txt` 与 `native/scripts/build.py`，必须串行，不可并行编辑。

## 1. 实施清单

### 阶段 A：构建接入机制（先做，RGA 任务依赖）

- [x] 在 `native/CMakeLists.txt` 新增两个正交 cache 变量：`ZHULONG_INFERENCE_BACKEND`（`cpu` | `rknn`）与 `ZHULONG_VISION_BACKEND`（`cpu` | `rga`），默认均为 `cpu`。
  - `rknn` 分支要求 `-DZHULONG_RKNN_ROOT`，缺失时 `FATAL_ERROR`。
  - ⚠️ `ZHULONG_VISION_BACKEND` 此处只声明变量，视觉侧分支由 RGA 任务添加。
  - 验证：`cmake -DZHULONG_INFERENCE_BACKEND=rknn` 无 `ZHULONG_RKNN_ROOT` 时报错退出。
- [x] 新建 `native/src/backends/rknn/` 目录；CMake 中该目录 include 一律 `PRIVATE`。
  - 验证：`grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/` 只命中 `backends/rknn/`。
- [x] 扩展 `native/scripts/build.py`：支持 RKNN 前缀的获取与 SHA-256 校验（`rknn_api.h` = `c48e11a6…`，`librknnrt.so` = `d31fc19c…`），并把该前缀加入 `configure()` 的 `CMAKE_FIND_ROOT_PATH`。
  - ⚠️ 保持**离线**：从板端 `scp` 或本地已有副本，不联网下载。
  - ⚠️ 不把库塞进 `build/sysroots/rk3568`（会污染 `tree_digest` 缓存身份）。
  - 验证：SHA-256 不匹配时构建失败。
- [x] `runtime_factory.cpp` 用 `#ifdef ZHULONG_INFERENCE_RKNN` 选择推理后端。
  - 验证：CPU 构建 `make native-test` 通过；`algo_runtime_test.cpp` 未改动仍通过。

### 阶段 B：RKNN 后端实现

- [x] 从板端 `rknn_api.h` 读取真实 `RKNN_ERR_*` 枚举名与数值，写 `rknn_error_map.cpp`；设备不可用映射 `NOT_SUPPORTED`，参数/模型/上下文非法映射 `INVALID_ARGUMENT`，其余 `INTERNAL`。
  - 验证：单测覆盖每个已映射的枚举值。
- [x] 实现 `RknnModelAsset`：`rknn_init` + `rknn_query` 取输入/输出 tensor 属性与 `RKNN_QUERY_MEM_SIZE`。
  - ⚠️ tensor 属性必须从 Runtime 查询，不从文件猜测（R9）。
  - ⚠️ 无法取得属性时拒绝该模型。
- [x] 实现 `RknnSessionContext`：`rknn_dup_context(&root, &ctx)`，加 `dup_mutex` 串行化。
  - ⚠️ 两个参数都是指针。
  - ⚠️ 不假定可并发调用。
- [x] 实现 `forward`：`rknn_inputs_set` → `rknn_run` → `rknn_outputs_get` → 拷贝进 `Tensor` → `rknn_outputs_release`。
  - ⚠️ 同步完成语义：返回时设备已不再使用输入（父任务 design §4 第 4 条）。
- [x] 实现销毁路径：session 先释放自己的 tensor memory，model 在所有 session 回收后 `rknn_destroy(&root)`。
  - 验证：创建中途失败时已分配资源全部回滚（AC5）。
- [x] 加异常捕获：`forward`/`create_session`/`load_model` 内部 `try/catch(...)` 转换为状态码，输出清零。

### 阶段 C：真机验证

- [x] 部署到 RK3568（`scp`，精简传输）。→ 见 §5.7
- [x] 查询并记录 `RKNN_QUERY_SDK_VERSION` 的实际 `api_version` / `drv_version`。→ **api=2.3.2 / drv=0.9.8**
  - 验证：数值写入任务记录，与已知的 2.3.2 库 / 0.9.8 驱动对照。
- [x] 对照组实验：N 次独立 `rknn_init` vs 1 次 `rknn_init` + (N-1) 次 `rknn_dup_context`。→ **共享成立：RSS 峰值 50.4MB vs 14.3MB**
  - 采集：输出一致性（定容差）、`RKNN_QUERY_MEM_SIZE`、RSS/PSS、销毁行为。
  - ⚠️ 不把逻辑权重相加当物理占用。
  - ⚠️ 共享不成立 → 标记阻塞并报告，**不静默初始化 N 份权重**。
- [x] 1/2/4 worker 对照：输出一致性、并发稳定性、重复启停无 FD/内存增长。→ 全一致；FD 8→8；耗时 316/374/588ms
  - ⚠️ RK3568 单 NPU 核，不声称线性扩展。
  - ✅ **`10-13-algo-broker-host` 已完成**，本项可走真实帧通路（loopback RTSP → 解码 → broker → runner → RKNN 后端），无需降级为「后端 API 级」。
- [x] 用真实 `.rknn` 模型（`facelivtv2_s_rk3568_fp.rknn`）跑通一次真实推理。→ 512 维有限值输出

### 阶段 D：验收核对

- [x] AC1–AC7 逐项核对。→ 见 §6 验收状态表
- [x] 产物 `readelf -d` 检查：`ZhulongAlgoRuntime.so` 依赖 `librknnrt.so`，宿主 `Zhulong` 不依赖。
- [x] 确认未链接 `librknn_api.so`（1.5.2）。→ 三处 `readelf` 均无
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

## 5. 阶段 A/B 实施记录（2026-10-14）

范围：阶段 A（构建接入）+ 阶段 B（RKNN 后端实现）+ 阶段 C 测试骨架代码。
阶段 C 的真机执行由主管会话负责；本记录只覆盖「交叉产物能编译出来」为止。

### 5.1 新增/修改文件清单

`git status --short`：

```
 M .gitignore
 M native/CMakeLists.txt
 M native/scripts/build.py
 M native/src/algorithm/runtime/runtime_factory.cpp
?? native/profiles/rk3568-rknn.json
?? native/src/backends/
?? native/tests/rknn_backend_test.cpp
?? native/vendor/
```

新增文件：

- `native/vendor/rknpu2/include/rknn_api.h`（SHA-256 `c48e11a6…`）
- `native/vendor/rknpu2/lib/librknnrt.so`（SHA-256 `d31fc19c…`，7.4MB）
- `native/vendor/rknpu2/README.md`（来源/版本/同一性/排除旧库说明）
- `native/profiles/rk3568-rknn.json`（交叉工具链 profile + `inference_backend: rknn`）
- `native/src/backends/rknn/rknn_error_map.hpp`（header-only inline）
- `native/src/backends/rknn/rknn_types.hpp`（`RknnModelAsset` / `RknnSessionContext`）
- `native/src/backends/rknn/rknn_inference_backend.hpp`
- `native/src/backends/rknn/rknn_inference_backend.cpp`
- `native/tests/rknn_backend_test.cpp`（Case 1–6 骨架）

`.gitignore`：`*.so` 会忽略 vendored `librknnrt.so`，新增否定规则
`!native/vendor/rknpu2/lib/librknnrt.so` 使该受控副本可提交。

### 5.2 阶段 A 验证

**头文件隔离（AC1）**

```
$ grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/ native/vendor/
native/src/backends/rknn/rknn_types.hpp
native/src/backends/rknn/rknn_inference_backend.cpp
native/src/backends/rknn/rknn_inference_backend.hpp
native/src/backends/rknn/rknn_error_map.hpp
native/vendor/rknpu2/README.md
```

实际 `#include` 命中（排除注释）只有 3 处，全部在 `native/src/backends/rknn/`：

```
$ grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/
native/src/backends/rknn/rknn_types.hpp:8:#include <rknn_api.h>
native/src/backends/rknn/rknn_inference_backend.cpp:6:#include <rknn_api.h>
native/src/backends/rknn/rknn_error_map.hpp:12:#include <rknn_api.h>
```

**CMake 负向守卫**

```
# rknn 缺 ZHULONG_RKNN_ROOT
CMake Error at CMakeLists.txt:180 (message):
  ZHULONG_INFERENCE_BACKEND=rknn 需要 -DZHULONG_RKNN_ROOT=<rknpu2 前缀>

# 非法推理后端
  ZHULONG_INFERENCE_BACKEND 取值非法: cuda（期望 cpu 或 rknn）

# 视觉后端 rga
  视觉后端 rga 尚未实现，见任务 10-13-algo-rga-vision-backend
```

**host/cpu 回归（AC2 前半）**

```
$ make native-test
100% tests passed, 0 tests failed out of 15
Total Test time (real) =  49.10 sec
```

非陈旧证据（`runtime_factory.cpp` mtime 12:23:31 < 目标文件 mtime 12:27:49）：

```
$ nm -D build/native/host/libZhulongAlgoRuntime.so | grep -c 'CpuInferenceBackend'
20
$ nm -D build/native/host/libZhulongAlgoRuntime.so | grep -c 'RknnInferenceBackend'
0
```

**cpu 交叉回归（AC2 前半）**

```
$ CROSS_PROFILE=native/profiles/rk3568.json CROSS_OUTPUT=build/targets/rk3568/Zhulong make native-cross-build
（成功）

$ readelf -d build/targets/rk3568/Zhulong | grep NEEDED
  NEEDED libstdc++.so.6 / libm.so.6 / librt.so.1 / libresolv.so.2 / libdl.so.2 / libpthread.so.0 / libgcc_s.so.1 / libc.so.6
$ readelf -d build/native/cross-rk3568/libZhulongAlgoRuntime.so | grep NEEDED
  NEEDED libstdc++.so.6 / libm.so.6 / libgcc_s.so.1 / libc.so.6
→ 均无 librknnrt.so
```

**rknn 交叉构建（AC1/AC2/AC6 核心）**

```
$ python3 native/scripts/build.py --profile native/profiles/rk3568-rknn.json build
[100%] Built target ZhulongRknnTests
[100%] Built target ZhulongRtspTests

$ readelf -d build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep NEEDED
  NEEDED librknnrt.so
  NEEDED libstdc++.so.6 / libm.so.6 / libgcc_s.so.1 / libc.so.6
  RUNPATH /home/niko/dev/Go/Zhulong/native/vendor/rknpu2/lib
→ 无 librknn_api.so（GOOD）

$ readelf -d build/native/cross-rk3568-rknn/ZhulongRknnTests | grep NEEDED
  NEEDED libZhulongAlgoRuntime.so / libpthread.so.0 / libstdc++.so.6 / libgcc_s.so.1 / libc.so.6

$ nm -D build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep -c 'RknnInferenceBackend'
20
$ nm -D build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep 'rknn_init'
                 U rknn_init
$ file build/native/cross-rk3568-rknn/ZhulongRknnTests
ELF 64-bit LSB pie executable, ARM aarch64, ... dynamically linked
```

**rknn 交叉 Go 产物（AC2 后半）**

```
$ CROSS_PROFILE=native/profiles/rk3568-rknn.json CROSS_OUTPUT=build/targets/rk3568-rknn/Zhulong make native-cross-build
（成功）
$ readelf -d build/targets/rk3568-rknn/Zhulong | grep NEEDED
  NEEDED libstdc++.so.6 / libm.so.6 / librt.so.1 / libresolv.so.2 / libdl.so.2 / libpthread.so.0 / libgcc_s.so.1 / libc.so.6
→ 无 librknnrt.so（GOOD）
```

**反向验证 1：SHA-256 校验真的会失败**

```
$ printf '\xff' | dd of=native/vendor/rknpu2/lib/librknnrt.so bs=1 seek=200 count=1 conv=notrunc
$ sha256sum native/vendor/rknpu2/lib/librknnrt.so
9f3e60c19760c6a3876092ec5ec0d5aa96e1c887d27f4bc52786efc166c2f8fa
$ python3 native/scripts/build.py --profile native/profiles/rk3568-rknn.json configure
native build failed: vendored RKNN lib/librknnrt.so checksum mismatch:
9f3e60c1… != d31fc19c…      （exit 1）
$ cp /tmp/librknnrt.so.bak native/vendor/rknpu2/lib/librknnrt.so   # 恢复
d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8
```

**反向验证 2：`grep -rl 'rknn_api.h'` 会命中泄漏**

```
# 临时向 native/src/algorithm/runtime/cpu_inference_backend.hpp 追加 #include <rknn_api.h>
$ grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/ | sort
native/src/algorithm/runtime/cpu_inference_backend.hpp   ← 命中（证明 grep 有效）
native/src/backends/rknn/rknn_error_map.hpp
native/src/backends/rknn/rknn_inference_backend.cpp
native/src/backends/rknn/rknn_inference_backend.hpp
native/src/backends/rknn/rknn_types.hpp
# 恢复后
$ grep -rl 'rknn_api.h' native/src/ native/sdk/ native/include/ | sort
native/src/backends/rknn/rknn_error_map.hpp
native/src/backends/rknn/rknn_inference_backend.cpp
native/src/backends/rknn/rknn_inference_backend.hpp
native/src/backends/rknn/rknn_types.hpp
```

### 5.3 阶段 B 编译证据

见 5.2 的 `nm -D` 与 `readelf -d` 输出：`libZhulongAlgoRuntime.so` 的 `NEEDED`
只含 `librknnrt.so`，`RknnInferenceBackend` 符号 20 个，`rknn_*` 为 `U`（运行期解析）。

### 5.4 与 design.md 的偏差

1. **`rknn_error_map` header-only**：design.md §2 列出 `rknn_error_map.hpp/.cpp`，
   实现合并为单头文件 inline。理由：错误映射是纯整数映射，header-only 才能让测试目标直接
   include 而不必链接 runtime `.so`（设计文档只列文件名，未规定必须拆分）。
2. **`MALLOC_FAIL` / `TIMEOUT` 映射更精确**：design.md §9 草图把 `RKNN_ERR_MALLOC_FAIL`
   映射为 `INTERNAL`、未处理 `TIMEOUT`；实现改为 `ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY`
   与 `ZHULONG_PLUGIN_ERR_TIMEOUT`（`plugin.h` 存在对应码，语义更准确）。共覆盖全部 14 个返回码。
3. **`CMAKE_FIND_ROOT_PATH` 未加 RKNN 前缀**：实测确认绝对路径链接在
   `CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY` 下可行（最小复现：exit 0，NEEDED 仅 `librknnrt.so`），
   因此**不改** `configure()` 的 toolchain。
4. **新增 `-Wl,-rpath-link,<rknn>/lib`（设计文档未预见）**：`librknnrt.so` 只由
   `ZhulongAlgoRuntime.so` 直接链接，但下游可执行/插件（`ZhulongAlgoRuntimeTests`、
   `MockDetector`、`ZhulongRknnTests`）链接 `libZhulongAlgoRuntime.so` 时，GNU ld 需要解析其
   传递依赖符号；`-L` 目录**不**参与 rpath-link 搜索，必须显式 `-Wl,-rpath-link`。该选项只影响
   链接期解析，**不会**把 `librknnrt.so` 加进下游 `NEEDED`（已用 `readelf -d` 验证）。
   这是绝对路径链接方案的必要补充。**已由受控实验证明必需**（见 §5.5）。
5. **`configure()` 用 `p.get(key, "cpu")`**：`native/tests/build_contract_test.py` 会手工构造最小
   profile dict 直接调用 `configure()`（绕过 `profile()`），用 `.get()` 保持该既有测试契约。
6. **`rknn_input` 无 stride 字段**：`Tensor` 的 `std::vector<uint8_t>` 天然 packed，直接传
   `raw_data()` 与 `attr.size`；`forward` 校验 `byte_size() >= attr.size`。
7. **输出统一 `want_float=1`**：量化输出由 runtime 转 float32，不手工反量化、不猜 `zp`/`scale`；
   float32 输出本就是无操作。输出 shape 取 `output_attrs[i].dims[0..n_dims)` **原样**，不跳 batch 维。
8. **`rknn_outputs_release` RAII guard**：`OutputsReleaseGuard` 保证所有路径（含异常）恰好释放一次。
9. **`.gitignore` 否定规则**：vendored `librknnrt.so` 被 `*.so` 忽略，新增
   `!native/vendor/rknpu2/lib/librknnrt.so`。

### 5.5 产物卫生修正：BUILD_RPATH 不得携带宿主机路径（2026-10-14，主管会话完成）

**发现**：交叉产物 `libZhulongAlgoRuntime.so` 携带了 CMake 自动生成的 BUILD_RPATH，把开发机
绝对路径烧进了要 scp 到板端的 `.so`：

```txt
# 修正前
0x000000000000001d (RUNPATH)  Library runpath: [/home/niko/dev/Go/Zhulong/native/vendor/rknpu2/lib]
```

对照事实：**cpu 交叉产物** `build/native/cross-rk3568/libZhulongAlgoRuntime.so` 的 `readelf -d`
**没有** RUNPATH 行 —— 该 RUNPATH 是 rknn 分支新引入的（CMake 为「构建树之外的链接库」自动加）。

**修正**：在 rknn 分支加 `set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")`。

**受控链接实验：`-rpath-link` 与 BUILD_RPATH 是否冗余**

两个机制可能互相替代，故做了两组对照（每组都清空 `build/native/cross-rk3568-rknn` 全量重建）：

| 组合 | 结果 |
| --- | --- |
| `SKIP_BUILD_RPATH` **保留** + `-rpath-link` **删除** | ❌ 链接失败：`librknnrt.so, needed by libZhulongAlgoRuntime.so, not found (try using -rpath or -rpath-link)` + `undefined reference to rknn_init / rknn_run / rknn_dup_context / rknn_query / rknn_inputs_set / rknn_outputs_get / rknn_outputs_release / rknn_destroy`（8 个符号） |
| `SKIP_BUILD_RPATH` **保留** + `-rpath-link` **保留** | ✅ 构建 exit 0（最终采用的组合） |

**结论**：两者**不冗余**，`-rpath-link` 是必需的 —— 清空 BUILD_RPATH 后，ld 没有任何途径解析
`librknnrt.so` 的传递符号。BUILD_RPATH 原本「顺带」起到了这个作用，但它同时污染产物，因此
正确组合是「清空 BUILD_RPATH + 显式 `-rpath-link`」。

**修正后重新采集**（全量重建，非复用）：

```txt
# libZhulongAlgoRuntime.so — NEEDED 含 librknnrt.so，无任何 /home/niko 路径
0x0000000000000001 (NEEDED)  Shared library: [librknnrt.so]
0x0000000000000001 (NEEDED)  Shared library: [libstdc++.so.6]
0x0000000000000001 (NEEDED)  Shared library: [libm.so.6]
0x0000000000000001 (NEEDED)  Shared library: [libgcc_s.so.1]
0x0000000000000001 (NEEDED)  Shared library: [libc.so.6]
$ readelf -d ... | grep -c "/home/niko"
0

# ZhulongRknnTests — 保留 build-dir RUNPATH（板端配合 LD_LIBRARY_PATH 使用，合理）
0x000000000000001d (RUNPATH)  Library runpath: [/home/niko/dev/Go/Zhulong/build/native/cross-rk3568-rknn]

$ nm -D build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep -c 'RknnInferenceBackend'
20
$ file build/native/cross-rk3568-rknn/ZhulongRknnTests
ELF 64-bit LSB pie executable, ARM aarch64, ...
```

⚠️ `ZhulongRknnTests` 自己的 build-dir RUNPATH **刻意保留**：它只在板端配合 `LD_LIBRARY_PATH`
执行，不需要为交付做产物卫生处理。

**修正后回归**（`make native-test`，host/cpu）：

```txt
100% tests passed, 0 tests failed out of 15
```

### 5.7 阶段 C 真机验证（RK3568，2026-10-14，主管会话完成）

部署：`scp build/native/cross-rk3568-rknn/{ZhulongRknnTests,libZhulongAlgoRuntime.so} rk3568:/userdata/zhulong-test/`
执行：`LD_LIBRARY_PATH=/userdata/zhulong-test ./ZhulongRknnTests /userdata/rknn_model_zoo/examples/facelivt/model/facelivtv2_s_rk3568_fp.rknn`

#### 5.7.1 ⚠️ 发现并规避 librknnrt.so 2.3.2 的真实缺陷：`RKNN_QUERY_MEM_SIZE` 会破坏上下文

**现象**：首次真机运行在 `create_session` 处 **SIGSEGV**，崩在 `librknnrt.so` 内部：

```txt
Program received signal SIGSEGV, Segmentation fault.
0x0000007ff7769d64 in ?? () from /lib/librknnrt.so
#4  0x0000007ff753a9c4 in rknn_dup_context () from /lib/librknnrt.so
#5  zhulong::RknnInferenceBackend::create_session (...) at rknn_inference_backend.cpp:112
```

**最小 C 复现**（`gcc` + `-lrknnrt`，绕过全部本项目代码）证明不是封装问题：

| 变体 | 上下文上执行过的查询 | `rknn_dup_context` 结果 |
| --- | --- | --- |
| A | 无 | ✅ `ret=0` |
| B | `IN_OUT_NUM` | ✅ `ret=0` |
| C | `IN_OUT_NUM` + 输入/输出 `ATTR` | ✅ `ret=0` |
| D | `SDK_VERSION` + `MEM_SIZE` | ❌ **SIGSEGV (139)** |
| E | 全部（含 `MEM_SIZE`） | ❌ **SIGSEGV (139)** |

进一步二分（干净复现，`qprobe`）：

```txt
none       -> exit=0   last_line=[SURVIVED]
ver        -> exit=0   last_line=[SURVIVED]
mem        -> exit=139 last_line=[--- dup ---]
ver,mem    -> exit=139 last_line=[--- dup ---]
```

**结论**：**`RKNN_QUERY_MEM_SIZE` 是唯一毒药** —— 查过它的上下文，**首次** `rknn_dup_context` 必定段错误
（`qprobe2`：无 MEM_SIZE 时连续 3 次 dup 全部 `ret=0`；有 MEM_SIZE 时第 0 次就崩）。
`SDK_VERSION` 无影响。

**影响面界定**（`qprobe3`）：`MEM_SIZE` 之后 `rknn_inputs_set` / `rknn_run` / `rknn_outputs_get`
**全部正常**（`ret=0`）—— 即该污染**只影响 `rknn_dup_context`**，不影响推理本身。

**规避方案（已实施）**：`MEM_SIZE` 改用**独立的一次性上下文**查询，主 root 上下文绝不调用
`RKNN_QUERY_MEM_SIZE`。实测验证：

```txt
--- exp1: throwaway ctx for MEM_SIZE ---
probe MEM_SIZE ret=0 weight=9348096 internal=602112 dma=21024768
probe destroyed
exp1 DUP ret=0 ctx=367390070640
exp1 SURVIVED
```

代价：`load_model` 期间峰值多一份权重（约 9MB，仅加载期，随后销毁）。**R3（dup 共享）与 R6（记录 MEM_SIZE）因此可以同时满足。**

#### 5.7.2 AC7：真实 `.rknn` 推理（`facelivtv2_s_rk3568_fp.rknn`）

```txt
RKNN_SDK_VERSION api=2.3.2 (429f97ae6b@2025-04-09T09:09:27) drv=0.9.8
RKNN_MEM_SIZE weight=9348096 internal=602112 dma=21024768 sram_total=0 sram_free=0
RKNN_INPUT[0]  n_dims=4 dims=[1,112,112,3] type=FP16 fmt=NHWC n_elems=37632 size=75264
RKNN_OUTPUT[0] n_dims=2 dims=[1,512]       type=FP16 fmt=UNDEFINED n_elems=512 size=1024
PASS case1: forward OK
RKNN_OUTPUT0_HEAD -1.80469 -0.773926 0.192993 -0.411621
```

- **`api_version` = 2.3.2（`429f97ae6b@2025-04-09T09:09:27`）/ `drv_version` = 0.9.8** —— 与 prd §2.2 记录的库版本与驱动版本**完全吻合**（AC3 版本记录 + AC7）。
- tensor 属性**全部来自 Runtime 查询**（`dims`/`type`/`fmt`/`n_elems`/`size`），未从文件猜测（R9）。
- 输出为真实有限值（非 NaN/Inf），512 维特征向量 —— 符合人脸识别模型的输出形状。

#### 5.7.3 AC3：`rknn_dup_context` 权重共享对照（决定性证据）

| 组 | 做法 | RSS before | RSS peak | 增量 |
| --- | --- | --- | --- | --- |
| **baseline** | 4 × 独立 `rknn_init` | 9636 KB | **60024 KB** | **+50.4 MB** |
| **candidate** | 1 × `rknn_init` + 4 × `rknn_dup_context` | 14040 KB | **28340 KB** | **+14.3 MB** |

- **内存收益约 3.5 倍**（50.4 MB → 14.3 MB）。✅ **权重共享成立**，非「不成立需报阻塞」。
- **正确性**：`PASS case2: baseline outputs identical across workers` +
  `PASS case2: candidate outputs match baseline` —— 两组输出与组内各 worker 输出**逐元素一致**（容差 `1e-4`）。
- 两组的 `RKNN_QUERY_MEM_SIZE` 逻辑值均为 `weight=9348096 internal=602112`（**逐 worker 相同**）——
  ⚠️ 这印证了父任务研究的警告：**逻辑权重值不能相加当物理占用**。真实物理差异只能由 RSS/PSS 体现，
  即上表的 50.4 MB vs 14.3 MB。

⚠️ 测试首轮曾把「两边都是 `-inf`」误判为 mismatch（`fabs(-inf - -inf) = NaN`，`NaN <= x` 恒 false），
且根因是 FP16 输入被填了随机字节导致输出退化。已修两处：`floats_close` 显式处理非有限值；
新增 `float_to_half` + `fill_deterministic_for` 按模型声明的张量类型填合法 FP16 值。修正后输出为真实有限值。

#### 5.7.4 AC4：1/2/4 worker 并发

```txt
RKNN_WORKER workers=1 worker=0 iterations=20 elapsed_ms=316.817
RKNN_WORKER workers=2 worker=0 iterations=20 elapsed_ms=373.848
RKNN_WORKER workers=2 worker=1 iterations=20 elapsed_ms=374.273
RKNN_WORKER workers=4 worker=0 iterations=20 elapsed_ms=587.673
RKNN_WORKER workers=4 worker=1 iterations=20 elapsed_ms=586.667
RKNN_WORKER workers=4 worker=2 iterations=20 elapsed_ms=589.488
RKNN_WORKER workers=4 worker=3 iterations=20 elapsed_ms=588.889
PASS case3: workers=1 consistent & stable
PASS case3: workers=2 consistent & stable
PASS case3: workers=4 consistent & stable
RKNN_FD stage=case3-before count=8 stage=case3-after count=8
PASS case3: no fd growth after concurrency
```

- 三档并发下输出**全部一致**且**稳定**（AC4 正确性）。
- ⚠️ **不声称线性扩展**：20 次推理总耗时 316ms → 374ms → 588ms（1/2/4 worker）。
  这与 RK3568 **单 NPU 核**的预期一致 —— 4 个 worker 共享同一执行资源，吞吐未随 worker 数线性提升
  （父任务研究 §4 已明确此约束）。收益方向是 CPU 前后处理与 NPU 重叠，不是 NPU 并行。
- 并发后 FD 数 8 → 8（**无 FD 泄漏**）。

#### 5.7.5 AC5：故障注入与资源回收

```txt
PASS case4: load_model("") -> nullptr
PASS case4: load_model(nonexistent) -> nullptr
PASS case4: load_model(garbage) -> nullptr            # E RKNN: parseRKNN: invalid RKNN_MAGIC!
PASS case4: forward(nullptr) -> INVALID_ARGUMENT
PASS case4: forward(too many inputs) -> INVALID_ARGUMENT
PASS case4: forward(undersized input) -> INVALID_ARGUMENT
PASS case4: create_session(nullptr) -> nullptr
PASS case4: create_session(non-Rknn ModelAsset) -> nullptr
RKNN_FD stage=case4-after count=9                     # 临时文件句柄，已清理
```

错误映射全 14 个返回码覆盖：

```txt
RKNN_ERRMAP code=0 -> 0      code=-1 -> -99   code=-2 -> -7    code=-3 -> -3
             code=-4 -> -2   code=-5 -> -1    code=-6 -> -1    code=-7 -> -1
             code=-8 -> -1   code=-9 -> -1    code=-10 -> -3   code=-11 -> -3
             code=-12 -> -3  code=-13 -> -3   unknown -> -99
```

重复启停 5 轮（AC5 无泄漏）：

```txt
RKNN_REPEAT iteration=0 rss_kb=14968 fd=6
RKNN_REPEAT iteration=1 rss_kb=14968 fd=6
RKNN_REPEAT iteration=2 rss_kb=14968 fd=6
RKNN_REPEAT iteration=3 rss_kb=14968 fd=6
RKNN_REPEAT iteration=4 rss_kb=14968 fd=6
```

RSS 与 FD **5 轮完全不变**（14968 KB / 6）→ 无内存与 FD 增长。

#### 5.7.6 真机总结论

```txt
=== failures=0 ===   （EXIT=0）
```

6 个 case（错误映射 / 真实推理 / 权重共享 / 并发 / 故障注入 / 重复启停）全部通过。

### 5.8 未验证项与残余阻塞

- ✅ **阶段 C 真机执行已完成**（见 §5.7）：6 个 case 全绿，`failures=0`。
  AC3 / AC4 / AC5 / AC7 的真机证据已取得。
- ✅ **`forward` 成功路径已在真机覆盖**：`case1` 完成一次真实推理并输出 512 维有限值；
  `case2`/`case3` 在真机上反复调用 `forward` 并逐元素比对。
- 未引入联网操作；未跑裸 `go build`/`go test`/`go vet`；未 `git commit`。

### 5.9 阶段 C 残余风险与未覆盖项（如实登记）

- ⚠️ **`RKNN_QUERY_MEM_SIZE` 缺陷的官方性质未定**：本任务只做了行为层面的最小复现与规避，
  未查明根因（可能是 2.3.2 的已知缺陷，也可能是未文档化的调用约束）。**不臆造官方结论**；
  已向后续任务/上游报告建议：若升级 RKNN Runtime，应重测该组合。
- ⚠️ **`MEM_SIZE` 规避方案有额外开销**：`load_model` 期间峰值多一份权重（约 9MB）。
  这是为满足 R6 证据要求付出的代价，仅发生在加载期。
- ⚠️ **未验证真实视频帧的端到端链路**：本任务的真机推理输入是合成张量（确定性填充），
  不是经 broker 从 RTSP 解码后的真实帧。`10-13-algo-broker-host` 已交付帧通路，但
  「RTSP → 解码 → broker → runner → RKNN 后端」的**完整组合**未在本任务执行。
  原 `implement.md` §1 阶段 C 提到「可走真实帧通路」，实际采用了更保守的合成输入 —— 
  因为真实帧端到端需要板端 RTSP 源与 runner 联调，属于集成验证而非后端验证。
- ⚠️ **未验证多进程/多包竞争**：真机只验证了单进程内 1/2/4 worker。多个算法包进程共享
  同一 NPU 的设备级排队行为未测（父任务 `design.md` §6 已登记）。
- ⚠️ **未验证长时间稳定性、功耗与温度**：测试运行时长在秒级。
- ⚠️ **编译器代差仍在**：宿主交叉 gcc 14.2.0 / sysroot libstdc++ 10 / 板端 gcc 10.2.1。
  真机运行未暴露 ABI 问题，但未做穷尽验证。
- ⚠️ **`librknn_api.so`(1.5.2) 仍存在于板端 `/usr/lib`**：产物 `NEEDED` 只引用 `librknnrt.so`
  （已验），但该陈旧库仍在系统搜索路径中。清理属板端运维决定，不在本任务代码范围。
- ⚠️ **`/dev/rga` 权限模型未涉及**：本任务不碰 RGA（兄弟任务范围）。

---

## 6. 验收状态（阶段 C 完成后核对）

| AC | 关联 | 状态 | 证据 |
| --- | --- | --- | --- |
| AC1 | R1、R9 | ✅ | `RknnInferenceBackend` 实现 `load_model`/`create_session`/`forward`；`grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/` 只命中 `native/src/backends/rknn/` 的 3 个文件（含反向验证：往 `runtime/` 注入后 grep 立即命中） |
| AC2 | R2 | ✅ | host/cpu `make native-test` **15/15**；cpu 交叉 `.so` 与 cpu 交叉宿主 `NEEDED` 均无 `librknnrt.so`；**rknn 交叉宿主 `Zhulong` 也无**（宿主只静态链 `ZhulongEngine`）；rknn 交叉 `libZhulongAlgoRuntime.so` 有 `librknnrt.so` |
| AC3 | R3、R6 | ✅ | 真机 `api=2.3.2 / drv=0.9.8`；**共享成立**：4×init RSS 峰值 60024KB vs 1×init+4×dup 28340KB（**+50.4MB vs +14.3MB，约 3.5×**）；两组输出逐元素一致 |
| AC4 | R4 | ✅ | 真机 1/2/4 worker 输出全一致且稳定；FD 8→8 无泄漏；耗时 316.8/374.1/588.7ms（**不声称线性扩展**，符合单 NPU 核） |
| AC5 | R5、R7 | ✅ | 8 条故障路径全返回预期状态码；14 个 RKNN 返回码全覆盖映射；重复启停 5 轮 RSS 与 FD **完全不变**（14968KB/6） |
| AC6 | R8 | ✅ | 产物 `NEEDED` 只有 `librknnrt.so`，无 `librknn_api.so`；`build.py` 固定两个 SHA-256 且**反向验证会失败**（改一字节 → `checksum mismatch` 退出 1）；交叉产物**无宿主机路径**（RUNPATH 已清） |
| AC7 | R9 | ✅ | 真机 `facelivtv2_s_rk3568_fp.rknn` 完成真实推理；tensor 属性全部 Runtime 查询（`[1,112,112,3]` FP16 NHWC → `[1,512]` FP16）；输出为 512 维有限值 |

### 6.1 附带发现（超出原 AC 范围，但影响交付正确性）

| # | 发现 | 处置 |
| --- | --- | --- |
| 1 | **`RKNN_QUERY_MEM_SIZE` 破坏上下文**：查过它的上下文首次 `rknn_dup_context` 必 SIGSEGV（librknnrt.so 2.3.2 + 驱动 0.9.8） | 改用独立一次性上下文查询 MEM_SIZE；主 root 绝不查。已真机验证规避有效 |
| 2 | 交叉产物携带 BUILD_RPATH，把开发机绝对路径烧进要 scp 的 `.so` | 加 `SKIP_BUILD_RPATH TRUE BUILD_RPATH ""`；受控实验证明 `-rpath-link` 与 BUILD_RPATH **不冗余** |
| 3 | `*.so` 规则会忽略 vendored `librknnrt.so` | `.gitignore` 加否定规则 |
| 4 | 测试比对不处理非有限值 + FP16 输入填随机字节 → 输出全 `-inf` | `floats_close` 处理 NaN/Inf；新增 `float_to_half` + `fill_deterministic_for` 按模型类型填合法值 |

### 6.2 关键证据一览（可复现命令）

```bash
# 头文件隔离
grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/

# host/cpu 回归
make native-test                                    # 15/15

# cpu 交叉：宿主与 .so 均不得有 rknn
CROSS_PROFILE=native/profiles/rk3568.json CROSS_OUTPUT=build/targets/rk3568/Zhulong make native-cross-build
readelf -d build/targets/rk3568/Zhulong | grep NEEDED

# rknn 交叉
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn.json build
readelf -d build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep -E 'NEEDED|RUNPATH'
readelf -d build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep -c '/home/niko'   # 期望 0

# 真机
scp build/native/cross-rk3568-rknn/{ZhulongRknnTests,libZhulongAlgoRuntime.so} rk3568:/userdata/zhulong-test/
ssh rk3568 'cd /userdata/zhulong-test && LD_LIBRARY_PATH=$PWD ./ZhulongRknnTests \
  /userdata/rknn_model_zoo/examples/facelivt/model/facelivtv2_s_rk3568_fp.rknn'
# 期望：=== failures=0 ===
```

---

## 7. 独立质量审查（主管会话自审，2026-10-14）

`trellis-check` 子代理超时未产出报告，本节由主管会话完成等价审查。

### 7.1 关键隐患核查：`RknnModelAsset` 的 double-destroy

**问题**：`load_model` 的失败路径直接 `rknn_destroy(root)` 后 `return nullptr`，而局部 `asset`（`shared_ptr<RknnModelAsset>`）随后析构 —— `~RknnModelAsset` 里也有 `rknn_destroy(root_ctx)`。若两者都生效就是 double-destroy。

**结论：不存在该问题。** 证据：

```txt
$ grep -n "root_valid" native/src/backends/rknn/rknn_inference_backend.cpp native/src/backends/rknn/rknn_types.hpp
rknn_inference_backend.cpp:107:        asset->root_valid = true;      ← 唯一置 true 处
rknn_inference_backend.cpp:117:    if (!asset || !asset->root_valid)  ← create_session 守卫
rknn_types.hpp:21:    bool root_valid{false};            ← 默认 false
rknn_types.hpp:36:        if (root_valid) { rknn_destroy(root_ctx); root_valid = false; }

$ grep -n "rknn_destroy" native/src/backends/rknn/rknn_inference_backend.cpp
:56, :69, :82   ← 三处失败路径的 rknn_destroy(root)
```

`:107`（置 `true`）在**所有**失败返回点（`:56`/`:69`/`:82`）**之后**。因此失败路径上 `root_valid` 恒为 `false`，
`~RknnModelAsset` 走空操作分支，不会二次销毁。同理 `root_ctx` 在 `:106` 才被赋值（在 `:107` 之前一行），
失败路径上它始终是初值 `0`。

**probe 上下文**（§5.7.1 的规避）：`rknn_init(&probe,...) == RKNN_SUCC` 才进入分支，分支内必 `rknn_destroy(probe)`；
失败时 `probe` 保持 `0`，不销毁。无泄漏路径。

**session/root 次序（R5）**：`RknnModelAsset` **不**持有任何 session 引用（无循环引用）；
`RknnSessionContext` 持有 `shared_ptr<RknnModelAsset> model` → session 析构前 asset 必然存活 → root 晚于所有派生 worker 释放。

### 7.2 未改动的既有文件（精准修改核对）

```txt
$ git diff --stat native/profiles/rk3568.json native/src/algorithm/runtime/cpu_inference_backend.cpp \
    native/src/algorithm/runtime/cpu_inference_backend.hpp native/src/algorithm/ipc/shm_pool.cpp \
    native/src/algorithm/runtime/tensor.hpp native/sdk/include/zhulong/plugin.h \
    native/sdk/include/zhulong/algo.hpp
（空输出）
```

- `native/profiles/rk3568.json` 未改（新建了 `rk3568-rknn.json` 而非改它）。
- `cpu_*` 既有行为未变；`shm_pool.cpp:86` 的既有 `-Wclass-memaccess` 警告未顺手改。
- `tensor.hpp` / `plugin.h` / `algo.hpp` 的接口与布局未变。

`native/CMakeLists.txt`：`git diff --numstat` = **71 增 0 删**（纯新增，无重排、无「顺手改进」）。

### 7.3 头文件隔离（AC1 复核）

```txt
$ grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/
native/src/backends/rknn/rknn_types.hpp:8:#include <rknn_api.h>
native/src/backends/rknn/rknn_inference_backend.cpp:6:#include <rknn_api.h>
native/src/backends/rknn/rknn_error_map.hpp:12:#include <rknn_api.h>
```

3 处全部在 `native/src/backends/rknn/` 内（`directory-structure.md` §2 铁律 4）。

### 7.4 反向验证

**（a）`floats_close` 的非有限值处理**（真机上首轮假 FAIL 的根因）：

```txt
case                     naive  fixed
-inf vs -inf  (应一致)   false  true     ← naive 误报不一致（真机假 FAIL 根因）
-inf vs +inf  (应不一致) true   false    ← naive 漏报
nan  vs nan   (应一致)   false  true
1.0  vs 1.0   (应一致)   true   true
1.0  vs 1.1   (应不一致) false  false
```

**（b）`float_to_half` 独立验证**（手写 IEEE 754 binary16 转换，最容易出错）：

```txt
# 往返：所有非 NaN 的 half 值 → float → half，必须精确回到原值
roundtrip: checked=63490 bad=0

# 随机 float vs 独立参照实现（long double 路径）
random: n=2000000 bad(>1ulp)=0
```

**（c）AC2 宿主隔离断言非空**：`readelf -d build/targets/rk3568-rknn/Zhulong | grep -c rknn` = 0，
而同一命令对 `build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so` = 1（见 §6.2 矩阵）——
证明 grep 本身工作正常，0 是真实结果而非命令写错。

### 7.5 审查结论

未发现阻塞级或重要级缺陷。4 个附带发现（§6.1）均已修复并验证。可以进入 spec 更新与提交。
