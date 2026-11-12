# 流式人脸推理回传特征向量与识别记录持久化

## Goal

打通 `face_recognition` 算法包（`rk3568` 与 `onnx` 双档位）流式视频推理的特征向量回传与数据库持久化链路：在流式推理输出中无损携带每张合格人脸的 **512 维 L2 归一化特征向量**（`float32[512]`，小端序 2048 字节），经 Native SDK Arena 打包、Runner IPC 协议传输、Go 宿主解析校验后，统一持久化到 SQLite `recognitions.feature_vector`（`BLOB`）列（覆盖 `matched = 0` 未匹配陌生人与 `matched = 1` 已命中人员），同时严格隔离前端实时 SSE/WebSocket 与常规列表查询载荷，为后续「陌生人识别 / 历史聚类 / 以向量检索 / 一键转入底库」提供完整的数据底座。

## Background & Confirmed Facts

- **两条特征提取路径的现状差异**：
  - 静态登记/搜图路径（`FaceExtractorContext::extract` / `zhulong_face_extract_feature`，`packages/face_recognition/rk3568/src/pipeline/face_recognition.cpp:545-660`）已向宿主返回 512 维 L2 归一化特征，并由 `internal/person/service.go:865-888` 按小端序 `float32ToBytes`（固定 `2048` 字节）存入 `person_faces.feature_vector BLOB NOT NULL`（`internal/database/migrations/000006_create_persons_tables.up.sql:13-19`）。
  - 流式推理路径（`FaceRecognitionPlugin::detect_scene_structure`，`rk3568/src/pipeline/face_recognition.cpp:283-457` 与 `onnx/src/pipeline/face_recognition.cpp:286-460`）已提取 `probe_features` 并经 `fusion_manager_.fuse_frame` 得到用于底库比对的 512 维 L2 单位向量 `fused_outputs[i].features`（`match_features`），但 `populate_matched_face_item` 比对后即丢弃该向量，`zhulong::FaceStructureItem`（`native/sdk/include/zhulong/algo.hpp:252-265`）与 `recognitions` 表（`000011_create_perception_and_alarm_tables.up.sql:1-21`）均无特征向量字段。
- **陌生人记录的表归属事实**：
  - `internal/record/service.go:469-550,581-656` 中，所有通过质量门禁并上报的人脸（无论是否关联全身框、`matched = false` 陌生人或 `matched = true` 已命中人员）在通过识别冷却抑制后均写入 **`recognitions` 表**（前端「识别记录 → 未匹配陌生人」）。
  - `captures` 表在同时检出人体框与人脸时仅写入 `target_type = "person"`，仅无全身框的孤立脸（`person_index == -1`）才写 `target_type = "face"`。
- **C ABI 尺寸门禁与预留位事实**：
  - `native/sdk/include/zhulong/plugin.h:230-264` 与 `native/tests/plugin_c_abi_test.c:111-120` 强制断言 `sizeof(zhulong_face_structure_item) == 288`（含偏移 `272..280` 的 `uint32_t reserved_[2]`）与 `sizeof(zhulong_scene_structure) == 32`（含偏移 `24..32` 的 `uint64_t reserved_`）。复用这两处对齐预留位可在**保持结构体尺寸与既有字段偏移 100% 不变**的前提下承载 Arena 特征池指针与 `(feature_offset, feature_dim)`。
- **IPC 64 KB 硬上限与前端实时流边界**：
  - `native/src/algorithm/ipc/ipc_protocol.hpp:34` 与 `unix_socket.cpp:478,585` 强制限制单包 `MSG_RESULT` 负载 `<= MAX_IPC_PAYLOAD_SIZE`（`65536` 字节），超限直接拒发导致整帧丢失。小端 `float32[512]` 原始 2048 字节经 Base64 编码固定为 `2732` 字节（零浮点文本化精度损失，比十进制 JSON 数组省约 45% 体积），且序列化层须具备字节级 64 KB 预算裁剪保护。
  - `internal/camera/stream_hub.go:1185-1196` 在 `record.Service` 消费后立即将 `aitypes.InferenceResult` 交给 `EventHub.BroadcastAIResult`（`json.Marshal` 推向浏览器实时预览），且 `internal/algorithm/service.go:710-740` 在单图试跑中序列化结果；Go 领域结构体的特征字段必须标记 `json:"-"`，杜绝向浏览器 SSE/WebSocket 或 HTTP JSON 泄漏原始向量。
- **已确认的产品与范围决策**：
  1. **落库范围（选项 A）**：在 `recognitions` 表新增可空 `feature_vector BLOB`，全量识别记录（含 `matched = 0` 陌生人与 `matched = 1` 已命中人员）均保存当帧用于比对的 512 维特征向量；`captures` 表不存储特征向量。
  2. **本期交付边界**：聚焦交付「流式推理回传特征向量 + `recognitions` 落库存储」数据底座，不在此任务中变更上层陌生人聚类、向量检索 HTTP API 或前端页面。

## Requirements

