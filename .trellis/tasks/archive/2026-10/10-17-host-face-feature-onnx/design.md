# 技术设计：宿主引擎人脸特征提取接入 ONNX 真实推理与 TTL 自动休眠

## 1. 架构定位与权衡分析

### 1.1 核心问题与约束
Zhulong 系统的基石架构之一是「宿主单可执行文件，零外部 Node.js / Python 运行时依赖」。
在 `.trellis/spec/native/hardware-backends.md` 与 `CMakeLists.txt` 中明确规定：
> `宿主 libZhulongEngine.a 绝不直接链接 libonnxruntime.so`

此约束的原因在于：
1. **防止二进制膨胀与符号污染**：避免几十兆的 ONNX Runtime 动态库硬绑定在宿主二进制上；
2. **多硬件平台兼容**：在板端（如 RK3568）上，宿主使用硬件 VPU/NPU（MPP/RKNPU），如果静态强绑定 x86/ARM 通用 ORT，会导致可执行文件在板端因缺失动态库而直接无法启动；
3. **故障隔离**：算法模型或推理引擎故障不得阻断 Go 宿主（Web 服务、流媒体分发、数据库管理）的基本运行。

### 1.2 架构选择：通过插件导出 C ABI + 宿主动态加载（dlopen）
- 算法插件 `FaceRecognitionPlugin`（`libFaceRecognitionPlugin.so`）本身已经链接了 `ZhulongAlgoRuntime`（内含 ONNX Runtime 后端），并且完整实现了：
  - SCRFD 检测（`FaceDetector`）
  - Umeyama 5 点仿射对齐（`Aligner`）
  - 512 维 L2 归一化特征抽取（`FaceFeatureExtractor`）
- 在插件中导出纯 C ABI 函数 `zhulong_face_extract_feature` 与 `zhulong_face_extract_release`；
- 宿主引擎 `native/src/abi/engine.cpp` 内通过线程安全的 `FacePluginLoader` 管理器挂载该接口。

### 1.3 核心系统原则：Fail-Closed 闭环报错与 TTL 资源回收
1. **Fail-Closed 闭环报错**：
   - 若算法插件不可用、动态库丢失、或者模型文件缺失，系统**必须立即且显式地报错**（返回 `Zhulong_ERR_UNSUPPORTED`），让前端与调用方明确获知「算法包未就绪/提取失败」；
   - **严禁静默回退到伪数学特征**！防止向人员底库写入无效向量导致后续视频流识别永久失灵。
2. **TTL 自动休眠与内存释放（60s Idle Auto-Unload）**：
   - 首次请求时按需懒加载；
   - 在活跃期内进入热状态（单张推理 10~25ms），批量录入体验丝滑；
   - 录入完毕后，后台 Reaper 线程监控最后活跃时间。当空闲超过设定阈值（默认 60 秒）时，自动调用 `zhulong_face_extract_release` 并执行 `dlclose`，将 ~80MB 物理内存完全交还给操作系统；
   - 下次再次请求时自动重新唤醒加载。

```txt
Web 前端 (上传照片)
  ↓ HTTP POST /api/v1/persons/:id/faces
Go 业务层 (internal/person)
  ↓ ExtractFaceFeature()
Go CGO 边界 (internal/engine)
  ↓ C.Zhulong_engine_extract_face_feature()
Native Engine (native/src/abi/engine.cpp)
  ↓ FacePluginLoader
  ├─ [未加载 / 已休眠] → 唤醒：dlopen("libFaceRecognitionPlugin.so") + 启动 Reaper 线程
  ├─ [加载失败 / 缺失] → 显式返回 Zhulong_ERR_UNSUPPORTED ➔ Go 映射为 500 FEATURE_EXTRACT_FAILED
  └─ [热状态] → 刷新 last_active 时间戳
      ↓ 调用插件导出的 zhulong_face_extract_feature()
      ├─ 1. SCRFD onnx 检测人脸框与 5 点关键点
      ├─ 2. 选取最高置信度人脸（若未检出人脸返回 Zhulong_ERR_NOT_FOUND ➔ 映射为 400 NO_FACE_DETECTED）
      ├─ 3. Umeyama 仿射变换至 112×112 对齐 patch
      ├─ 4. facelivtv2-l onnx 前向推理 (512 维)
      └─ 5. 原地单位 L2 范数归一化 (||v|| = 1.0)
      ↓
输出 512 维 float32 真实向量 → 存入 SQLite person_faces (2048 字节)

【后台 Reaper 线程】
  └─ 监控活跃时间戳：若空闲超过 60 秒
      → 调用 zhulong_face_extract_release() 释放模型与 Session
      → 执行 dlclose(handle_) 彻底释放 ~80MB 内存
      → 回归 0 内存开销休眠态
```

---

## 2. 接口契约与数据流设计

### 2.1 插件导出 C ABI 契约

在 `packages/face_recognition/src/face_recognition.cpp` 与导出符号表中定义：

```c
#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief 人脸识别插件单次特征提取独立 C ABI
 */
ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_face_extract_feature(
    const uint8_t* pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float* out_features,
    uint32_t* feature_dim
);

/**
 * @brief 显式释放特征提取单例上下文持有的模型与推理会话（便于安全卸载）
 */
ZHULONG_PLUGIN_EXPORT void zhulong_face_extract_release();

#ifdef __cplusplus
}
#endif
```

