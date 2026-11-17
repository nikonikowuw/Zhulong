# Design: 人体属性阶段（PPLCNet x1_0 person attribute）

## 1. ABI 变更（向后兼容的尾随扩展）

`zhulong_person_item` 40B → 56B：

| 偏移 | 字段 |
| --- | --- |
| 0..8 | `int64_t track_id` |
| 8..24 | `float body_box[4]` |
| 24..28 | `float body_score` |
| 28..32 | `uint32_t face_count` |
| 32..40 | `const zhulong_face_item* faces` |
| 40..44 | `uint32_t attribute_count` |
| 44..48 | `uint32_t reserved_`（必须为 0，补偿指针前对齐） |
| 48..56 | `const zhulong_person_attribute* attributes` |

```c
typedef struct zhulong_person_attribute {
    char     name[ZHULONG_PERSON_ATTRIBUTE_NAME_MAX_LEN]; /* UTF-8 标签，'\0' 结尾 (0..32) */
    float    score;                                       /* sigmoid 概率 (32..36) */
    uint32_t reserved_;                                   /* 必须为 0 (36..40) */
} zhulong_person_attribute;                               /* 40B */
```

为什么安全：宿主从不读取 C 结构体，结果 JSON 由包内静态链接的 `result_serializer.hpp` 产出；
`PersonStructureResultAdapter` / `SceneStructureResultAdapter` 与插件一起编译，尺寸断言在同一次构建内校验。
旧宿主只会看到多出的 `attributes` JSON 键（Go `map[string]any` 原样接收）。

Arena 布局（scene）：`scene(32) | persons(n*56) | faces(n*288) | candidates(n*200) | attributes(n*40) | features(n*4)`。
person_structure：`persons(n*56) | faces | candidates | attributes`。

## 2. 属性模型与预处理

- 输入：人体框（归一化）直接拉伸到 192×256；不 letterbox、不改宽高比（模型训练即如此）。
- onnx tier：`Tensor({1,3,256,192}, FLOAT32)`，RGB 顺序，`(pix - mean[c]) / std[c]`，
  mean = 123.675/116.28/103.53，std = 58.395/57.12/57.375。
- rknn tier：`Tensor({1,256,192,3}, UINT8)` 原始 RGB（`convert.py` 已把 mean/std 烘焙进模型）。
- 输出 26 个 0..1 概率；阈值：全部 0.5，`Glasses`(1) = 0.3，`HoldObjectsInFront`(18) = 0.6。
  只输出过阈值项，按 score 降序；每人上限 8 条（`kMaxPersonAttributesPerPerson`，属性随人数放大 JSON，需给 64KB IPC 载荷留余量）。
- 标签顺序固定（`person_attribute_labels.txt`）：
  Hat, Glasses, ShortSleeve, LongSleeve, UpperStride, UpperLogo, UpperPlaid, UpperSplice,
  LowerStripe, LowerPattern, LongCoat, Trousers, Shorts, Skirt&Dress, Boots, HandBag,
  ShoulderBag, Backpack, HoldObjectsInFront, AgeLess18, Age18-60, AgeOver60, Female, Front, Side, Back。

## 3. 采样与缓存（每 worker）

```
struct CachedTrack { std::array<float,4> box; std::vector<PersonAttribute> attrs; int64_t updated_ms; };
std::unordered_map<uint32_t, std::vector<CachedTrack>> tracks_;  // 受 attr_mu_ 保护
```

每帧：
1. 若 `!enabled`、无属性 session、或模型未加载 → 全部 person 不带属性，直接返回。
2. 按 IoU 降序贪心关联当前人体框与缓存（IoU ≥ 0.30）；命中则更新 box。
3. 未命中的人体框作为新 track 插入（`attrs` 空、`updated_ms = 0`）。
4. 过期 = `now_ms - updated_ms >= refresh_ms`（新 track 立即算过期）；候选按「面积降序」排序。
5. 每帧最多处理 `max_persons_per_frame` 个过期 track：裁剪 → 推理 → 写缓存；
   单个推理失败只跳过该 track。
6. 清理超过 `2 * refresh_ms + 1000ms` 未再出现的 track；缓存上限 64。
7. 所有 person 都挂上其关联 track 的 attrs 副本（按 track 关联，不按 index）。

这样 1s 刷新 + 每帧最多 2 人的成本下，10 人场景 ≈ 10 次推理/秒（RK3568 FP16 ≈ 149ms/s NPU），
远低于逐帧逐人。

## 4. 管道接入点

`FaceRecognitionPlugin::detect_scene_structure`：
- 新成员：`std::unique_ptr<zhulong::Session> attr_model_;`（存 `ModelHandle` 或 session 工厂）
  与 `std::unordered_map<uint32_t, std::shared_ptr<zhulong::Session>> attr_sessions_;`
  `mutable std::mutex attr_mu_;`、`std::unordered_map<uint32_t, std::vector<CachedTrack>> attr_tracks_;`
  以及 `AttributeParams attr_defaults_`。
