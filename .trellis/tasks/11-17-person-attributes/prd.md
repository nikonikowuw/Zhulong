# PRD: 人体属性识别（PPLCNet x1_0）接入 face_recognition 算法包

## Goal

让 face_recognition 算法包在输出「人体 + 人脸」结构化结果时，为每个真人体附带一组
26 分类多标签属性（Hat / Glasses / UpperStride / Trousers / Female / Front / Back ...），
经 Runner IPC JSON 传到 Go 宿主，最终落入 `records.attributes_json` 并显示在记录详情页。
属性模型为 PULC `PPLCNet_x1_0_person_attribute`（输入 3×256×192、输出 26 个独立 sigmoid 概率），
是二级分类器：复用包内 `scrfd_person_2.5g` 的人体框，逐人裁剪 → 分类 → 挂到 `PersonItem`。

## Background

| 编号 | 现有事实 | 关联 |
| :--- | :--- | :--- |
| F1 | `zhulong_person_item` 固定 40B，无属性字段；SDK `PersonItem` 同理 | `native/sdk/include/zhulong/plugin.h`、`algo.hpp` |
| F2 | 结果 JSON 由包内静态链接的 `result_serializer.hpp` 生成，宿主只读 JSON | `native/sdk/src/internal/result_serializer.hpp` |
| F3 | Go `aitypes.PersonItem` 已有 `Attributes map[string]any`，`internal/record/service.go:637` 已把它写进 `records.attributes_json` | `internal/aitypes/model.go` |
| F4 | 包内两张模型（onnx/rknn）共享 `src/`，漂移门只放行白名单文件 | `native/scripts/check_package_drift.py` |
| F5 | 宿主不读 payload `track_id`，`IsFaceCloseup()` 要求真人体 `track_id == 0` | `internal/aitypes/model.go:148`、`internal/record/service.go:1067` |
| F6 | RK3568 上 FP16 单人 14.86ms，逐帧逐人不可持续，必须按目标采样 | model zoo 实测 |

## Requirements

### R1. C ABI：person 属性尾随扩展（56B）
- `zhulong_person_item` 尾部追加 `attribute_count`、`reserved_`、`attributes`，40B → 56B，
  保持 8 字节对齐；`zhulong_person_attribute{char name[32]; float score; uint32_t reserved_;}` = 40B。
- 新增 `ZHULONG_PERSON_ATTRIBUTE_NAME_MAX_LEN 32`；无属性时 `attribute_count=0`、`attributes=NULL`。
- 更新 `plugin_c_abi_test.c` 尺寸/偏移/手工 Arena；`algo.hpp` 静态断言 person=56、attribute=40。

### R2. SDK 与序列化
- `zhulong::PersonAttribute{std::string name; float score;}`，`PersonItem` 增加
  `std::vector<PersonAttribute> attributes`；两个适配器的 Arena 增加 attribute 池。
- 序列化器在 `person_structure` 与 `scene_structure` 的 persons[] 中输出
  `"attributes":{"Hat":0.91,...}`（按 score 降序，无属性时省略字段）。
- `native/sdk/abi.json` sdk_version 1.3.0 → 1.4.0；契约文档补充布局与语义。

### R3. 算法包：属性阶段
- 新增 `src/attribute/person_attribute.{hpp,cpp}`：26 标签 + 每标签阈值（基准 0.5，
  Glasses 0.3，HoldObjectsInFront 0.6）；模型缺失时静默降级，插件仍可用。
- 裁剪走 CPU `Aligner::crop_and_align_direct` 直接拉伸到 192×256（模型训练即拉伸，无 letterbox）。
- onnx tier：HWC uint8 → NCHW float32，per-channel mean [123.675,116.28,103.53] / std [58.395,57.12,57.375]；
  rknn tier：`Tensor({1,256,192,3}, UINT8)` 原始 RGB（模型内置 mean/std）。
- 采样缓存：按 worker 维护 (box, attrs, last_ms) 列表，IoU≥0.3 贪心关联；
  未过期目标沿用缓存，每帧最多新分类 `person_attribute_max_persons` 个，单目标刷新周期
  `person_attribute_refresh_ms`；`enable_person_attributes=false` 时整段跳过。
- 真人体 `track_id` 保持 0；属性只挂在真人体上，退化近景项不输出属性。

### R4. 配置与 UI 表单
- `config.schema.json`（两个 tier 字节一致）新增：`enable_person_attributes`（bool，默认 true）、
  `person_attribute_refresh_ms`（int 200..10000，默认 1000）、`person_attribute_max_persons`
  （int 1..8，默认 2）。任务表单由 schema 自动渲染，无需改前端代码。

### R5. 测试与验证
- `tests/aligner_matcher_test.cpp`（两 tier 一致）新增无模型单测：IoU/关联/过期选择/
  阈值过滤/排序/参数解析。
- `dev_infer` 打印人体属性；记录详情页沿用既有 attributes JSON 面板。
- 验证：`make pkg-drift-check`、包 `unit-test`/`test`、Go `aitypes` 测试、板端 `--verify` 实测。

## Out of Scope
- 属性名中文展示/本地化；属性历史趋势统计；INT8 量化模型（默认 FP16，精度优先）。
