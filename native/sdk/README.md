# Zhulong 算法包 SDK

这里放的是**算法包作者**需要的一切：头文件、静态库、CMake 包配置，外加一个自查工具。
装好之后，算法包工程**不需要**本仓库即可构建、测试与打包。

```bash
make sdk-install                              # → build/sdk/prefix
make sdk-install BUNDLE_RUNTIME=1             # 连 onnxruntime 依赖闭包一起拷进前缀（可整体搬迁/离线）
make sdk-install SDK_PREFIX=/opt/zhulong-sdk  # 自定义位置（绝不装到系统路径）

make sdk-dist                                # → build/packages/zhulong-algo-sdk-<ver>-<arch>-<target>.tar.gz
make sdk-dist BUNDLE_RUNTIME=1               # 同上，但前缀自带运行时（可搬到无网/异构机器）
make sdk-clean-check                         # 干净容器里从零构建两个包 + 脚手架 round-trip
```

`sdk-dist` 就是「把安装好的前缀原样打包」——不另建文件清单：第二份清单必然和 `install()`
规则漂移，而漂移的表现是「第三方缺个头/缺个 .a」却没人知道该改哪份。

⚠️ **镜像基线**：前缀里的 `libZhulongAlgoRuntime.a` 与 bundled `libonnxruntime.so` 是在开发机上
编译的二进制，符号版本不向下兼容。目标机/镜像必须 **≥ 开发机**那一代发行版，否则链接期会报
`undefined reference to __isoc23_strtol@GLIBC_2.38` / `__cxa_call_terminate@CXXABI_1.3.15`
—— 那是镜像太旧，不是包的问题。

## 1. 前缀里有什么

```
<prefix>/include/zhulong/          公开头文件（plugin.h / algo.hpp / algo_main.hpp / runtime/ / platform/）
<prefix>/lib/libzhulong_algo_sdk.a 骨架（algo_main + IPC + 日志）
<prefix>/lib/libZhulongAlgoRuntime.a 算法运行库（推理后端、算子、Tensor/Image 等）
<prefix>/lib/cmake/ZhulongAlgoSDK/ CMake 包配置（导入 Zhulong::AlgoSDK / Zhulong::AlgoRuntime）
<prefix>/dev/stb/                  开发期资产（可选：devtest 读 jpg 用）
<prefix>/tools/algo_probe.py       协议自查工具（只用 Python 标准库）
<prefix>/tools/new_package.py      脚手架：一条命令生成可跑的包骨架
<prefix>/abi.json                  版本与能力声明（sdk_version / api_version / protocol）
```

`BUNDLE_RUNTIME=1` 时 `native/scripts/bundle_runtime.py` 会把 onnxruntime **及其 NEEDED 闭包**
（`libonnx` / `libonnx_proto` / `libprotobuf` / `libXNNPACK` / `libcpuinfo` / `libre2` / `libabsl_*` …）
一起拷进前缀，导入目标上带 `-Wl,-rpath-link` + `-Wl,-rpath` + `--disable-new-dtags`，因此整个前缀
可以直接搬到另一台机器（或容器镜像）里用。

只拷 `libonnxruntime.so` 是不够的：发行版构建的 ORT 不是自包含的，开发机上那些依赖恰好被 apt
装在系统路径里，于是「拷一个 .so」在干净容器里会当场翻车。glibc / libstdc++ / libgcc / 动态装载器
刻意**不**打包（必须来自目标系统）。有关闭 `--disable-new-dtags` 的原因也写在 CMake 配置里：
`DT_RUNPATH` 不沿依赖链继承，三层 NEEDED 只能找到第一层。

## 2. 从零起一个包

```bash
python3 <prefix>/tools/new_package.py my_detector --target onnx --sdk-prefix <prefix>
cd my_detector/onnx
ffmpeg -i sample.ppm sample.jpg     # manifest 要求 sample.jpg
make SDK_PREFIX=<prefix> && make test SDK_PREFIX=<prefix> && make verify SDK_PREFIX=<prefix>
```

生成的是**能跑通的最小真包**（空检测实现 → `count` 恒 0），不是示例片段：把 `detect()`
换成真实推理就是产品包。脚手架 round-trip 也是 `make sdk-clean-check` 的一步——SDK 少了
任何东西（头/库/CMake 变量）都会在那儿红。

## 3. 包工程长什么样

```cmake
cmake_minimum_required(VERSION 3.20)
project(my_algorithm LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)

find_package(ZhulongAlgoSDK 1.0 REQUIRED)

add_executable(my_algorithm
    src/main.cpp          # 只有 ZHULONG_ALGO_MAIN(...) 三行
    src/my_plugin.cpp     # 算法类本体
)
target_link_libraries(my_algorithm PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)
```

```cpp
// src/main.cpp
#include <zhulong/algo_main.hpp>
#include "my_plugin.hpp"

ZHULONG_ALGO_MAIN(MyDetectorPlugin, zhulong::sdk::DetectionResultAdapter, "MyDetector", "1.0.0")
```

要点：

