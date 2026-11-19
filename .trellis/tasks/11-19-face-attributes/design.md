# Design: 人脸属性阶段（InsightFace genderage）

## 1. ABI 变更（SDK 1.5.0，向后兼容的尾随扩展）

### 1.1 person_structure：人脸数组内联指针

`zhulong_face_item` 280B → 296B：

| 偏移 | 字段 |
| --- | --- |
| 272..280 | `const zhulong_face_candidate* candidates`（既有末字段） |
| 280..284 | `uint32_t attribute_count` |
| 284..288 | `uint32_t attribute_reserved_`（必须为 0，补偿指针前对齐） |
| 288..296 | `const zhulong_person_attribute* attributes` |

`zhulong_face_structure_item` 288B → 304B：

| 偏移 | 字段 |
| --- | --- |
| 280..288 | `const zhulong_face_candidate* candidates`（既有末字段） |
| 288..292 | `uint32_t attribute_count` |
| 292..296 | `uint32_t attribute_reserved_` |
| 296..300 | `uint32_t attribute_offset` |
| 300..304 | `uint32_t attribute_reserved2_` |

### 1.2 scene_structure：场景级共享池 + 下标

`zhulong_scene_structure` 32B → 48B：

| 偏移 | 字段 |
| --- | --- |
| 0..4 | `uint32_t person_count` |
| 4..8 | `uint32_t face_count` |
| 8..16 | `const zhulong_person_item* persons` |
| 16..24 | `const zhulong_face_structure_item* faces` |
| 24..32 | `const float* face_features` |
| 32..40 | `const zhulong_person_attribute* face_attributes` |
| 40..44 | `uint32_t face_attribute_total` |
| 44..48 | `uint32_t reserved_`（必须为 0） |

**为什么两处用不同的路径（本任务最关键的设计决策）**：

`zhulong_face_structure_item` 在场景里是**连续数组**（`faces` + `face_count`），而人脸这个
连续段的**总字节数**取决于元素尺寸。属性池要放在人脸数组后面，池基址就必须是
`faces + face_count * sizeof(element)`。只要元素尺寸增长，**旧宿主**按旧的 sizeof 算出来的
池基址就会落到新字段中间 —— 读到的是属性个数或保留字段的字节，而不是池的开头。

`zhulong_person_item` 增加属性时没这个问题是因为属性指针在元素**内部**，每个元素自描述；
但人脸属性如果也用元素内指针，旧宿主算池基址的公式就依赖"每张脸自带几个属性"，而那是运行期
数据。所以场景路径必须让池基址**恒定**：池独立于人脸数组，人脸只用 `attribute_offset`
（起始下标）索引 —— 与既有 `face_features` / `feature_offset` 完全同一模式，旧宿主的
`face_features` 基址计算本来就不依赖人脸元素尺寸的增长，这次也不会被破坏。

person_structure 里人脸数组后面没有共享池，且 `type 3` 的载荷本来就是"每个元素自带
candidates 指针"的自描述风格，所以那里用内联指针。

为什么整体安全：宿主从不读取 C 结构体，结果 JSON 由包内静态链接的 `result_serializer.hpp`
产出；两个适配器与插件一起编译，`static_assert` 在同一次构建内校验。
旧宿主只会看到多出的 `attributes` JSON 键（Go `map[string]any` 原样接收）。

Arena 布局（scene，与 `SceneStructureResultAdapter::pack` 的分配顺序一致）：
`scene(48) | persons(n*56) | faces(n*304) | candidates(n*200) | person_attributes(n*40) | face_attributes(n*40) | features(n*4)`。

Arena 布局（person_structure）：
`persons(n*56) | faces(n*296) | candidates(n*200) | face_attributes(n*40) | person_attributes(n*40)`。

## 2. 模型与预处理

- 模型：InsightFace `genderage`，来自 rknn_model_zoo `examples/age_gender`。
- 输入：**已对齐**的 96×96 RGB 人脸（复用包内人脸识别那套 112×112 相似变换，乘 `96/112`）。
  416 张 SCface（证件照 + 监控）中 362 张检出人脸，genderage 侧对齐失败 0 张。
- rknn tier：`Tensor({1,96,96,3}, UINT8)` 原始 RGB，**不归一化**
  （`convert.py` 用 `mean=[[0,0,0]] / std=[[1,1,1]]` 把归一化烘进了图内，喂归一化数据会二次归一）。
- 输出两个张量（`SPLIT_OUTPUT=1` 把最后一层 Concat 拆开，避免在 NPU 上做 concat）：
  - `[1,2]` FLOAT32 性别 logits → `softmax(min-max)`；
  - `[1,1]` FLOAT32 age_raw → `×100` 得年。

## 3. 输出语义与阈值

- 性别：置信度 **≥ 0.60** 才输出 `Female` / `Male`（低于阈值说明模型自己也不确定，
  输出一个 0.52 的标签只会污染下游报表）。
- 年龄：回归头没有概率语义，**固定 score 1.0**，只给区间不给确切值：
  `Age0-18` / `Age18-30` / `Age30-45` / `Age45-60` / `Age60+`。
  依据：SCface 2009 年真实年龄对照（n=20）MAE 3.6y、相关系数 **−0.036** ——
  绝对年龄不可信，区间才有意义。
- 结果按 score 降序稳定排序。
- 每张脸最多 2 条（1 个性别 + 1 个年龄档），属性体量固定，不随场景人脸数爆炸。

