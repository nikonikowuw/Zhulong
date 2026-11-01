# 调研：结果 ABI 布局与对象模型约束（2026-10-30）

> 本文是 `.trellis/spec/native/algo-plugin-contract.md`（57KB，注入会被截断）在本任务上的**摘要**，
> 每条都标注了可复核的 file:line。实现时以本文件为速查，争议以 spec 与源码为准。

## 1. 结果载荷的形状（决定了方案空间）

| 事实 | 锚点 |
| --- | --- |
| 每次 `process` 只返回**一个**结果：`result_type + count + data + internal_token` | `native/sdk/include/zhulong/plugin.h:237-244` |
| `result_type` 是运行时判别值：1 检测 / 2 分类 / 3 人体结构 / 4 机动车结构 / 99 自定义 | `plugin.h:76-81` |
| 3 → `zhulong_person_item[]`；item 内嵌 `faces` 指针（指向同一 arena） | `plugin.h:176-182` |
| 结构化结果整棵用单块 arena 打包，`result_release` 单次 `free` | spec §2 |

⇒ 「人、脸两路平级」只能是**新增一种载荷类型**（双数组），不能靠"返回两份结果"或"再加一个数组字段"。

## 2. 结构体布局（为什么不能加字段）

```
zhulong_person_item（40B，零 padding）
  0  int64_t  track_id            8
  8  float    body_box[4]        16
 24  float    body_score          4
 28  uint32_t face_count          4
 32  const zhulong_face_item*     8
```

| 事实 | 锚点 |
| --- | --- |
| 40 字节、8 字节整除，无预留字段 | `plugin.h:169-182` 注释 |
| 对照：`zhulong_face_item` **有** `uint32_t reserved_` 用作指针前填充 | `plugin.h:163` |
| `zhulong_frame_view` 曾用"原 4 字节填充缝隙"安置 `dma_buf_fd`（先例：填充位可复用，但 person_item 没有） | spec §2 |
| 包与宿主**独立升级**，`IInferenceBackend`/`IVisionBackend` 虚表禁止增删（同类 ABI 事故史） | spec §2「运行库 HAL 的 vtable 属于内部 ABI」 |

⇒ 加 `has_body` 布尔 → 48 字节 → 老包/新宿主互相解析错位。**否决**，写进 PRD D1。

## 3. 退化语义（本期临时方案，v2 后删除）

| 事实 | 锚点 |
| --- | --- |
| 现行契约：`track_id=0`、`body_box` 逐位等于 `faces[0].box`、`body_score` = 人脸质量分、禁止 1.0f | `plugin.h:166-175`、`algo.hpp:174-190` |
| 宿主判据：`PersonItem.IsFaceCloseup()` | `internal/aitypes/model.go` |
| 宿主落库：退化项记 `target_type="face"` | `internal/record/service.go`（capture 分支） |
| 区域过滤**无条件**取 `person.BodyBox` | `internal/region/filter.go:33` |

## 4. 宿主存储层已经平级（关键判断依据）

| 表 | 粒度 | 关键列 |
| --- | --- | --- |
| `recognitions` | 每张脸一条 | `person_id` / `matched` / `similarity` / `candidates_json` / `box_json` / `body_box_json` / `face_image_path` |
| `captures` | 每个目标一条 | `target_type` / `track_id` / `score` / `box_json` / `crop_path` |

| 事实 | 锚点 |
| --- | --- |
| `target_type` 是开放字符串列，前端按开放集合处理 | `internal/record/model.go:85`、`web/src/features/records/data/target-type.ts:6-16` |
| 前端已预留 `face: 'emerald'` 色相 | `web/src/features/records/data/target-type.ts:12-16` |
| 徽标当前渲染**原始字符串**（person/vehicle 也一样） | `web/src/features/records/components/views/captures-view.tsx:182` |
| `ListTargetTypes` 动态返回已出现的目标类型 | `internal/record/service.go:733-735` |

