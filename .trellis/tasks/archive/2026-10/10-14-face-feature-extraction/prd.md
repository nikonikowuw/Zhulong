# 实现人脸特征提取闭环（方案 2：Engine 独立提取与底库持久化）

## Goal

在 `internal/engine` 的 C ABI 中接入单次人脸特征提取能力，彻底替换 `internal/person` 中的 2048 字节全零假占位（`make([]byte, 2048)`）；实现上传登记照时即时提取真实 512 维特征并持久化到 SQLite `person_faces.feature_vector`；使 `GetGallerySyncData` 下发真实向量，保证算法 Runner 零秒冷启动，且登记过程完全不干扰运行中的视频流推理。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

- **前置**：结构化 ABI 与常量契约已就绪（`10-14-algo-structure-abi` 已归档）。
- **后继**：`10-14-gallery-hot-sync`（底库下行通道）将直接搬运本任务持久化的真实 512 维向量。
- **关联**：`internal/engine` 是唯一允许 `import "C"` 的包。`internal/person` 通过依赖注入的 Go 接口解耦。

## 核心设计决策（方案 2：Engine 独立单次提取）

1. **业务与推理隔离**：登记照单次特征提取由 `internal/engine` 提供独立能力，不把大图灌给正在处理实时视频流的 Runner，杜绝推理卡顿与丢帧风险；
2. **即时校验反馈**：用户在 Web 端上传照片时即时提取特征。未检出人脸或多脸时，立即返回 `400 Bad Request` 报错拦截，杜绝脏数据入库；
3. **底库强持久化**：提取出的 512 维 `float32` 向量（2048 字节）直接持久化进 SQLite `person_faces.feature_vector`；
4. **开机零开销秒启动**：冷启动时 Go 直接从 SQLite 读取已持久化的向量下发，Native 算法进程直接 `memcpy` 载入内存矩阵，无需开机重跑大批量 NPU 提取。

## 问题证据

```go
// internal/person/service.go:297
feat := make([]byte, 2048)   // ← 全零伪造切片，从未经过真实特征提取
face := &Face{
    ID:            faceID,
    OwnerRowID:    personID,
    ImagePath:     fileName,
    FeatureVector: feat,
}
```

## Requirements

- **R1 Engine C ABI 扩展**：
  在 `native/include/Zhulong/engine.h` 增加 `Zhulong_engine_extract_face_feature` 接口，支持输入图片二进制字节，输出 512 维 `float32` 数组与特征维度，定义 `Zhulong_ERR_NOT_FOUND`（未检出人脸）等规范状态码。
- **R2 Go CGO 封装与抽象接口**：
  在 `internal/engine` 封装 `ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)`；
  在 `internal/person` 定义消费者接口 `FaceFeatureExtractor`，通过 Uber Fx 进行依赖注入，严守架构边界。
- **R3 真实特征写入与即时校验**：
  改造 `internal/person/service.go` 的 `AddFaceWithReader`：
  - 调用 `FaceFeatureExtractor` 提取特征；
  - 提取失败时返回清晰的业务错误码（如 `NO_FACE_DETECTED`，三语本地化）；
  - 提取成功后将 512 维 float32 编码为 2048 字节 Little-Endian BLOB，存入 `person_faces.feature_vector`。
- **R4 底库数据准备下发（GetGallerySyncData）**：
  `GetGallerySyncData` 读取持久化的 `FeatureVector`，通过 `bytesToFloat32` 转换为真实浮点数组，输出给下游 IPC 下发；同时携带相对安全的 `image_path`。
- **R5 删除同步与物理清理**：
  `DeleteFace` 与 `DeletePerson` 保持安全的物理图片文件删除，并清除关联的数据库特征记录。

## Acceptance Criteria

- [ ] `Zhulong_engine_extract_face_feature` C ABI 编译通过且在 Native 单元测试中验证正确性。
- [ ] `internal/engine` 正确封装并导出 `ExtractFaceFeature` 方法。
- [ ] `AddFaceWithReader` 写入数据库的 `FeatureVector` 长度恒为 2048 字节且**非常量全零**。
- [ ] 上传无脸图片时，`AddFaceWithReader` 显式失败并返回预期错误码，不写入数据库与磁盘。
- [ ] `GetGallerySyncData` 吐出的 `GallerySyncItem` 携带非空、非零的 `Features` 切片（长度为 512）。
- [ ] `DeleteFace` 正常清理磁盘物理图片。
- [ ] `make native-test` 与 `make go-check`（含 `-race`）全绿。

## 范围外

- UDS 消息协议 `MSG_GALLERY_*` 的传输与分片（属 `10-14-gallery-hot-sync`）。
- 运行时多路视频流的 1:N 检索与检测（属 `10-14-face-algo-package`）。
