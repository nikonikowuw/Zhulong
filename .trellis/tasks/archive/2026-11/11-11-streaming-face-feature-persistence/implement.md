# 实施计划：流式人脸推理回传特征向量与识别记录持久化

## 1. 有序实施清单

1. **阶段一：Native SDK C ABI、Arena 打包与 IPC 序列化**
   - 修改 `native/sdk/include/zhulong/plugin.h`：新增 `ZHULONG_FACE_FEATURE_MAX_DIM`（`512`）；将 `zhulong_face_structure_item` 的 `reserved_[2]` 替换为 `feature_offset` + `feature_dim`（保持 `288` 字节），将 `zhulong_scene_structure` 的 `reserved_` 替换为 `const float* face_features`（保持 `32` 字节）。
   - 修改 `native/sdk/include/zhulong/algo.hpp`：在 `zhulong::FaceItem` 与 `zhulong::FaceStructureItem` 新增 `std::vector<float> features`；在 `SceneStructureResultAdapter::pack` 中校验并追加打包连续特征浮点池至同一 Arena 尾部。
   - 修改 `native/sdk/src/algo_main.cpp`：实现 IEEE-754 小端 `float32` Base64 编码器，在 `serialize_inference_result`（`ZHULONG_RESULT_TYPE_SCENE_STRUCTURE`）中输出 `"feature_b64"`，并实现基于 `ipc::MAX_IPC_PAYLOAD_SIZE`（`65536` B）的按 `quality` 降序预算裁剪保护。
   - 修改 `native/sdk/abi.json`：`sdk_version` 由 `1.2.0` 升至 `1.3.0`，记录流式特征池与 `"feature_b64"` 扩展。
   - 更新 `native/tests/plugin_c_abi_test.c`、`native/tests/face_recognition_onnx_test.cpp` 与 Runner 序列化测试（验证 288 B / 32 B 静态断言、真实模型流式特征 L2 归一化输出、Base64 逐位无损往返及 25 张脸 `<= 64 KB` 预算裁剪）。

2. **阶段二：`face_recognition` 双档位（`onnx` + `rk3568`）同步接线**
   - 修改 `packages/face_recognition/onnx/src/pipeline/face_recognition.cpp` 与 `packages/face_recognition/rk3568/src/pipeline/face_recognition.cpp`：在 `populate_matched_face_item` 中写入 `out_face.features`（`match_features`），并在 `detect_scene_structure` / `detect_persons` 中移动至输出结构体。
   - 修改 `packages/face_recognition/{onnx,rk3568}/CMakeLists.txt`：`find_package(ZhulongAlgoSDK 1.3 REQUIRED)`。
   - 运行 `make pkg-drift-check` 确保双 target 零漂移。

3. **阶段三：Go `aitypes` 反序列化与前端实时广播隔离**
   - 修改 `internal/aitypes/model.go`：新增 `FaceFeatureDimension = 512`、`FaceFeatureByteLen = 2048`；在 `FaceStructureItem` 与 `FaceSubItem` 新增 `FeatureVector []byte`（`json:"-"`）；通过私有 wire 结构解码校验 `"feature_b64"`（Base64 合法、2048 字节、全有限数、非零范数，不合法则置 `FeatureVector = nil`）。
   - 扩充 `internal/aitypes/model_test.go` 与 `internal/region` 测试：验证合法解码、畸形/NaN/零向量容错、`json.Marshal` 零泄漏及 `Reproject`/`Filter` 保真传递。

4. **阶段四：SQLite 迁移与 `internal/record` 持久化**
   - 新增 `internal/database/migrations/000034_add_feature_vector_to_recognitions.{up,down}.sql`。
   - 修改 `internal/record/model.go`、`internal/record/service.go`（`planPersonStructure` 兼容分支）、`internal/record/service_build.go`（`buildRecognitionRecordAndJob`）与 `internal/record/store.go`（`ListRecognitions` 在 `Find` 阶段 `Omit("feature_vector")`，`GetRecognitionByID` 完整读取）。
   - 扩充 `internal/record/store_test.go`、`internal/record/service_test.go`（含陌生人 `matched = false`、已命中人员 `matched = true` 及同轨迹升级落库的特征字节逐位验证，以及分页执行计划回归）。

5. **阶段五：规范文档更新与全量门禁验证**
   - 同步更新 `.trellis/spec/native/algo-plugin-contract.md` 与 `.trellis/spec/backend/record-guidelines.md` 中关于流式人脸特征载荷与 `recognitions.feature_vector` 的契约说明。
   - 运行 Native、算法包与 Go 全量检查门禁。

## 2. 验证命令

```bash
# 1. 双 target 源码漂移门禁
make pkg-drift-check

# 2. Native 构建与全量 CTest（含 C ABI 静态断言、IPC 序列化、ONNX 插件实跑）
make native-build
make native-test

# 3. 安装本地 ONNX SDK 前缀并验证 face_recognition/onnx 包构建与自测
make sdk-install BUNDLE_RUNTIME=1
make -C packages/face_recognition/onnx unit-test SDK_PREFIX=$(pwd)/build/sdk/onnx
make -C packages/face_recognition/onnx test SDK_PREFIX=$(pwd)/build/sdk/onnx

# 4. Go 跨层测试（遵循 AGENTS.md：走 native/scripts/build.py wrapper，开启 -race）
python3 native/scripts/build.py go test -race ./internal/aitypes/... ./internal/region/... ./internal/database/... ./internal/record/... ./internal/camera/...

# 5. 收工前全仓门禁
make check
```

## 3. 风险文件与回滚点

- `native/sdk/include/zhulong/plugin.h` 与 `native/tests/plugin_c_abi_test.c`：严禁改变 `sizeof(zhulong_face_structure_item) == 288`、`sizeof(zhulong_scene_structure) == 32` 或既有字段偏移，仅替换 `reserved_` 槽位。
- `packages/face_recognition/{onnx,rk3568}/src/pipeline/face_recognition.cpp`：两档位存在权重后缀（`.onnx` vs `.rknn`）与开发期候选路径差异，按函数块精准编辑后立即跑 `make pkg-drift-check`，禁止整文件覆盖。
- `internal/record/store.go`：`Omit("feature_vector")` 仅作用在 `listInReadSnapshot` 的 `Find` 查询链上，不改变 `Count` 与 `ORDER BY captured_at DESC, id DESC` 索引计划，需跑 `pagination_test.go` 验证无 `TEMP B-TREE`。
- `internal/database/migrations/000034_add_feature_vector_to_recognitions.{up,down}.sql`：未合并发布前回滚可直接执行 `down.sql`（`ALTER TABLE recognitions DROP COLUMN feature_vector;`）并回退迁移文件。

## 4. `task.py start` 前置检查

- [x] `prd.md` 已通过收敛检查，无重复段落、无未决阻塞问题。
- [x] `design.md` 与 `implement.md` 已就绪。
- [x] `implement.jsonl` 与 `check.jsonl` 已配置真实规范上下文并通过 `task.py validate`。
- [ ] 向用户展示最终规划摘要并获得明确实施批准后再执行 `task.py start`。