## 5. 行业对照与模式分析（为什么"平级"是成熟方案的标准）

成熟安防厂商、国标/国际标准与工业级推理流媒体框架的实现，可归纳为三种模式：

### 5.1 三大模式拆解

#### 模式 A：扁平对象列表 + 父引用索引（Flat Objects with Parent Linkage）
* **代表**：**NVIDIA DeepStream**（`NvDsObjectMeta.parent` 指针）、**公安部 GA/T 1400.3**（`PersonList` 与 `FaceList` 平级顶层数组 + `SubRelatedID` 外键关联）。
* **形态**：所有检测对象在帧级完全扁平排列，Face 自带独立 BoundingBox、质量分、特征值与底库 ID；Face 通过一个可选的 `parent`（DeepStream）或 `person_index` / `SubRelatedID`（GA/T 1400）指向 Person。
* **孤立人脸**：天然支持。`parent == NULL` / `person_index = -1` / `SubRelatedID` 为空。Face 作为一等公民在管线中正常流转，下游识别插件无阻碍处理，绝不捏造 Person。

#### 模式 B：统一实体 + 多部件可选描述（Entity-Aspect / Multi-Rect Descriptor）
* **代表**：**ONVIF Profile M**（Analytics Service Spec §5.3/§5.4）、**海康威视 (Hikvision ISAPI)**、**大华 (Dahua)**。
* **形态**：
  - ONVIF Profile M：顶层抽象是 `<tt:Object ObjectId="15">`，其外观描述 `<tt:Appearance>` 下，`<tt:HumanFace>` 与 `<tt:HumanBody>` 是**并列可选兄弟节点**。
  - 海康/大华：结构化 Target 包含 `targetID`，同时具备 `faceRect` 和 `humanRect` 两个矩形字段。
* **孤立人脸**：
  - ONVIF：`<tt:Appearance>` 下只出现 `<tt:HumanFace>`，`<tt:HumanBody>` 节点直接省略。
  - 海康/大华：`faceRect` 有有效归一化坐标，`humanRect` **显式为 null 或全 0**，`targetType` 标为 `"face"`。没有任何厂商用 faceRect 伪造 humanRect。

#### 模式 C：严格流水线级联（Cascade Sub-label Model）
* **代表**：Frigate、早期固定卡口人脸抓拍机固件。
* **形态**：Face 只是 Person 目标的二级属性（`sub_label`）。
* **孤立人脸**：**直接丢弃**（`label != person` 直接 return）。只适合固定卡口远景，近景特写/刷脸完全不可用。

### 5.2 厂商与模式横向对比矩阵

| 维度 | 模式 A：扁平列表 + 引用索引<br>*(DeepStream, GA/T 1400)* | 模式 B：统一实体 + 多部件描述<br>*(ONVIF Profile M, 海康, 大华)* | 模式 C：级联子属性<br>*(Frigate)* |
|---|---|---|---|
| **核心哲学** | 实体粒度最小化，彼此独立，关系通过 ID/Index 挂接 | 顶层是“人”，人和脸是同一个目标的两个部件 | 只有人体，人脸只是人体框的二级裁剪产物 |
| **C ABI 内存/传输效率** | **最高**（定长结构体，连续紧凑数组，无指针逃逸，无动态序列化） | 中等（大量 optional 字段导致结构体膨胀或变长序列化） | 高（但表达力严重残缺） |
| **孤立人脸支持** | **天然完整支持**（`person_index = -1`） | **天然支持**（`body_rect = null`） | **不支持**（无人体直接丢弃） |
| **一人多脸/多人争抢** | 支持（每个 Face 独立索引对应 Person） | 难处理（1 个 Object 强绑 1 脸 1 身） | 不支持 |
| **算法模块解耦性** | **优秀**（人脸检测与人体检测算子独立输出后几何挂载） | 良好 | 差（完全强耦合） |