- `init`：按可选模型模式加载 `models/pplcnet_person_attribute`（缺失 → 加载失败 → 属性静默关闭）。
- `create_session`：为每个 worker 建属性 session，`lookup_session` 复用同一套 per-worker 表。
- `detect_scene_structure`：在拿到 `person_detections` 之后、构建 `scene.persons` 之前，
  解析参数 `AttributeParams params = resolve_attribute_params(task_context.params.json, attr_defaults_);`
  然后 `classify_persons(...)` 得到 `std::vector<std::vector<PersonAttribute>>` 与 detections 对齐，
  构建 `scene.persons[i].attributes`。
- `detect_persons`（旧 v1 结果）沿用 `scene.persons`，属性自动跟随。
- 不修改 `FaceExtractorContext`。

## 5. 配置

```json
"enable_person_attributes":   {"type":"boolean","default":true, ...},
"person_attribute_refresh_ms":{"type":"integer","minimum":200,"maximum":10000,"default":1000, ...},
"person_attribute_max_persons":{"type":"integer","minimum":1,"maximum":8,"default":2, ...}
```

两个 tier 的 `config.schema.json` 必须字节一致；`additionalProperties:false`。
参数解析容错：类型不符/越界 → 用默认值。

## 6. 文件清单

- `native/sdk/include/zhulong/plugin.h`：新结构、宏、person 注释。
- `native/sdk/include/zhulong/algo.hpp`：`PersonAttribute`、`PersonItem.attributes`、两个 adapter、断言。
- `native/sdk/src/internal/result_serializer.hpp`：persons[] 输出 attributes。
- `native/sdk/abi.json`：1.4.0。
- `native/tests/plugin_c_abi_test.c`：尺寸/偏移/Arena。
- `.trellis/spec/native/algo-plugin-contract.md`：契约段落。
- `packages/face_recognition/{onnx,rk3568}/src/attribute/person_attribute.{hpp,cpp}`（hpp 一致）。
- `packages/face_recognition/{onnx,rk3568}/src/pipeline/face_recognition.{hpp,cpp}`。
- `packages/face_recognition/{onnx,rk3568}/config.schema.json`、`CMakeLists.txt`。
- `native/scripts/check_package_drift.py`：`src/attribute/person_attribute.cpp` 放行。
- `packages/face_recognition/{onnx,rk3568}/tests/aligner_matcher_test.cpp`：新增单测。
- 模型：`packages/face_recognition/onnx/models/PPLCNet_x1_0_person_attribute.onnx`，
  `packages/face_recognition/rk3568/models/PPLCNet_x1_0_person_attribute.rknn`。
- `packages/face_recognition/README.md`、`internal/aitypes/model_test.go`（属性透传断言）。

## 7. 验证步骤

1. `make native-build`（SDK + native 全量，含 C ABI 静态断言）。
2. `make -C packages/face_recognition/onnx unit-test test`（x86，含新单测与 dev_infer）。
3. `make pkg-drift-check`。
4. `python3 native/scripts/build.py go test ./internal/aitypes/...`。
5. `make sdk-cross-install` + 交叉构建 rk3568 包，scp 到板子 `/opt/zhulong`，`--verify` 跑 sample.jpg，
   检查输出 JSON 的 `attributes` 与耗时。

## 6. 验收记录（2026-11-17）

- `.rknn` 元数据核对：`pplcnet_person_attribute.rknn` 输入 `x` 为 `float16 / NHWC`，与其余 int8 检测模型一致；
  模型 zoo 官方 C++ demo 对同一模型也是「UINT8、NHWC、尺寸 W*H*C、pass_through=0」，因此包内
  `Tensor({1,256,192,3}, UINT8)` 的打包方式与官方一致（SDK 静态模型走 `in.fmt = attr.fmt`，此处为 NHWC）。
- 构建/自测：onnx 档 `make all` + `make test`（读 `sample.jpg`，5 个人体，人均 5–6 个属性，cost_ms ≈ 201ms）；
  `make unit-test` 覆盖标签/阈值/截断/排序/IoU/轨迹缓存/参数解析；两个档位 `aligner_matcher_test.cpp` 与
  `dev_infer.cpp` 保持逐字节一致。
- 跨层：`native-test` 34/34（含 `ZhulongPluginCAbi` 的 56B/40B 断言、`ZhulongAlgoJsonRoundTrip` 的属性 JSON 断言）；
  `internal/algorithm` 协议测试校验 `ZHULONG_PERSON_ATTRIBUTE_NAME_MAX_LEN`；
  `internal/record` 用例断言人体属性同时落到 recognition 与 person capture 的 `attributes_json`。
- 交叉编译：`make sdk-cross-install` 产出 aarch64 前缀（sdk 1.4.0）后，rk3568 档 `make all` 全量通过。
- 板端运行验证**未完成**：`native/scripts/board_check.sh` 卡在 SSH（`/etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf`
  符号链接属主 `nobody:nogroup`，以及 `192.168.17.135:22` 当前不可达）。网络恢复后需补跑，重点看
  `make test` 输出里的人体属性与单帧耗时。
- 包版本：两个档位 `manifest.json` 由 1.0.0 → 1.1.0。
