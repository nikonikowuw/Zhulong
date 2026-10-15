# 执行计划：实现人脸特征提取闭环（方案 2：Engine 独立提取与底库持久化）

## 门禁与前置检查

- [x] 确认方案 2 架构定案：`internal/engine` 提供单次提取 C ABI，`internal/person` 通过接口解耦依赖。
- [x] 确认工作区构建基线通过：`make go-check`。

---

## 阶段一：Native 引擎 C ABI 扩展与实现（Step 1）

- [x] **1.1 扩展头文件 `native/include/Zhulong/engine.h`**：
  - 声明 `Zhulong_engine_extract_face_feature` 接口与参数；
  - 导出符号与注释文档完善。

- [x] **1.2 Native 端人脸特征提取实现**：
  - 在 `native/src/pipeline/` 或对应算法辅助模块中实现特征提取函数；
  - 封装图像解码、检测、ArcFace 5 点仿射对齐与 512 维归一化输出；
  - 添加异常防护门，确保所有错误码安全返回，杜绝异常穿越 ABI；
  - 编写 Native 单元测试 `native/tests/face_feature_extract_test.cpp` 并挂载至 CTest；
  - 验证命令：`make native-test`。

---

## 阶段二：Go 边界层封装与错误码（Step 2）

- [x] **2.1 `internal/engine` 封装**：
  - 在 `internal/engine/engine.go` 中实现 `ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)`；
  - 严格遵循 CGO 指针与生命周期规则（`runtime.KeepAlive`，谁分配谁释放）；
  - 编写 `internal/engine/engine_face_test.go` 单测覆盖。

- [x] **2.2 本地化三语错误码**：
  - 在 `internal/httputil/locale.go` 中注册 `NO_FACE_DETECTED` 与 `FEATURE_EXTRACT_FAILED`；
  - 编写 HTTP 错误映射测试。

---

## 阶段三：Person 领域改造与底库持久化（Step 3）

- [x] **3.1 抽象 `FaceFeatureExtractor` 接口**：
  - 在 `internal/person/service.go` 中定义 `FaceFeatureExtractor` 接口并注入 `Service`；
  - 在 `internal/app` 中完成 Uber Fx 组装绑定。

- [x] **3.2 替换 `AddFaceWithReader` 的全零伪造逻辑**：
  - 移除 `feat := make([]byte, 2048)`；
  - 调用 `s.extractor.ExtractFaceFeature(ctx, imageBytes)`；
  - 失败时返回 `apperr.Invalid("NO_FACE_DETECTED", ...)` 且不写磁盘与 DB；
  - 成功时将 `[]float32` 编码为 2048 字节 Little-Endian 存入 `face.FeatureVector`。

- [x] **3.3 完善 `GetGallerySyncData`**：
  - 确保下发真实的 `Features` 与安全的 `ImagePath`；
  - 移除失效的死代码标记与占位注释。

- [x] **3.4 单元测试与质量验证**：
  - 编写 `internal/person/service_test.go` 针对 Mock Extractor 成功/失败分支的完整测试；
  - 验证命令：
    ```bash
    go test -race ./internal/person/...
    go test -race ./internal/engine/...
    ```

---

## 阶段四：全量工程门禁（Step 4）

- [x] 执行全量检查验证：
  ```bash
  make native-test
  make go-check
  ```

---

## 回滚计划

1. Native 侧：若单次提取 ABI 出现异常，返回 `Zhulong_ERR_INTERNAL`，不影响任何既有视频流拉流与处理；
2. Go 侧：`internal/person` 的 `FaceFeatureExtractor` 接口可注入一个 fallback 占位实现；
3. 数据库 `person_faces.feature_vector` 列保持既有 2048 字节二进制结构，无任何 schema 结构回滚负担。
