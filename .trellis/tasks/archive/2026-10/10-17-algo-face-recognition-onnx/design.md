# 技术设计：接入 ONNX 版本人脸识别算法包

## 1. 总体架构与数据流

Zhulong 采用「两层抽象 + 纯 C ABI 隔离」的算法插件架构：

```txt
[ zhulong_frame_view 视频帧 (RGB24/NV12/BGR24) ]
                   │
                   ▼ (FaceDetector::detect)
┌─────────────────────────────────────────────────────────┐
│ 1. 预处理 (CPU / Letterbox)                              │
│    - Letterbox 缩放居中至 640×640                         │
│    - 通道重排 & 类型转换：HWC UINT8 ➔ NCHW FLOAT32       │
│    - 归一化：(pixel - 127.5f) / 128.0f                   │
└─────────────────────────────────────────────────────────┘
                   │
                   ▼ Tensor {1, 3, 640, 640} FLOAT32
┌─────────────────────────────────────────────────────────┐
│ 2. SCRFD 前向推理 (IInferenceBackend::forward)          │
│    - OnnxSessionContext 独占 Worker 会话                │
│    - 输入挂载为 Ort::Value (零拷贝借用)                  │
│    - 输出 9 头张量 (3 stride × {score, bbox, kps})      │
└─────────────────────────────────────────────────────────┘
                   │
                   ▼ (FaceDetector::decode_scrfd)
┌─────────────────────────────────────────────────────────┐
│ 3. SCRFD 解码与 NMS 筛选                                 │
│    - 读取 9 头输出 (跳过二次 Sigmoid)                     │
│    - 仿射逆变换映射回归原图坐标                           │
│    - NMS 去重并归一化框与 5 点关键点至 [0.0, 1.0]         │
└─────────────────────────────────────────────────────────┘
                   │
                   ▼ std::vector<FaceDetection>
┌─────────────────────────────────────────────────────────┐
│ 4. Umeyama 仿射对齐 (Aligner::crop_and_align)            │
│    - 根据 5 点关键点拟合 ArcFace 5 点参考模板            │
│    - 2×3 相似变换重采样裁剪 112×112 人脸 Patch           │
│    - 输出 NCHW FLOAT32：(pixel - 127.5f) / 127.5f        │
└─────────────────────────────────────────────────────────┘
                   │
                   ▼ Tensor {1, 3, 112, 112} FLOAT32
┌─────────────────────────────────────────────────────────┐
│ 5. 特征提取 (FaceFeatureExtractor::extract)              │
│    - 前向推理 FaceLiVTv2 / ArcFace ONNX 模型             │
│    - 输出 [1, 512] FLOAT32 特征向量                      │
│    - 原地单位 L2 范数归一化                              │
└─────────────────────────────────────────────────────────┘
                   │
                   ▼ 512 维归一化特征向量
┌─────────────────────────────────────────────────────────┐
│ 6. 底库比对 (Matcher::match)                             │
│    - GalleryRegistry 快照获取当前已注册人脸向量           │
│    - 1:N 点积（余弦相似度）计算与 Top-K 排序             │
│    - 产出 zhulong_person_item 嵌套结构化结果             │
└─────────────────────────────────────────────────────────┘
```

## 2. 核心模块改造设计

### 2.1 检测预处理重构 (`detector.cpp`)

当前 `FaceDetector::detect` 在 `input_tensor` 构建时假定输入为 `{1, model_h, model_w, 3}` UINT8（专为 RKNN 设计）。
在接入 ONNX 时，需要支持生成 `{1, 3, model_h, model_w}` FLOAT32 张量：

```cpp
// 策略：检测当前编译配置与会话需求
#if defined(ZHULONG_INFERENCE_ONNX)
    constexpr bool kUseNchwFloat = true;
#else
    constexpr bool kUseNchwFloat = false;
#endif

// 若需要 NCHW FLOAT32：
// 1. 先经 letterbox 产出 640x640 RGB24 uint8 中间缓冲
// 2. 将 HWC uint8 转为 NCHW float32，执行 (x - 127.5f) / 128.0f 归一化
```