- **R1 — Native SDK C ABI 与 C++17 Arena 特征打包契约**：
  - 在 `native/sdk/include/zhulong/plugin.h` 定义 `#define ZHULONG_FACE_FEATURE_MAX_DIM 512`；将 `zhulong_face_structure_item`（`288` 字节）偏移 `272..280` 的 `uint32_t reserved_[2]` 具象化为 `uint32_t feature_offset`（`272..276`）与 `uint32_t feature_dim`（`276..280`）；将 `zhulong_scene_structure`（`32` 字节）偏移 `24..32` 的 `uint64_t reserved_` 具象化为 `const float* face_features`（指向同一单块连续 Arena 内的浮点特征池，全帧无特征时为 `NULL`）。保持 `api_version = 1`、既有字段偏移与结构体总字节数完全不变，并更新 `native/tests/plugin_c_abi_test.c` 的 `_Static_assert` 与 Arena 解构测试。
  - 在 `native/sdk/include/zhulong/algo.hpp` 的 `zhulong::FaceItem` 与 `zhulong::FaceStructureItem` 新增 `std::vector<float> features`；扩展 `SceneStructureResultAdapter::pack` 将合法维度（`1..ZHULONG_FACE_FEATURE_MAX_DIM` 且全部元素 `std::isfinite`）的特征向量追加打包至单块连续 Arena 尾部（8 字节对齐起始边界），非法维度或含非有限值的特征按 fail-closed 置 `feature_dim = 0`（不截断、不抛错丢帧），由 `SceneStructureResultAdapter::release` 单次 `std::free` 统一释放。
  - 将 `native/sdk/abi.json` 的 `sdk_version` 由 `1.2.0` 升至 `1.3.0`（`api_version: 1`、`protocol: 1` 不变），记录新增的流式人脸特征 Arena 与 IPC 字段语义。
- **R2 — 算法包双档位（`rk3568` + `onnx`）流式特征透传**：
  - 在 `packages/face_recognition/{rk3568,onnx}/src/pipeline/face_recognition.cpp` 的 `populate_matched_face_item`、`detect_scene_structure` 与 `detect_persons` 中，将实际送入 `matcher_.match` 的 512 维 L2 归一化特征 `match_features`（启用跨帧融合且命中多帧时为融合特征，否则为单帧特征）填入输出人脸项 `features`（涵盖默认 `filter_unmatched = false` 下的未匹配陌生人与已命中人员）。
  - 将 `packages/face_recognition/{rk3568,onnx}/CMakeLists.txt` 的 `find_package(ZhulongAlgoSDK ...)` 最低版本升至 `1.3`，防止旧 SDK 前缀静默混编；确保 `make pkg-drift-check` 零漂移通过。
- **R3 — Runner IPC 无损 Base64 编码与 64 KB 载荷预算保护**：
  - 在 `native/sdk/src/algo_main.cpp` 的 `serialize_inference_result`（`ZHULONG_RESULT_TYPE_SCENE_STRUCTURE` 分支）中，当 `scene->face_features != nullptr` 且 `f.feature_dim > 0`（且范围不越界、全元素有限）时，按显式 IEEE-754 小端序（`float32` 4 字节 LE）编码为标准 Base64 字符串，输出可选 JSON 字段 `"feature_b64":"..."`（512 维对应 `2048` 原始字节、`2732` Base64 字符）；`feature_dim == 0` 时省略 `"feature_b64"` 字段。
  - **64 KB 硬上限防护**：序列化时先精确计算不含 `"feature_b64"` 的完整结构化基础 JSON 字节数；若总字节数超出 `ipc::MAX_IPC_PAYLOAD_SIZE`（`65536` B），在不超过 `65536` B 剩余预算的前提下按人脸 `quality` 降序（同分按下标升序）择优附带 `"feature_b64"`，超预算的尾部人脸仅省略 `"feature_b64"` 而完整保留框、关键点与比对身份，严禁因特征字段撑爆 64 KB 导致 `unix_socket` 丢弃整帧 `MSG_RESULT`。
- **R4 — Go `aitypes` 反序列化校验与实时广播隔离**：
  - 在 `internal/aitypes/model.go` 的 `FaceStructureItem` 与 `FaceSubItem` 新增 `FeatureVector []byte` 字段，强制标记 `json:"-"`，确保 `EventHub.BroadcastAIResult`（实时预览流）、`AlgorithmTrialResponse`（单图试跑）及任何对 `InferenceResult` 的 `json.Marshal` 均**零字节输出**特征向量。
  - 在 `ParseInferenceResult` 中通过内部 wire 结构解析 `"feature_b64"`：要求标准 Base64 解码成功、字节长度严格等于 `512 * 4 = 2048`、全部 512 个小端 `float32` 均为有限数（非 `NaN`/`Inf`）且 L2 范数平方 `> 1e-12`；任一条件不满足时仅将该脸的 `FeatureVector` 置为 `nil`（fail-closed 丢弃坏向量，保留识别框与身份元数据，不使整包 JSON 解析报错）。
  - 确保 `region.Reproject`、`region.Config.Filter` 与 `record.planPersonStructure` 在坐标重映射、ROI/MASK 过滤及旧结构展平时原样保留 `FeatureVector`。