### 2.2 宿主端 TTL 状态机与并发管理（`FacePluginLoader`）

在 `native/src/abi/engine.cpp` 中设计状态机：

```cpp
class FacePluginLoader {
public:
    using ExtractFn = zhulong_status_t (*)(const uint8_t*, int32_t, int32_t, int32_t, int32_t, float*, uint32_t*);
    using ReleaseFn = void (*)();

    static FacePluginLoader& instance() {
        static FacePluginLoader inst;
        return inst;
    }

    zhulong_status_t extract(const uint8_t* pixels, int32_t w, int32_t h, int32_t stride, int32_t fmt, float* out, uint32_t* dim);
    void shutdown(); // 引擎注销时调用

private:
    FacePluginLoader();
    ~FacePluginLoader();

    bool ensure_loaded_locked();
    void unload_locked();
    void reaper_loop();

    std::mutex mu_;
    std::condition_variable cv_;
    void* handle_{nullptr};
    ExtractFn extract_fn_{nullptr};
    ReleaseFn release_fn_{nullptr};

    std::chrono::steady_clock::time_point last_active_{};
    std::chrono::seconds idle_timeout_{60};
    bool stop_reaper_{false};
    std::thread reaper_thread_;
};
```

#### 时序与并发规约：
1. **调用排队与续租**：
   - 调用 `extract()` 时在 `mu_` 锁保护下执行 `ensure_loaded_locked()`；
   - 成功后更新 `last_active_ = std::chrono::steady_clock::now()`；
   - 释放锁执行推理（或在单次短锁下执行）；
2. **Reaper 定时巡检**：
   - Reaper 线程以 `cv_.wait_for(lock, idle_timeout_)` 周期唤醒；
   - 若 `handle_ != nullptr` 且当前时间距 `last_active_` 超过 `idle_timeout_`：
     调用 `release_fn_()`，随后调用 `dlclose(handle_)`，将 `handle_ = nullptr`、`extract_fn_ = nullptr`，清空状态；
3. **引擎生命周期汇合（Shutdown）**：
   - `Zhulong_engine_destroy` 时调用 `FacePluginLoader::instance().shutdown()`；
   - 设置 `stop_reaper_ = true`，调用 `cv_.notify_all()`，并 `reaper_thread_.join()`；
   - 卸载任何持有的库句柄，杜绝线程悬挂与内存泄漏。

### 2.3 多人脸选择策略
当上传的图像中检测到多张人脸时：
- 按照 `face_score`（置信度得分）降序排序；
- 选取置信度最高的人脸作为注册主人脸；
- 若最高置信度人脸低于 `score_threshold`（默认 0.50f），判定为未检出有效人脸，返回 `ZHULONG_PLUGIN_ERR_NOT_FOUND`。

### 2.4 宿主端插件寻址策略
宿主端依次尝试以下寻址候选路径：
1. 环境变量 `ZHULONG_FACE_PLUGIN_PATH` 显式指定的路径；
2. 环境变量 `ZHULONG_PLUGIN_DIR` 目录下的 `libFaceRecognitionPlugin.so`；
3. 构建产物目录 `build/native/host/libFaceRecognitionPlugin.so`；
4. 算法目标目录 `packages/face_recognition/targets/cpu/plugin.so`；
5. 当前工作目录下 `./libFaceRecognitionPlugin.so`。

若遍历所有候选路径均无法成功 `dlopen` 或 `dlsym` 找不到符号，打印警告日志并**立即返回 `Zhulong_ERR_UNSUPPORTED`**。

---

## 3. 错误映射与异常防爆门

| 状态类型 | 插件状态码 | Native Engine 状态码 | Go CGO 映射错误 | HTTP 错误响应 | 业务含义 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **成功** | `ZHULONG_PLUGIN_OK (0)` | `Zhulong_OK (0)` | `nil` | `200 OK` | 成功提取 512 维真实向量 |
| **未检出人脸** | `ZHULONG_PLUGIN_ERR_NOT_FOUND (-4)` | `Zhulong_ERR_NOT_FOUND (-4)` | `ErrNotFound` | `400 NO_FACE_DETECTED` | 上传照片无清晰人脸 |
| **算法包缺失** | *(dlopen 失败)* | `Zhulong_ERR_UNSUPPORTED (-3)` | `ErrUnsupported` | `500 FEATURE_EXTRACT_FAILED` | 插件缺失或模型不可用 |
| **参数非法** | `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT (-1)` | `Zhulong_ERR_INVALID_ARGUMENT (-1)` | `ErrInvalidArgument` | `400 INVALID_ARGUMENT` | 尺寸为 0 或空指针 |
| **格式不支持** | `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED (-3)` | `Zhulong_ERR_UNSUPPORTED (-3)` | `ErrUnsupported` | `400 UNSUPPORTED_FORMAT` | 非 RGB24/BGR24 格式 |
| **内部推理异常** | `ZHULONG_PLUGIN_ERR_INTERNAL (-99)` | `Zhulong_ERR_INTERNAL (-99)` | `ErrInternal` | `500 FEATURE_EXTRACT_FAILED` | 推理崩溃或内存不足 |

全量接口调用均置于 `boundary([&] { ... })` 与 `try-catch` 内部，绝不发生 C++ 异常穿越 ABI 边界。