## 4. 管道接入点

`FaceRecognitionPlugin::detect_scene_structure`：

- 新成员：`zhulong::Model face_attribute_model_`、`SessionTable face_attribute_sessions_`、
  `FaceAttributeClassifier face_attribute_classifier_`、`bool default_face_attributes_enabled_`。
- `init`：作为第 6 个**可选**模型加载 `models/genderage_face`（缺失 → 静默关闭，插件照常可用）。
- `create_session`：为每个 worker 建人脸属性 session。
- **3.5 阶段**（3 阶段组装完 `processed_faces` 之后、5 阶段构造场景之前）：
  读任务级 `enable_face_attributes` → `lookup_session` → 逐张 `classify(...)` 写入
  `ProcessedFace::attributes`。
- 5 阶段构造 `scene.faces` 时 `face.attributes = std::move(processed.attributes)`。
- `detect_persons`（旧 v1 结果）的退化孤立脸同样带属性。
- 关键点用**原帧像素坐标**（`pixel_landmarks(detection, width, height)`），不在分类器里二次换算。

## 5. 配置

```json
"enable_face_attributes": {"type":"boolean","default":true, ...}
```

两个 tier 的 `config.schema.json` 必须字节一致；`additionalProperties:false`。
按任务开关，不改模型加载（关掉只是跳过推理）。

## 6. Go 宿主：两条通道的合流

标签空间重叠不是假设：人体模型与人脸模型**都有** `Female`，实际数据里会同时出现
`Female: 0.54`（人体看走眼）和 `Male: 0.98`（人脸看准了）。

合并规则（`mergeAttributeChannels`）：

1. 只有一条通道有值 → 直接返回它（不复制、不改名）。
2. 两边同名 → **两条都保留**，人脸那条改名 `<标签>@face`。

理由：覆盖会静默丢掉一整条通道的证据，而且丢哪边取决于 Go map 的遍历顺序；
取最大值把"人体说 Female=0.54、人脸说 Male=0.98"这种真实矛盾揉成一个数，掩盖问题。
后缀而不是前缀，是因为现有报表按"标签名是否等于某个已知属性名"匹配，后缀在视觉上
仍归到同名标签附近，同时键集合是纯函数、与遍历顺序无关。

人脸通道若已自带 `@face`，继续追加（`Female@face@face`）—— 难看但不丢信息。

落库位置：

- 人脸抓拍 → `captures.attributes_json` 只写 `face.Attributes`（原来写死 `"{}"`）；
- 人体抓拍 → 只写 `person.Attributes`；
- 识别记录 → 合并后的结果。

## 7. 文件清单

- `native/sdk/include/zhulong/plugin.h`：新字段、注释、`zhulong_person_attribute` 前移。
- `native/sdk/include/zhulong/algo.hpp`：`PersonAttribute` 前移、两个 `attributes` vector、两个适配器、断言。
- `native/sdk/src/internal/result_serializer.hpp`：`append_attributes` 泛化、4 处调用、SCENE 越界校验。
- `native/sdk/abi.json`：1.5.0。
- `native/tests/plugin_c_abi_test.c`、`native/tests/result_serializer_test.cpp`。
- `.trellis/spec/native/algo-plugin-contract.md`。
- `packages/face_recognition/{onnx,rk3568}/src/attribute/face_attribute.{hpp,cpp}`（两档位一致）。
- `packages/face_recognition/{onnx,rk3568}/src/pipeline/face_recognition.{hpp,cpp}`。
- `packages/face_recognition/{onnx,rk3568}/config.schema.json`、`CMakeLists.txt`、`tests/dev_infer.cpp`。
- `packages/face_recognition/rk3568/models/genderage_face.rknn`。
- `internal/aitypes/model.go`、`internal/aitypes/model_test.go`。
- `internal/record/service.go`、`internal/record/service_build.go`、`internal/record/export_test.go`、
  `internal/record/capture_face_attributes_test.go`。
- `internal/apidocs/{docs.go,swagger.json,swagger.yaml}`：`make api-docs` 自动产物
  （`aitypes.FaceSubItem` 新增 `attributes` 字段的投影），必须与源码同批提交，否则 `go-check` 红。
- `packages/face_recognition/README.md`：面向使用者的字段与限制说明。
- `native/CMakeLists.txt`：`ZhulongPluginCAbiTests` 是纯 `assert()` 的 C 测试，补 `-UNDEBUG`。

## 8. 验证步骤

1. `make native-build` + `make native-test`（C ABI 静态断言、序列化测试）。
2. `make -C packages/face_recognition/onnx unit-test test`（x86）。
3. `make pkg-drift-check`。
4. `python3 native/scripts/build.py go test -race ./internal/aitypes/ ./internal/record/`。
5. `make sdk-cross-install` + 板端构建运行，检查人脸行属性与耗时。
   落地方式：`BOARD_HOST=<ip> native/scripts/board_check.sh`（脚本自建 aarch64 SDK 前缀 →
   同步到板端 → 板端 `make test` / `make verify` / `algo_probe.py --all` / 符号门禁）。

## 9. 已知限制

- onnx tier 没有 `genderage_face.onnx`，人脸属性在该 tier 恒为空（静默降级，不报错）。
- 绝对年龄不可用，只输出区间。
- 性别阈值 0.60 是针对监控场景手调的；证件照场景几乎全部过阈值。
