# PRD: 人脸属性识别（InsightFace genderage）接入 face_recognition 算法包

## Goal

让 face_recognition 算法包的每张**人脸**附带性别与年龄段属性，与既有的人体属性并行输出，
经 Runner IPC JSON 传到 Go 宿主，最终落入 `records.attributes_json` 并显示在记录详情页。

两条通道严格分离：

- **人体属性**挂在 `persons[]` 上（PPLCNet，人体框 192×256，服饰 / 姿态 / 粗年龄）；
- **人脸属性**挂在 `faces[]` 上（genderage，人脸 96×96，性别 + 五档年龄）。

用户裁决（m00285）：两条都要，且各挂各的项 —— 不允许把人脸退化成「只有属性的伪人体」。

## Background

| 编号 | 现有事实 | 关联 |
| :--- | :--- | :--- |
| F1 | `zhulong_face_item` 280B、`zhulong_face_structure_item` 288B、`zhulong_scene_structure` 32B，均无属性字段 | `native/sdk/include/zhulong/plugin.h` |
| F2 | 人体属性的 ABI 先例：`zhulong_person_attribute{char name[32]; float score; uint32_t reserved_;}` = 40B，尾随扩展 | 任务 `11-17-person-attributes` |
| F3 | 结果 JSON 由包内静态链接的 `result_serializer.hpp` 生成，宿主只读 JSON，不读 C 结构体 | `native/sdk/src/internal/result_serializer.hpp` |
| F4 | 场景里人脸是**连续数组**（`faces` + `face_count`），元素尺寸一变，池基址就不能再依赖元素内容 | `zhulong_scene_structure` 的 `face_features`/`feature_offset` 既有模式 |
| F5 | 近景人脸会退化成 `PersonItem`（`IsFaceCloseup()`），退化项也要带人脸属性 | `internal/aitypes/model.go`、`internal/record/service.go` |
| F6 | 两条通道的标签空间**重叠**：人体模型与人脸模型都有 `Female` | PPLCNet 标签表 / genderage 输出 |
| F7 | 人脸属性模型无法给出可信的绝对年龄：SCface 实测 MAE 3.6y、相关系数 −0.036 | 板端基准（200 张监控脸） |

## Requirements

### R1. C ABI：人脸属性的两条扩展路径（SDK 1.5.0）

person_structure（`type 3`）：人脸在数组里、没有场景级池，所以用**内嵌指针**。

- `zhulong_face_item` 280B → 296B，尾随 `attribute_count`(280)、`attribute_reserved_`(284)、
  `const zhulong_person_attribute* attributes`(288)。
- `zhulong_face_structure_item` 288B → 304B，尾随 `attribute_count`(288)、`attribute_reserved_`(292)、
  `attribute_offset`(296)、`attribute_reserved2_`(300)。

scene_structure：人脸是连续数组，元素尺寸一变，内嵌指针会让**旧宿主**的池基址计算落到未知字节。
因此改用**场景级共享池 + 起始下标**（与 `face_features`/`feature_offset` 同一模式），池基址恒定：

- `zhulong_scene_structure` 32B → 48B，尾随 `const zhulong_person_attribute* face_attributes`(32)、
  `uint32_t face_attribute_total`(40)、`uint32_t reserved_`(44)；原有字段偏移全部不变。
- 人脸属性复用 `ZHULONG_PERSON_ATTRIBUTE_NAME_MAX_LEN 32`，不新增类型。
- 类型名 `zhulong_person_attribute` 沿用（历史包袱），doc 注明人脸 / 人体共用同一布局。

### R2. SDK 与序列化

- `zhulong::PersonAttribute` 定义前移到 `FaceItem` 之前；`FaceItem` 与 `FaceStructureItem`
  各加 `std::vector<PersonAttribute> attributes`。
- 两个适配器 Arena 增加人脸属性段；SCENE 的人脸写 `attribute_offset`（无属性时 0）。
- 序列化器把 `append_person_attributes` 泛化为 `append_attributes(ss, count, attrs)`，
  在 4 处调用（PERSON/SCENE × 人/脸）；SCENE 人脸先校验
  `attribute_offset + attribute_count <= face_attribute_total` 再切片，越界整体放弃该脸属性。
- `native/sdk/abi.json` sdk_version 1.4.0 → **1.5.0**；契约文档补布局、两条扩展路径的取舍理由。

### R3. 算法包：人脸属性阶段

- 新增 `src/attribute/face_attribute.{hpp,cpp}`：`FaceAttributeClassifier::classify(session, image, landmarks_pixels, out)`。
- 复用包内已对齐好的 112×112 相似变换：`rec_affine × (96/112)`，`Aligner::crop_and_align_direct` 出 96×96。
- rknn tier：`Tensor({1,96,96,3}, UINT8)` 原始 RGB，**不做归一化**（mean/std 已烘进图内）。
- 输出两个 FLOAT32：`[1,2]` 性别 logits → `softmax(min-max)` 置信度，**≥0.60 才输出** Female/Male；
  `[1,1]` age_raw → `×100` 得年，分五档 `Age0-18` / `Age18-30` / `Age30-45` / `Age45-60` / `Age60+`，
  **固定 score 1.0**（回归头没有概率语义，伪造一个置信度会误导下游）。
- 模型缺失时静默降级（`lookup_session` 返回空则整段跳过），插件仍可用。
- 阶段位置：3 阶段组装完合格人脸 → **3.5 逐张分类** → 5 阶段构造场景。`enable_face_attributes=false` 时跳过。
- 近景退化项同样带属性（`detect_persons` 路径）。
- 两个 tier 共享同一份 `src/`（只差扩展名与模型文件名）以满足漂移门。

### R4. 配置与 UI 表单

- `config.schema.json`（两个 tier 字节一致）新增 `enable_face_attributes`（boolean，默认 true）。
  任务表单由 schema 自动渲染，无需改前端代码。

### R5. Go 宿主：两条通道各自落库

- `aitypes.FaceSubItem` 与 `FaceStructureItem` 各加 `Attributes map[string]any`；
  `toFaceStructureItem`（PERSON_STRUCTURE 归一化出口）必须搬运它。
- 抓拍：人脸抓拍写 `face.Attributes`（原为写死的 `"{}"`），人体抓拍仍写 `person.Attributes`。
- 识别记录：两条通道合并，**同名标签不覆盖、不取 max**，人脸那条改名 `<标签>@face`。

### R6. 测试与验证

- `native/tests/plugin_c_abi_test.c`：三处 sizeof、全部新偏移、手算 Arena（含池 + 下标切片）。
- `native/tests/result_serializer_test.cpp`：人脸属性序列化、两条通道不互相串、越界放弃。
- `internal/aitypes`：真实 JSON 载荷解析后读取人脸属性（防止归一化出口漏字段）。
- `internal/record`：人脸 / 人体属性各归其项、合并规则（表驱动，含不改调用方 map、单通道直接返回）。
- 验证：`make native-test`、包 `unit-test`/`test`（两个 tier）、Go `-race`、板端 `--verify` 实测。
