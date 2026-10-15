# 设计：实现人脸特征提取闭环（方案 2：Engine 独立提取与底库持久化）

## 1. 架构定位与核心权衡

### 1.1 为什么采纳方案 2？
正如用户敏锐指出的，将人脸特征提取从视频流 Runner 进程解耦是极具工业价值的决定：
1. **防止抖动影响实时推理**：
   视频流 Runner 是时延极其敏感的流水线（25~30 FPS）。若通过 IPC 将管理端上传的数兆大图硬塞给正在执行视频流推理的 Runner，极易导致 Worker 线程锁竞争、NPU 算力抢占，引发视频流丢帧与卡顿；
2. **独立离线录入能力**：
   管理人员录入人脸底库时，系统可能未开启任何摄像头视频流（Runner 甚至没有启动）。由 Native Engine 提供独立的单次人脸特征提取 C ABI，保证了人员管理功能可以在脱离视频流的前提下独立、高可用地运行；
3. **消除冷启动风暴**：
   提取出的 512 维向量一次性持久化至 SQLite `person_faces.feature_vector`。当 Runner 启动时，Go 宿主直接下发已持久化的浮点向量，Runner 内存底库毫秒级载入，杜绝开机对千张照片重复推理的数十秒系统挂死；
4. **守住 CGO 架构边界**：
   项目铁律是「`internal/engine` 是唯一可以 `import "C"` 的包」。我们将单次提取能力封装在 `internal/engine` 中，领域包 `internal/person` 仅通过纯 Go 接口依赖，零 CGO 污染。

---

## 2. 接口与契约设计

### 2.1 Native C ABI 契约扩展（`engine.h`）

在 `native/include/Zhulong/engine.h` 中扩展导出函数：

```c
/**
 * @brief 从人脸图像二进制数据同步提取 512 维归一化特征向量
 *
 * 内存与调用规范：
 * 1. image_data 指向 JPEG/PNG/WebP 格式的图像二进制内存，生命周期仅在调用期间有效；
 * 2. out_features 为调用方分配的 float 数组缓冲区，容量需 >= 512；
 * 3. feature_dim 为输入输出参数：传入缓冲区容量（按 float 计），返回实际写入的特征维度（固定 512）；
 * 4. 纯函数设计：内部完成图片解码、人脸检测、5 点关键点仿射对齐、特征模型前向推理与 L2 归一化；
 * 5. 异常安全：严禁 C++ 异常穿越 ABI，所有错误转换为返回状态码。
 *
 * @param engine 引擎实例句柄（可为 NULL 或关联的 engine 实例）
 * @param image_data 图像二进制数据指针
 * @param image_size 图像数据字节数
 * @param[out] out_features 输出特征向量缓冲区
 * @param[in,out] feature_dim 缓冲区容量输入与实际维度输出
 * @return Zhulong_status_t 0 成功；Zhulong_ERR_NOT_FOUND 未检测到人脸；其他为错误码
 */
Zhulong_status_t Zhulong_engine_extract_face_feature(
    Zhulong_engine_h engine,
    const uint8_t* image_data,
    size_t image_size,
    float* out_features,
    uint32_t* feature_dim
);
```

### 2.2 Go 边界层封装（`internal/engine`）

在 `internal/engine` 中提供符合 Go 习惯的安全包装：

```go
// ExtractFaceFeature decodes image bytes, detects face, and returns a 512-dimensional normalized embedding vector.
func (e *Engine) ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error) {
    if len(imageBytes) == 0 {
        return nil, apperr.Invalid("IMAGE_DATA_EMPTY", "image data cannot be empty", nil)
    }
    // 调用 C.Zhulong_engine_extract_face_feature 并做 runtime.KeepAlive
    // 返回 []float32 或根据错误码映射为 apperr 错误
}
```

### 2.3 领域解耦与依赖注入（`internal/person`）

在 `internal/person` 中定义抽象消费者接口：

```go
// FaceFeatureExtractor abstracts one-shot facial embedding generation.
type FaceFeatureExtractor interface {
    ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)
}
```

在 `Service` 结构体中注入：
```go
type Service struct {
    store     Store
    extractor FaceFeatureExtractor
    facesDir  string
    logger    *zap.Logger
}
```
通过 Uber Fx 在 `internal/app` 中将 `*engine.Engine` 绑定给 `FaceFeatureExtractor`。在单元测试中，可注入轻量 Mock 实例。

### 2.4 上传登记与即时校验流程（`AddFaceWithReader`）

1. **读取并校验图片**：验证 magic bytes 及 10MB 大小限制；
2. **提取真实特征**：
   ```go
   features, err := s.extractor.ExtractFaceFeature(ctx, imageBytes)
   if err != nil {
       // 未检测到人脸或图片质量不达标，立即拒绝，返回 400
       return nil, apperr.Invalid("NO_FACE_DETECTED", "No detectable face found in uploaded photo", err)
   }
   if len(features) != 512 {
       return nil, apperr.New(apperr.KindInternal, "FEATURE_DIM_MISMATCH", "Unexpected feature dimension", nil)
   }
   ```
3. **转字节存库**：
   使用 Little-Endian 将 512 维 `float32` 编码为 2048 字节字节切片：
   ```go
   featBytes := float32ToBytes(features)
   face := &Face{
       ID:            faceID,
       OwnerRowID:    personID,
       ImagePath:     fileName,
       FeatureVector: featBytes,
   }
   ```
4. **落盘与入库**：写入物理文件与 SQLite `person_faces`。若任一步失败，自动级联清理。

### 2.5 底库数据准备下发（`GetGallerySyncData`）

现存的 `service.go:394` 逻辑天然闭环：
```go
floats := bytesToFloat32(f.FeatureVector)
syncItems = append(syncItems, GallerySyncItem{
    FaceID:     f.ID,
    PersonID:   p.PersonID,
    PersonName: p.Name,
    ImagePath:  f.ImagePath,
    Features:   floats, // 真实 512 维向量
})
```
下行 IPC 通道在下发时携带真实的 `Features`，算法进程冷启动时无需重算，直接载入内存矩阵。

---

## 3. 错误码与三语映射（`locale.go`）

在 `internal/httputil/locale.go` 中补充错误码支持：
- `NO_FACE_DETECTED`：
  - 中文："上传的照片中未检测到清晰人脸"
  - 英文："No detectable face found in the uploaded photo"
  - 繁体："上傳的照片中未檢測到清晰人臉"
- `FEATURE_EXTRACT_FAILED`：
  - 中文："人脸特征提取失败"
  - 英文："Failed to extract facial features"
  - 繁体："人臉特徵提取失敗"

---

## 4. 测试与验证策略

1. **Native 层测试**：
   编写 `native/tests/face_feature_extract_test.cpp`，输入合成或测试人脸样本，断言返回状态码与 512 维特征输出；
2. **Go Engine 桥接测试**：
   测试 `Engine.ExtractFaceFeature` 对空数据、越界数据的防御，以及正常数据下的 CGO 内存对齐；
3. **Person 业务层测试**：
   - 编写 Mock Extractor 测试 `AddFaceWithReader`：提取成功存入 2048 字节，提取失败时磁盘文件与 DB 记录均不残留；
   - 测试 `GetGallerySyncData` 返回的 `Features` 非全零；
4. **全量门禁**：
   - `make native-test`
   - `make go-check`