- **R5 — SQLite 版本化迁移与 `internal/record` 持久化**：
  - 新增成对迁移 `internal/database/migrations/000034_add_feature_vector_to_recognitions.{up,down}.sql`：
    - `up.sql`：`ALTER TABLE recognitions ADD COLUMN feature_vector BLOB;`（可空列，历史记录与不带特征的算法写入 SQL `NULL`，零全表重写开销）。
    - `down.sql`：`ALTER TABLE recognitions DROP COLUMN feature_vector;`。
  - 在 `internal/record/model.go` 的 `Recognition` 模型新增 `FeatureVector []byte`（`gorm:"column:feature_vector"`，存储格式与 `person_faces.feature_vector` 完全同构：2048 字节小端 `float32[512]`，无特征时为 `nil` 写 SQL `NULL`）；保持对外 HTTP 投影 `RecognitionResponse` 与告警推送 `FaceEventData` 不暴露该二进制列。
  - 在 `internal/record/service_build.go` 的 `buildRecognitionRecordAndJob` 中将合法（`len == 2048`）的 `face.FeatureVector` 写入 `Recognition.FeatureVector`（否则写 `nil`）。
  - 在 `internal/record/store.go` 的分页列表查询 `ListRecognitions` 中对数据读取阶段应用 `Omit("feature_vector")`（避免列表翻页/导出读取 SQLite 溢出页，保持分页查询轻量且不改变索引执行计划），而单条详情查询 `GetRecognitionByID` 完整读取 `FeatureVector`。

## Acceptance Criteria

- [ ] **AC1（C ABI 零膨胀与 SDK Arena 往返）**：`native/tests/plugin_c_abi_test.c` 通过 `sizeof(zhulong_face_structure_item) == 288`、`sizeof(zhulong_scene_structure) == 32` 静态断言及特征池读写断言；`ZhulongFaceRecognitionOnnx` 与新增/扩充的 Native 测试验证流式 `api.process` 产出的每张检出人脸均携带 `feature_dim == 512`、L2 范数 `≈ 1.0` 的有限特征向量，且与底库比对所用特征一致。
- [ ] **AC2（IPC Base64 无损编码与 64 KB 预算裁剪）**：Native 单测验证（a）512 维 `float32` 经 `serialize_inference_result` 输出 `2732` 字符 `"feature_b64"`，解码回小端字节与源 `float32` 逐位一致（`memcmp == 0`）；（b）`feature_dim == 0` 时 JSON 不含 `"feature_b64"`；（c）模拟单帧超多人脸（如 25 张脸）时，序列化后的 JSON 总字节数严格 `<= 65536`（`MAX_IPC_PAYLOAD_SIZE`），全部 25 张脸的结构化元数据完整保留，高 `quality` 人脸优先携带 `"feature_b64"`。
- [ ] **AC3（Go 解析校验与实时流隔离）**：`internal/aitypes` 单测验证（a）合法 `"feature_b64"` 精确还原为 2048 字节 `FeatureVector`；（b）缺省、非法 Base64、长度非 2048、含 `NaN`/`Inf` 或全零向量时 `ParseInferenceResult` 不报错且 `FeatureVector == nil`；（c）对解析后的 `InferenceResult` 执行 `json.Marshal` 输出中不包含 `"feature_b64"` 或 `"feature_vector"`；`region.Reproject` 与 `region.Filter` 后保留人脸的 `FeatureVector` 不丢失。
- [ ] **AC4（SQLite 迁移与 `recognitions` 落库契约）**：`internal/database` 与 `internal/record` `-race` 测试验证（a）迁移 `000034` 的 up/down 可逆；（b）`ProcessInferenceResult` 处理 `matched = false`（陌生人）与 `matched = true`（已命中人员，含同轨迹 `unmatched → matched` 融合升级）时，`GetRecognitionByID` 读回的 `FeatureVector` 均为精确的 2048 字节小端特征，无特征输入时列值为 `NULL`（`len == 0`）；（c）`ListRecognitions` 保持 `EXPLAIN QUERY PLAN` 无 `TEMP B-TREE` 且不加载 `feature_vector` 大字段，`RecognitionResponse` JSON 结构保持向后兼容。
- [ ] **AC5（双档位零漂移与全量门禁）**：`make pkg-drift-check`、`make native-build`、`make native-test`、`make -C packages/face_recognition/onnx unit-test test`（在 SDK 前缀可用时）及 `make check` 全部通过。

## Out of Scope

- 不在 `captures` 表或 `alarms` 表新增或写入 `feature_vector` 列。
- 不修改现有 `GET /api/v1/records/recognitions`、`GET /api/v1/records/recognitions/:id`、SSE/WebSocket 实时流或告警推送的对外 JSON 字段。
- 不在本任务实现陌生人特征聚类归档、基于 `recognitions.feature_vector` 的历史以图搜人/以向量检索 API、陌生人一键入底库接口或前端 UI 变更（由后续独立任务基于本数据底座承接）。
