# 算法包黑盒化（10-24-algo-package-blackbox-exe）基线采集记录

> 本文件记录算法包黑盒化任务（从 `dlopen .so` 迁移到独立可执行文件）在重大改造前的基线测量数据。
> 用于作为 AC3（行为等价）、AC9（特征提取等价）、AC16（包独立构建）等验收准则的比对基准。

---

## 0. 采集上下文说明（依据 D5 / implement 0.6）

- **采集时间**：2026-10-25
- **采集时 HEAD**：`3842359`（docs(task): 记录算法包黑盒化任务的设计变更（R11/R12/R13/R14））
- **前置生效状态**：
  - 阶段 1.0（R14 / commit `7aeb3bb`）已先于本基线落地：`cpu_mock` 产品后端已拔除，桩后端已迁移至测试夹具（`native/tests/fixtures/stub_inference_backend.*`），`native/profiles/rk3568.json` 已改为 `rk3568-stub.json`，`TARGET_MODEL_EXT` 改由 `ZHULONG_INFERENCE_RKNN` 决定。
  - 本机构建环境：Linux x86_64，带本地 ONNX Runtime 依赖（`build/deps/onnxruntime`），当前默认后端为 `onnx`。
  - 当前测试状态：`make native-test` **28/28 全部通过**。

---

## 1. 改造前未定义符号与依赖基线（0.2 / AC1 对照）

### 1.1 未定义 `zhulong` 符号数（黑盒被捅穿证据）

```bash
nm -D --undefined-only build/native/host/libPlateRecognitionPlugin.so | grep -c zhulong
# 测量结果：3
nm -D --undefined-only build/native/host/libFaceRecognitionPlugin.so | grep -c zhulong
# 测量结果：9
```

明细清单：

- **`libPlateRecognitionPlugin.so` (3 个未定义符号)**：
  1. `_ZN7zhulong26get_default_vision_backendEv` (`zhulong::get_default_vision_backend()`)
  2. `_ZN7zhulong29get_default_inference_backendEv` (`zhulong::get_default_inference_backend()`)
  3. `_ZN7zhulong3nmsERKSt6vectorI12zhulong_bboxSaIS1_EEf` (`zhulong::nms(...)`)
- **`libFaceRecognitionPlugin.so` (9 个未定义符号)**：
  1. `_ZN7zhulong26get_default_vision_backendEv` (`zhulong::get_default_vision_backend()`)
  2. `_ZN7zhulong29get_default_inference_backendEv` (`zhulong::get_default_inference_backend()`)
  3. `_ZN7zhulong7runtime15GalleryRegistry8instanceEv` (`zhulong::runtime::GalleryRegistry::instance()`)
  4. `_ZN7zhulong8platform15DmaBufAllocator8allocateEm` (`zhulong::platform::DmaBufAllocator::allocate(unsigned long)`)
  5. `_ZN7zhulong8platform15DmaBufAllocator8instanceEv` (`zhulong::platform::DmaBufAllocator::instance()`)
  6. `_ZN7zhulong8platform9DmaBuffer10sync_startEm` (`zhulong::platform::DmaBuffer::sync_start(unsigned long)`)
  7. `_ZN7zhulong8platform9DmaBuffer8sync_endEm` (`zhulong::platform::DmaBuffer::sync_end(unsigned long)`)
  8. `_ZN7zhulong8platform9DmaBufferD1Ev` (`zhulong::platform::DmaBuffer::~DmaBuffer()`)
  9. `_ZNK7zhulong7runtime15GalleryRegistry8snapshotEv` (`zhulong::runtime::GalleryRegistry::snapshot() const`)

### 1.2 动态依赖与 RUNPATH

```bash
readelf -d build/native/host/libPlateRecognitionPlugin.so | grep -E "NEEDED|RUNPATH"
```

输出：
```text
 0x0000000000000001 (NEEDED)             Shared library: [libZhulongAlgoRuntime.so]
 0x0000000000000001 (NEEDED)             Shared library: [libstdc++.so.6]
 0x0000000000000001 (NEEDED)             Shared library: [libm.so.6]
 0x0000000000000001 (NEEDED)             Shared library: [libgcc_s.so.1]
 0x0000000000000001 (NEEDED)             Shared library: [libc.so.6]
 0x000000000000001d (RUNPATH)            Library runpath: [$ORIGIN]
```