### 5.3 Zhulong 架构选型结论：采用模式 A

Zhulong 是 C ABI 静态链接 + Go 宿主：
1. **模式 B 在 C ABI 下劣势明显**：C 结构体不支持优雅的 Optional。若强行让每个 Item 兼具 `body_box` 和 `face_box`，在纯人脸特写或纯背身人体下，始终有 50% 字段是 NaN/零填充的无用内存。
2. **模式 A 完美契合 Zhulong 跨层现状**：
   - **底层 C ABI**：`persons[]` 存真人体，`faces[]` 存真人脸，Face 内带 `int32_t person_index`（`-1` 为无关联）。定长、紧凑、单 arena 分配、无指针穿越、可做严密的越界断言（`0 <= index < person_count`）。
   - **顶层 Go 存储**：宿主数据库本就是模式 A —— `recognitions`（脸）与 `captures`（身体/目标）本来就是两张平级的表，天然对应。
   - **算子流水线**：插件内 SCRFD（人脸）与 YOLO/SCRFD-person（人体）天然是两路独立的候选框集，用空间几何（`common/face_association.hpp`）计算 `person_index`，开销最小、逻辑最纯粹。

## 6. 实现时要核对的既有约束

- 异常不得穿越 C ABI：每个导出函数 `try/catch(...)` → 状态码（spec §2「异常防爆门守则」）。
- 输出形状/dtype 不合法时必须返回空结果，不得合成目标（spec §2「真实检测的结构化语义」）。
- 测试归属：包内自测（无需模型）在 `packages/<id>/<target>/tests/` + `make unit-test`；
  金标回归（需权重）在 `native/tests/`（spec §5.5）。
- 两 target 漂移由 `make pkg-drift-check`（`native/scripts/check_package_drift.py`）拦截。

## 7. SCENE_STRUCTURE (v2) 内存布局算术速查

### 7.1 `zhulong_face_structure_item`（288 字节，8 字节整除自然对齐）

```
偏移  类型                                  大小   说明
---------------------------------------------------------------------------------
  0   float    box[4]                        16   [x1, y1, x2, y2] 归一化坐标 [0.0, 1.0]
 16   float    quality                        4   人脸质量分 (0.0 ~ 1.0)
 20   float    landmarks[5][2]               40   5 点关键点
 60   int32_t  matched                        4   1=命中底库，0=未命中
 64   float    similarity                     4   Top-1 匹配置信度
 68   int32_t  is_fused                       4   预留多帧融合标志
 72   int32_t  person_index                   4   关联同一载荷内 persons[i]，-1 表示孤立人脸
 76   char     person_id[64]                 64   Top-1 人员 ID（未命中为空串）
140   char     person_name[64]               64   Top-1 人员姓名（未命中为空串）
204   char     matched_face_id[64]           64   Top-1 底库照片 ID（未命中为空串）
268   uint32_t candidate_count                4   候选人数量
272   uint32_t reserved_                      4   显式对齐填充位（使随后的 64 位指针 8 字节自然对齐）
276   const zhulong_face_candidate*           8   指向同一 Arena 内候选人数组的只读借用指针
---------------------------------------------------------------------------------
总计：284 + 4(padding) = 288 字节（288 % 8 == 0）
```

### 7.2 `zhulong_scene_structure` 容器（32 字节，置于 Arena 首部）

```
偏移  类型                                  大小   说明
---------------------------------------------------------------------------------
  0   uint32_t person_count                   4   真人体数量
  4   uint32_t face_count                     4   检出人脸数量（含关联人脸与孤立特写人脸）
  8   const zhulong_person_item*              8   指向真人体数组指针（face_count 恒为 0，faces 恒为 NULL）
 16   const zhulong_face_structure_item*      8   指向独立人脸数组指针
 24   uint64_t reserved_                      8   尾随预留位（8 字节对齐保底）
---------------------------------------------------------------------------------
总计：32 字节（32 % 8 == 0）
```