- **可执行文件名必须等于 `manifest.json` 的 `id`** —— 宿主按这个约定找入口。
- 算法类实现 `init` / `create_session` / 以及对应任务类型的方法（`detect` / `classify` /
  `detect_persons` / `detect_vehicles`），方法签名都带 `zhulong::TaskContext&`。
- 包**自带**推理后端：`zhulong::Model::load` 的路径相对**包根**（`models/` 与可执行文件同级），
  运行时宿主把 CWD 设成包根，包也可以自己按 `/proc/self/exe` 定位。
- 不要在包里 `dlopen` 任何东西，也不要导出自己的 C ABI：宿主不装载包，只 exec 它。

## 4. 三个协议（`abi.json` 里逐条声明）

| 协议 | 要求 | 命令 |
| :--- | :--- | :--- |
| `verify` | **必须** | `<entry> --verify --sample <netpbm>`：跑一次完整生命周期，最后一行 JSON 报告 |
| `broker_consumer` | **必须** | `<entry> --broker <sock> [--broker …] [--name <n>] [--algorithm-id <id>]`：连帧 broker 长驻消费 |
| `extract_feature` | **可选** | `<entry> --extract-feature`：stdin 帧流 → stdout 一行 JSON 特征 |

`extract_feature` 是**底库登记**用的：宿主拿一张照片换一个 512 维归一化特征。实现方式是在包内
直接编译的源文件里定义

```c
zhulong_status_t zhulong_face_extract_feature(const uint8_t* pixels, int32_t width, int32_t height,
                                             int32_t stride, int32_t format,
                                             float* out_features, uint32_t* feature_dim);
```

骨架用**弱符号**引用它：定义了就被调用，没定义就如实返回 `UNSUPPORTED`（进程级退出码 5），
**不是构建错误**。所以人脸类包实现它才能被宿主用于底库登记，其它任务类型可以不实现。

⚠️ 定义必须放在包内**直接编译**的源文件里：GNU ld 不会为满足弱未定义引用去抽取静态归档成员，
把定义搬进 `.a` 会让能力静默消失（宿主侧表现为「底库登记功能没了且没有错误日志」）。

帧协议（小端）：

| offset | size | 字段 |
| :--- | :--- | :--- |
| 0 | u32 | `magic` = `"ZLFE"`（0x45464C5A） |
| 4 | u32 | `request_id`（响应必须回显） |
| 8 | u32 | `width` |
| 12 | u32 | `height` |
| 16 | u32 | `stride`（≥ `width*3`） |
| 20 | u32 | `format`（1 = BGR24，2 = RGB24） |
| 24 | u32 | `payload_len`（= `stride*height`） |
| 28 | u8[] | 裸像元 |

失败码：`NO_FACE_DETECTED` / `INVALID_INPUT` / `UNSUPPORTED` / `MODEL_UNAVAILABLE` / `INTERNAL`。
**帧级错误不要退出进程** —— 一张坏图不该毁掉热进程。

## 5. 自查：`tools/algo_probe.py`

```bash
# 开发布局（可执行文件在 <pkg>/build/ 下）需要显式给包根
python3 <prefix>/tools/algo_probe.py --entry build/my_algorithm --pkg-dir . \
        --all --sample sample.ppm --image sample.ppm

# 安装布局（可执行文件就在包根）
python3 <prefix>/tools/algo_probe.py --entry ./my_algorithm --all --sample sample.ppm --image sample.ppm
```

它会校验：

- `--verify`：退出码 0、`status=passed`、`result_type` 与 `task_type` 一致；`sample_decoded=false`
  会明确警告「只证明了 ABI 生命周期」；
- `--extract-feature`：`feature_dim == 512`、向量 L2 范数 ≈ 1（未归一化的向量会污染整个底库）；
  非人脸类包如实返回 `UNSUPPORTED`（exit 5）时输出 `SKIPPED`，不算失败 —— 但把这种包配成宿主的
  `[person].extractor_algorithm`，底库登记就会失败。

图像只支持 netpbm（P6/P5）：包骨架不链接任何编解码器，JPEG/PNG 的解码责任在调用侧。
要拿 jpg 自查先转一次：`ffmpeg -i sample.jpg -pix_fmt rgb24 sample.ppm`。

## 6. 打包与安装

```bash
# 目录内必须有：manifest.json, sample.jpg, models/, <id> 可执行文件（+ 可选 config.schema.json）
tar -czf my_algorithm-x86_64-onnx.tar.gz manifest.json my_algorithm sample.jpg models/ config.schema.json
```

这套流程在干净容器里由 `native/scripts/sdk_clean_check.sh` 走一遍（只挂前缀与包目录、无仓库、
无宿主二进制）：`rm -rf build && make` → `make test` → `algo_probe.py --all`，并 grep 包文件里
是否残留指向仓库的相对路径。

宿主安装时会：解析 manifest → 校验平台 → 检查 `sample.jpg` → 解析并 `chmod 0755` 包根入口 →
校验 `config.schema.json` → 用**包内可执行文件**跑 `--verify`。归档里**不得**出现 `plugin.so`：
那是已下线的旧形态，会被明确拒绝（`ALGO_ENTRY_INVALID`）。