```bash
readelf -d build/native/host/libZhulongAlgoRuntime.so | grep -E "NEEDED|RUNPATH"
```

输出：
```text
 0x0000000000000001 (NEEDED)             Shared library: [libonnxruntime.so.1.21]
 0x0000000000000001 (NEEDED)             Shared library: [libstdc++.so.6]
 0x0000000000000001 (NEEDED)             Shared library: [libm.so.6]
 0x0000000000000001 (NEEDED)             Shared library: [libgcc_s.so.1]
 0x0000000000000001 (NEEDED)             Shared library: [libc.so.6]
```

### 1.3 文件体积基线

```bash
ls -lh build/native/host/libZhulongAlgoRuntime.so build/native/host/libPlateRecognitionPlugin.so build/native/host/libFaceRecognitionPlugin.so build/native/host/zhulong_algo_runner
```

输出：
- `libZhulongAlgoRuntime.so`: 2.0MB
- `libPlateRecognitionPlugin.so`: 1.6MB
- `libFaceRecognitionPlugin.so`: 2.9MB
- `zhulong_algo_runner`: 2.2MB

---

## 2. 改造前验包推理基线（0.3 / AC3 对照）

### 2.1 直接传 `sample.jpg` 的行为（无独立解码器，合成帧回退）

```bash
./build/native/host/zhulong_algo_runner --verify-plugin build/native/host/libPlateRecognitionPlugin.so --sample packages/plate_recognition/sample.jpg
```

末行日志与 JSON 输出：
```json
{"zhulongLog":true,"level":"warn","module":"algorithm.runner","streamId":0,"message":"verify_sample_undecodable reason=unsupported_or_unreadable"}
{"status":"passed","sample":"packages/plate_recognition/sample.jpg","sample_decoded":false,"width":640,"height":480,"result_type":4,"count":0}
```

*说明*：宿主旧 runner 内部仅内置极简 PPM/PGM 解析器，不链接图像解码库。直接喂 JPEG 时回退为 640x480 的全黑合成帧，`sample_decoded: false`，因此 `count: 0`。

### 2.2 解码为 PPM 后的真实推理基线（关键锚点）

将 `packages/plate_recognition/sample.jpg` 经宿主标准流程（或 ffmpeg）解码为标准 RGB24 PPM：
```bash
ffmpeg -y -i packages/plate_recognition/sample.jpg -pix_fmt rgb24 /tmp/sample_plate.ppm 2>/dev/null
./build/native/host/zhulong_algo_runner --verify-plugin build/native/host/libPlateRecognitionPlugin.so --sample /tmp/sample_plate.ppm 2>/dev/null
```

输出：
```json
{"step":"start","plugin":"build/native/host/libPlateRecognitionPlugin.so"}
{"step":"abi_verified","name":"PlateRecognition","version":"1.0.0"}
{"status":"passed","sample":"/tmp/sample_plate.ppm","sample_decoded":true,"width":1106,"height":826,"result_type":4,"count":2}
```

- **基线锚点**：
  - `sample_decoded`: `true`
  - `width`: 1106, `height`: 826
  - `result_type`: 4（车辆/车牌结构化结果）
  - **`count`**: **2**（检出 2 处车牌）

---

## 3. 宿主侧人脸特征提取基线（0.4 / AC9 对照）

在 Go 宿主侧，通过 `dlopen` 加载 `libFaceRecognitionPlugin.so` 的测试执行耗时：

```bash
python3 native/scripts/build.py go test -race -count=1 -run TestExtractFaceFeature_ValidationAndSuccess ./internal/engine -v
```

执行结果：
```text
=== RUN   TestExtractFaceFeature_ValidationAndSuccess
--- PASS: TestExtractFaceFeature_ValidationAndSuccess (0.71s)
PASS
ok  	github.com/nikonikowuw/Zhulong/internal/engine	1.728s
```