张量转换实现：
```cpp
void hwc_uint8_to_nchw_float(const uint8_t* src, float* dst, int w, int h, float mean, float std_val) {
    const float inv_std = 1.0f / std_val;
    const int plane_size = w * h;
    float* dst_r = dst;
    float* dst_g = dst + plane_size;
    float* dst_b = dst + plane_size * 2;

    for (int i = 0; i < plane_size; ++i) {
        dst_r[i] = (static_cast<float>(src[i * 3 + 0]) - mean) * inv_std;
        dst_g[i] = (static_cast<float>(src[i * 3 + 1]) - mean) * inv_std;
        dst_b[i] = (static_cast<float>(src[i * 3 + 2]) - mean) * inv_std;
    }
}
```

### 2.2 仿射对齐与特征预处理 (`aligner.cpp` & `extractor.cpp`)

`Aligner::crop_and_align_direct` 目前直接输出 `[112, 112, 3]` uint8。
为支持 ONNX 特征提取模型，重载或扩展该方法直接产出 `{1, 3, 112, 112}` FLOAT32 张量：
- 在双线性插值采样时，或采样完成后直接原地写入 NCHW float32 缓冲区；
- 归一化参数：`mean = 127.5f, std = 127.5f`。

### 2.3 SCRFD 9 头输出解码保真 (`decode_scrfd`)

SCRFD 模型在 ONNX 格式下的 9 个输出节点分别对应：
- Stride 8: `score [12800, 1]`, `bbox [12800, 4]`, `kps [12800, 10]`
- Stride 16: `score [3200, 1]`, `bbox [3200, 4]`, `kps [3200, 10]`
- Stride 32: `score [800, 1]`, `bbox [800, 4]`, `kps [800, 10]`

⚠️ **关键约束**：
- ONNX 图中 score 节点后已接 Sigmoid 算子，输出值域已在 `(0.0, 1.0)`。
- 解码函数 `decode_scrfd` 必须直接读取 `score[anchor_idx]`，绝对不可再次调用 `1.0f / (1.0f + exp(-s))`。
- 坐标必须经过 Letterbox 缩放补偿，最后归一化至 `[0.0, 1.0]`。

### 2.4 特征提取与 L2 归一化 (`FaceFeatureExtractor::extract`)

- 验证输出张量 element_count == 512；
- 将输出数据拷贝至 `std::vector<float>`；
- 执行快速原地 L2 范数归一化：
  $$\hat{v} = \frac{v}{\max(\|v\|_2, 10^{-6})}$$
- 送入 `matcher.match` 进行底库 1:N 检索。

## 3. 依赖隔离与构建设计

1. **头文件卫生**：
   `packages/face_recognition/` 仅引入 `zhulong/algo.hpp`，**严禁直接包含 `<onnxruntime_cxx_api.h>`**。所有 ONNX 细节完全封装在 `libZhulongAlgoRuntime.so` 内。
2. **CMake 构建配置**：
   - 算法包动态库 `libFaceRecognitionPlugin.so` 仅链接 `ZhulongAlgoRuntime` 与系统基础库，不直接引入 `libonnxruntime.so`；
   - 支持通过 `-DZHULONG_INFERENCE_BACKEND=onnx` 统一编译运行时与算法包。

## 4. 异常防御与边界约束

- 所有对外导出的 C ABI 函数（如 `zhulong_plugin_get_api` 中的 `process`、`session_create`、`init` 等）一律处于顶层 `try ... catch` 块内；
- 发生异常时清空结果，返回 `ZHULONG_PLUGIN_ERR_INTERNAL`，杜绝 C++ 异常穿越动态库边界。
- 结果内存分配严格采用 Arena 单块连续打包，`result_release` 时一次性释放。