### 7.3 Arena 打包与 `out.count` 契约

- **单块连续内存分配**：
  `total_bytes = sizeof(zhulong_scene_structure) + n_persons * sizeof(zhulong_person_item) + n_faces * sizeof(zhulong_face_structure_item) + n_candidates * sizeof(zhulong_face_candidate)`；
- **指针分配基准**：
  - `scene = (zhulong_scene_structure*)arena;`
  - `scene->persons = (zhulong_person_item*)(arena + sizeof(zhulong_scene_structure));`
  - `scene->faces = (zhulong_face_structure_item*)((uint8_t*)scene->persons + persons_bytes);`
  - `candidates = (zhulong_face_candidate*)((uint8_t*)scene->faces + faces_bytes);`
- **判空与吞帧防御**：
  `out.count` 必须恒等于 `person_count + face_count`。当且仅当 `n_persons == 0 && n_faces == 0` 时，
  `out.count = 0` 且 `out.data = nullptr`；只有这样才能确保近景特写人脸（`n_persons == 0, n_faces > 0`）
  不会被 `algo_main.cpp` 的 `if (!result || result->count == 0)` 误吞。

## 8. Runner JSON 契约与宿主单向归一化

### 8.1 Runner 格式化输出 (`algo_main.cpp`)

```json
{
  "stream_id": 1,
  "pts": 12345678,
  "capture_time_ms": 1730304000000,
  "cost_ms": 28,
  "task_type": "scene_structure",
  "detections": [],
  "classifications": [],
  "persons": [
    {
      "track_id": 0,
      "body_box": [0.1, 0.2, 0.5, 0.9],
      "body_score": 0.88
    }
  ],
  "faces": [
    {
      "box": [0.15, 0.22, 0.35, 0.42],
      "quality": 0.94,
      "landmarks": [[0.2, 0.3], [0.3, 0.3], [0.25, 0.35], [0.22, 0.38], [0.28, 0.38]],
      "matched": true,
      "similarity": 0.89,
      "person_index": 0,
      "person_id": "usr_abc",
      "person_name": "张三",
      "matched_face_id": "face_123",
      "candidates": []
    }
  ],
  "vehicles": []
}
```

### 8.2 宿主反序列化入口归一化 (`internal/aitypes/model.go`)

在 `ParseInferenceResult` 入口层屏蔽兼容期双格式：
- 若载荷 `task_type == "person_structure"`（type 3 旧包）：解析出 `Persons`，自动将所有 `p.Faces` 提取至顶层 `res.Faces`，并将 `face.PersonIndex` 置为对应 `person` 下标；
- 若载荷 `task_type == "scene_structure"`（type 5 新包）：直接反序列化为顶层 `res.Persons` 和 `res.Faces`；
- **下游收益**：`internal/region`、`internal/record`、`internal/camera` 全链路只认知一套平级的 `Persons` 与 `Faces` 模型，无需随处编写兼容分支。

## 9. 区域过滤与宿主跟踪的协同规范

1. **区域过滤索引重映射（Index Remapping）**：
   `region.Filter` 过滤 `Persons` 时维护旧到新下标映射 `oldIdx -> newIdx`（淘汰者为 -1）：
   - `face.PersonIndex >= 0`：若父人体淘汰，人脸连带丢弃；若父人体保留，`face.PersonIndex = newIdx`；
   - `face.PersonIndex == -1`：孤立人脸按人脸框底边中点（BottomCenter）独立做 ROI/MASK 判定。
2. **Host Target Tracker 分桶**：
   - 真人体：送入 `bucketKey{camera, algorithm, kind: "person"}`，分配人体 `track_id`；
   - 孤立人脸：送入 `bucketKey{camera, algorithm, kind: "face"}`，分配独立人脸 `track_id`，告别坐标哈希抖动；
   - 关联人脸：其识别记录 `Recognition.TrackID` 继承父人体 `track_id`。