- **特征提取单测执行时长**：0.71s（含模型冷加载 + 提取验证）
- **产物依赖**：依赖 `build/native/host/libFaceRecognitionPlugin.so` 的存在。

*注意*：RKNN 板端在硬件上的冷启动与模型加载时长在本机无法测量，显式标记为「**未测**」，由后续板端联调记录。

### 3.3 板端实测（2026-10-27，RK3568 实机，补上「未测」）

**硬件/软件基线**（`ssh rk3568`）：

```text
Linux linaro-alip 5.10.226 aarch64      Debian 11.11
librknnrt.so 2.3.2 (429f97ae6b@2025-04-09)   librockchip_mpp.so.1 / librga.so.2
/dev/mpp_service, /dev/dri/renderD128|129     内存 1953MB，温度 46°C（连续推理后）
```

**做法**：交叉构建 aarch64 SDK 前缀 → 前缀与 `packages/face_recognition/rk3568/` 一起拷到板端 →
**在板子上用板子自己的 g++ 10.2 编译包**（`make SDK_PREFIX=/tmp/sdk-rk3568`）。这比交叉编译包更强：
顺带证明 SDK 前缀在真设备上可用，且不需要宿主机工具链参与。

| 项 | 结果 |
| :--- | :--- |
| 包编译（板端 g++ 10.2 / cmake 3.25） | 通过（`face_recognition` + `face_recognition_devtest`，链接 GCC-14 交叉构建的 `.a` 无缺符号） |
| `make test`（sample.jpg → result.jpg） | **5 人 / 5 脸**，`cost_ms ≈ 246`（对比 x86 ONNX 130ms） |
| `--verify --sample <ppm>` | `{"status":"passed","sample_decoded":true,"width":640,"height":443,"result_type":3,"count":5}` |
| `--extract-feature`（三帧） | 512 维、`norm=1.000000`；两帧同图向量一致；全黑帧 `NO_FACE_DETECTED` 且**进程不退出** |
| `--broker /tmp/nope.sock` | `{"status":"failed","stage":"args",...}` exit 5（拒绝非法 socket 路径） |
| AC1 黑盒判据 | `nm -D --undefined-only \| grep -c zhulong` = **0**；`nm -D \| grep -c zhulong` = **0**；`ldd \| grep -ci zhulong` = **0** |
| 符号门禁 `check_plugin_symbols.py --entry` | 通过（不导出任何插件 C ABI） |
| NPU 真的在算 | 连续 3 次推理后 `/sys/kernel/debug/rknpu/load` = **18%**（CPU 回退不会有这个读数） |

⚠️ **跨平台特征不可混用**：同一张脸，onnx 档位向量 head 为 `[0.041, 0.011, 0.083]`，
rk3568 档位为 `[0.026, -0.062, 0.055]`。权重不同 → 度量空间不同。`[person].extractor_algorithm`
必须指向**与流式识别同平台**的人脸包，否则底库登记与识别永远匹配不上（且没有任何报错）。

---

## 4. 算法包构建寄生现状基线（0.5 / AC16 对照）

### 4.1 算法包目录缺乏独立构建文件

```bash
find packages -name 'CMakeLists.txt' | wc -l
# 测量结果：0
```

### 4.2 宿主构建树直接引用算法包源码

```bash
grep -n 'packages/' native/CMakeLists.txt | head -n 5
```

输出：
```text
366:    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/face_recognition.cpp
367:    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/matcher.cpp
368:    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/aligner.cpp
369:    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/detector.cpp
370:    ${CMAKE_CURRENT_SOURCE_DIR}/../packages/face_recognition/src/extractor.cpp
```

### 4.3 宿主构建生成包相关产物

```bash
find build/native/host -name '*Recognition*'
```

当前产出清单（改造后上述清单在宿主编译期必须为空）：
- `build/native/host/libFaceRecognitionPlugin.so`
- `build/native/host/libPlateRecognitionPlugin.so`
- `build/native/host/ZhulongFaceRecognitionPluginTests`
- `build/native/host/ZhulongPlateRecognitionPluginTests`
- `build/native/host/ZhulongFaceRecognitionOnnxTests`
