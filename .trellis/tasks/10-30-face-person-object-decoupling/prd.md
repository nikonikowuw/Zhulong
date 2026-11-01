# 人脸与人体对象独立化 (SCENE_STRUCTURE v2) — PRD

> **唯一目的**：让人脸与人体成为**独立对象**。人脸可以没有人体而独立存在，人体不再承载人脸。
> 其余条款（前端文案、记录层判据）都是同一目标下的收尾，不作为独立诉求。

## 1. 目标与背景 (Goal & Background)

### 1.1 目标

在结果模型里把两个对象解耦：

- **人体对象**只描述检出的人体（框、分数、跟踪 ID），**不再内嵌人脸**；
- **人脸对象**是一等公民：自带框、质量、关键点、底库匹配结果与候选人，可独立存在；
- 两者的关联变成**可选引用**（`person_index`），`-1` 表示这张脸没有对应的人体检出。

完成后，"无人体的人脸"不再需要伪造人体框，`body_score` 与 `face.quality` 各归其位，
记录/前端/区域过滤都不再有"伪装的人体"这种中间态。

### 1.2 为什么要做（问题）

人脸识别算法包（`packages/face_recognition/`）的结果 ABI 把人脸**强制嵌入**人体对象：

```c
/* native/sdk/include/zhulong/plugin.h:176 */
typedef struct zhulong_person_item {
    int64_t  track_id;
    float    body_box[4];
    float    body_score;
    uint32_t face_count;
    const zhulong_face_item* faces;   /* 人脸只能挂在这里 */
} zhulong_person_item;                /* 40 字节，紧密排列，无预留位 */
```

v1 里不存在「有脸、无人」的表达，近景特写只能把**人脸框别名成人体框**，产生三个后果：

1. 记录列表把一张脸显示成「人体置信度 84%」（`internal/record/service.go` 曾无条件写 `target_type="person"`）；
2. `body_score` 二义：SDK 文档原写「退化项填 1.0f」，包实际填人脸质量分，两种值都无法与真人体区分；
3. 下游无法判断人体框是检出的还是抄来的，任何按 `body_score` 做阈值/统计的功能都读到假数据。

### 1.3 行业成熟方案对照与模式选型

经过对主流安防标准、推理流媒体框架与大厂实现的调研（详见 `research/abi-and-object-model.md` §5）：

| 厂商 / 标准 | 人脸与人体组织形式 | 孤立人脸（无人体检出）处理 |
| --- | --- | --- |
| **NVIDIA DeepStream** | 帧级 `NvDsObjectMetaList` 单链表**扁平排列**；Face 通过 `parent` 指针指向 Person | `parent == NULL`，人脸作为一等对象正常流转 |
| **公安部 GA/T 1400.3** | `PersonList` 与 `FaceList` 为顶层**平级数组**；Face 通过 `SubRelatedID` 关联 PersonID | `SubRelatedID` 为空，人脸正常上报，绝不伪造 Person |
| **ONVIF Profile M** | 抽象 `<tt:Object>` 下，`<tt:HumanFace>` 与 `<tt:HumanBody>` 为**并列可选兄弟描述符** | `<tt:HumanBody>` 节点直接省略，仅保留 Face |
| **海康威视 (ISAPI) / 大华** | 结构化 Target 中同时包含 `faceRect` 与 `humanRect` 两个矩形字段 | `humanRect` 显式为 null 或全 0，`targetType="face"`，绝不拿 faceRect 填 humanRect |
| **Frigate** | Face 仅作为 Person 检测框的二级属性（`sub_label`） | **直接丢弃**（`label != person` 直接 return） |

**行业共识**：
1. **没有任何成熟厂商会拿人脸框伪造人体框**。缺失就是 null/empty/独立存在。
2. 工业界的三种模式中：
   - 模式 B（ONVIF / 海康多部件描述符）在 C 语言结构体下难以优雅表达 Optional，会导致结构体大幅膨胀且充斥 NaN/全零填充；
   - 模式 C（Frigate 级联）无法支持近景特写与刷脸；
   - **模式 A（DeepStream / GA/T 1400 扁平列表 + 引用索引）** 在 C ABI 下内存最紧凑（连续定长数组）、无指针逃逸风险、天然契合 Go 宿主已有存储结构，是 Zhulong 的选型基准。

### 1.4 已探明的约束（决定方案空间）

- **v1 无处加字段**：`zhulong_person_item` 为 8+16+4+4+8=40 字节紧密排列，零 padding；
  也没有 `zhulong_face_item` 那样的 `reserved_` 占位（`plugin.h:163`）。加布尔字段 → 48 字节 →
  老包/新宿主互相解析错位，破坏「包独立升级」，**否决**。
- **一次 process 只能返回一种载荷**：`zhulong_inference_result` 是 `result_type + count + data`
  （`plugin.h:237`），因此"人、脸两路平级"必须**新增一种载荷类型**（同一载荷内两个数组）。
  同时，`result->count` 必须定义为 `person_count + face_count`（场景内总目标数），防止 `algo_main.cpp`
  在「0 个人体 + 1 个人脸」近景特写下判定 `count == 0` 而将整帧丢弃。
- **IPC 序列化契约**：插件包由独立进程 Runner（`algo_main.cpp`）托管，通过标准输出流向 Go 宿主，
  宿主经 `aitypes.ParseInferenceResult` 反序列化为领域模型。因此载荷平级化不仅改动 C ABI，
  还必须同步更新 Runner 的 JSON 序列化 Schema 与宿主解析管道。
- **宿主存储层已经平级**：`recognitions` 每张脸一条、`captures` 每个目标一条（详见
  `research/abi-and-object-model.md` §4）；嵌套只存在于传输层，这降低了本次改动的风险面。
- **前端已预留 `face` 类型**：`target-type.ts` 的 `TARGET_TYPE_HUE` 已有 `face: 'emerald'`；
  `target_type` 是开放字符串（DB 列 + `ListTargetTypes`），新增取值不需要迁移。

---

## 2. 需求规范 (Detailed Requirements)

### 2.1 对象模型与 C ABI 规范（核心）

- **R1 新增载荷类型**：`ZHULONG_RESULT_TYPE_SCENE_STRUCTURE = 5`。v1 函数表布局不变
  （新增运行时判别值，不新增函数指针、不改既有结构体尺寸）。
- **R2 独立人脸结构体**：新增 `zhulong_face_structure_item`，尺寸固定为 **288 字节**，严格满足 8 字节整除自然对齐：
  ```c
  typedef struct zhulong_face_structure_item {
      float    box[4];                                   /* 16 B: [x1, y1, x2, y2] 严格归一化坐标 [0.0, 1.0] */
      float    quality;                                  /*  4 B: 人脸质量分 (0.0 ~ 1.0) */
      float    landmarks[ZHULONG_FACE_LANDMARK_COUNT][2];/* 40 B: 5 点关键点 (x, y)，归一化 [0.0, 1.0] */
      int32_t  matched;                                 /*  4 B: 1=命中底库，0=未命中 */
      float    similarity;                               /*  4 B: Top-1 匹配置信度 (0.0 ~ 1.0) */
      int32_t  is_fused;                                /*  4 B: 是否多帧融合结果 (预留) */
      int32_t  person_index;                             /*  4 B: 关联同载荷内 persons[i]；-1 = 无关联孤立脸 */
      char     person_id[ZHULONG_PERSON_ID_MAX_LEN];      /* 64 B: 底库人员 ID (未命中为空串) */
      char     person_name[ZHULONG_PERSON_NAME_MAX_LEN];  /* 64 B: 底库人员姓名 (未命中为空串) */
      char     matched_face_id[ZHULONG_FACE_ID_MAX_LEN];  /* 64 B: 底库人脸照片 ID (未命中为空串) */
      uint32_t candidate_count;                          /*  4 B: candidates 数组元素数 */
      uint32_t reserved_;                                /*  4 B: 64 位指针前置填充对齐 (8 字节对齐) */
      const zhulong_face_candidate* candidates;          /*  8 B: 指向候选人数组借用指针 (指向同一 Arena) */
  } zhulong_face_structure_item;                         /* 288 字节，满足 8 字节整除 */
  ```
  - `person_index`：关联到同一载荷内 `persons[i]`；`-1` = 无关联（近景特写 / 身体未检出）；
  - `person_id` / `person_name` / `matched_face_id`：**底库身份**，与 `person_index` 是两个不同概念，
    命名与文档必须区分（前者是"挂在哪个检出人体"，后者是"这是底库里的谁"）。
- **R3 双数组载荷与 Arena 内存模型**：
  新增 `zhulong_scene_structure` 容器，置于 Arena 首部（尺寸固定为 **32 字节**）：
  ```c
  typedef struct zhulong_scene_structure {
      uint32_t person_count;                             /* 4 B: 真人体数量 */
      uint32_t face_count;                               /* 4 B: 检出人脸数量 (含关联与孤立脸) */
      const zhulong_person_item* persons;                /* 8 B: 真人体数组指针 (face_count 恒填 0, faces=NULL) */
      const zhulong_face_structure_item* faces;          /* 8 B: 独立人脸数组指针 */
      uint64_t reserved_;                                /* 8 B: 尾部对齐预留位 */
  } zhulong_scene_structure;                             /* 32 字节，满足 8 字节整除 */
  ```
  - **内存布局**：单块连续 Arena 依次存放 `zhulong_scene_structure` + `persons[]` + `faces[]` + `candidates[]`；
    `out.internal_token` 记录 Arena 首地址，由 `result_release` 单次 `free`，生命周期契约不变。
  - **`out.count` 语义**：`out.count` 严格定义为 `person_count + face_count`（场景内总目标数）。
    当且仅当 `person_count == 0 && face_count == 0` 时，`out.count = 0`，`out.data = nullptr`；
    只要有人或脸，`out.count > 0` 且 `out.data` 指向 `zhulong_scene_structure*`。
  - `persons` 只装**真人体**，永不含占位框；人脸只在 `faces` 里出现一次（不在 `persons[i].faces` 重复挂载）。
- **R4 索引合法性断言**：`person_index` 必须满足 `-1 或 [0, person_count)`。适配层打包时断言，
  宿主解析时丢弃非法索引（fail closed，不得按越界索引取 person）。
- **R5 关联判据唯一实现**：沿用 `packages/face_recognition/*/src/common/face_association.hpp` 的纯函数，
  v2 不重写几何逻辑（`make unit-test` 已覆盖上半身/IoF/零尺寸/NaN）。

### 2.2 跨层 IPC 协议与宿主适配

- **R6 Runner IPC JSON 契约与宿主归一化 (Host Normalizer)**：
  - Runner（`algo_main.cpp`）针对 type 5 输出平级 JSON：
    ```json
    {
      "task_type": "scene_structure",
      "persons": [{"track_id": 0, "body_box": [x1, y1, x2, y2], "body_score": 0.88}],
      "faces": [{"box": [x1, y1, x2, y2], "quality": 0.95, "landmarks": [...], "person_index": 0, "matched": true, ...}]
    }
    ```
  - Go 宿主领域模型（`internal/aitypes`）扩充：
    `InferenceResult` 与 `RawInferencePayload` 新增平级字段 `Faces []FaceStructureItem`。
  - **宿主反序列化入口单向归一化**：
    在 `aitypes.ParseInferenceResult` 入口层做平级化归一：若收到旧包的 `TaskTypePersonStructure` (type 3)，
    自动将其嵌套的 `p.Faces` 提取到顶层 `res.Faces` 并回填 `PersonIndex = i`；
    下游所有领域模块（Region Filter、Record Service、Host Tracker、WebSocket 推流）**统一面向平级结构编码**，
    杜绝在各领域层散落 `if-type-3-else-type-5` 分支。兼容期结束后仅需移除入口转换逻辑。
- **R7 区域过滤联动与索引重映射安全 (Index Remapping Guard)**：
  - **框判据定稿（闭环 Q1）**：维持人体框判定策略（人站在 ROI 外脸探入不单独记警报，避免误报）；孤立人脸使用人脸框底边中点（BottomCenter）独立做 ROI/MASK 判定。
  - **索引重映射保护**：`internal/region/filter.go` 过滤 `res.Persons` 时，必须维护旧下标到新下标的映射表 `oldIdx -> newIdx`（被过滤者映射为 `-1`）：
    - 对于 `face.PersonIndex >= 0`：若其父人体保留，更新 `face.PersonIndex = newIdx`；若其父人体被过滤，人脸联动丢弃（遵循 DeepStream 锚定实体生命周期规范）；
    - 对于 `face.PersonIndex == -1`（孤立人脸）：独立按人脸框底边中点执行 ROI/MASK 过滤。
    - 绝不允许未重映射的原始 `person_index` 流入下游导致越界 panic 或跨目标误挂。
- **R8 记录映射与 Host Target Tracking 分桶**：
  - **宿主跟踪器分桶**：
    - 真人体（`res.Persons`）送入 `bucketKey{kind: "person"}`，分配宿主人体 `track_id`；
    - 孤立人脸（`face.PersonIndex == -1`）送入独立跟踪桶 `bucketKey{kind: "face"}`，
      分配专属宿主人脸 `track_id`，告别脆弱的坐标哈希冷却，近景抓拍平滑去重；
    - 关联人脸（`face.PersonIndex >= 0`）直接继承其父人体的 `track_id`。
  - **落库写入**：
    - 人脸对象落 `recognitions`（每脸一条，含 face 裁剪 + 全幅图，`TrackID` 继承人体或使用人脸 track_id）；
    - 人体对象落 `captures`（`target_type="person"`、`Score` = `body_score`）；
    - 孤立人脸落 `captures`（`target_type="face"`、`Score` = 人脸质量分、`box` = 人脸框、裁剪图为人脸）。
    - 历史方法 `aitypes.PersonItem.IsFaceCloseup()` 仅在入口归一化兼容期保留，兼容期结束后删除。

### 2.3 算法包侧

- **R9 双 target 同步迁移**：
  `packages/face_recognition/{onnx,rk3568}` 改为输出 type 5：
  - 引入 `SceneStructureResultAdapter`，绑定宏 `ZHULONG_ALGO_MAIN`；
  - `detect_persons` → 平级构造（真人体进 `persons`，全部人脸进 `faces` 并填 `person_index`）；
  - 彻底删除 `make_degenerate_person_item` 占位生成逻辑；
  - 两个 target 必须同步（`make pkg-drift-check`）。

### 2.4 同目标下的收尾

- **R10 目标类型本地化与前端全覆盖**：
  前端记录模块对 `target_type` 进行全局本地化显示（「人脸 / 人体 / 车辆」），中性兜底长尾未知类型：
  1. `web/src/features/records/components/views/captures-view.tsx`（表格列 Badge）
  2. `web/src/features/records/components/captures/capture-card.tsx`（卡片视图 Badge）
  3. `web/src/features/records/components/detail/record-detail-dialog.tsx`（详情抽屉标题与徽标）
  4. `web/src/features/records/components/records-toolbar.tsx`（筛选下拉项 Option 文案）
- **R11 文档收敛**：`plugin.h` / `algo.hpp` / `.trellis/spec/native/algo-plugin-contract.md` 的
  「退化语义」段落改为"v1 历史兼容说明"，并指向 type 5 的新语义。

---

## 3. 验收标准 (Acceptance Criteria)

1. **孤立人脸独立存在与空人体特写防丢**：只含近景特写的输入 → `person_count == 0` 且 `face_count >= 1`，
   `zhulong_inference_result.count == face_count > 0`，Runner 正常产出平级 JSON，绝不被判空逻辑误吞；
   载荷中不存在任何占位人体项。
2. **关联正确且与 v1 一致**：全身 + 正脸场景下 `faces[i].person_index` 指向正确人体项；
   同一帧上 type 3 与 type 5 的挂载结果一致（同一判据）。
3. **身份与关联不混淆**：同一脸项上 `person_index`（视觉关联）与 `person_id`（底库身份）分别取到期望值；
   无底库命中时 `person_id` 为空串而 `person_index` 仍可非 -1。
4. **索引安全与重映射保护**：
   - 构造 `person_index` 越界的畸形载荷时宿主丢弃该脸而非崩溃/越界；
   - 区域过滤触发人脸/人体切片重排时，保留人脸的 `person_index` 正确更新，父人体被过滤的人脸连带过滤，绝无越界 panic 或目标错挂。
5. **记录层与宿主追踪**：
   - 孤立人脸 → `captures.target_type="face"`、`Score` = 人脸质量分、裁剪图为人脸，在宿主 tracker 中分配独立 `track_id`，告别坐标哈希去重；
   - 真人体 → `captures.target_type="person"` 且 `track_id` 非 0；
   - 关联人脸产出 `recognitions`，其 `TrackID` 继承父人体 `track_id`。
6. **区域过滤**：配置 ROI 后孤立人脸按自身人脸框底边中点判定，关联人脸随父人体联动，单测全覆盖。
7. **兼容期归一化验证**：输入 type 3 旧格式载荷，经 `ParseInferenceResult` 入口归一化后，
   在下游所有领域服务中均能透明以平级结构处理并正确落库（`native/tests/face_recognition_plugin_test.cpp` 覆盖）。
8. **不回归**：`body_score` 不再出现在任何退化语义中；`make native-test`、
   两个 target 的 `make unit-test`、`make pkg-drift-check`、`go test -race ./cmd/... ./internal/...`、
   `make check` 全绿；前端四处目标类型本地化（表格、卡片、抽屉、下拉筛选）文案齐全（R10）。

---

## 4. 不在范围 (Out of Scope)

- **vehicle/plate 解耦**：车牌天然在车上，退化只来自"只识别车牌"的配置，收益极低；维持 v1 嵌套语义。
- **人脸空间轨迹检索**：`web/src/features/spatial/data/schema.ts` 保持 `['vehicle', 'person']` 二元枚举，
  本次不将 `face` 纳入跨摄像头轨迹搜索，仅作为抓拍捕获对象落库展示。
- **阈值调优与性能**：检测阈值、NMS/IoU、算子优化、每帧张量缓冲复用（Per.6「先测再改」）。
- **算法能力扩展**：人脸属性（年龄/性别/口罩）、多帧融合（`is_fused`）。
- **GB/GA 上报映射**：本任务只做内部模型；平级化恰好使其更接近 GA/T 1400，但协议映射另开任务。

---

## 5. 关键决策 (Key Decisions)

| ID | 决策 | 理由 |
| --- | --- | --- |
| D1 | 本期实施解耦（用户 2026-10-30 明确："PRD 的作用就是把人和脸独立出来"） | 记录层已平级，剩余工作集中在传输层，收益是删掉整套补丁 |
| D2 | **不在 v1 结构里加字段/位** | 40 字节零 padding；包独立升级是 ABI 立身之本 |
| D3 | 解耦走**新增 `result_type = 5`** | `process` 一次只能一种载荷；新增类型是加法，函数表不变 |
| D4 | 人脸在载荷里**只出现一次**（`faces[]`），`persons[i].faces` 不再使用 | 避免同一事实两处表达，杜绝两路不一致 |
| D5 | 索引而非指针表达关联 | arena 内指针跨对象脆弱；索引可校验、可断言、可 JSON 往返 |
| D6 | 关联判据沿用 `common/face_association.hpp` | 已有包内自测覆盖，避免 v2 重写几何 |
| D7 | 兼容期一个版本周期，宿主双类型 | 旧包（type 3）不可强升；超期后删分支 |
| D8 | **架构模式选型：对标 DeepStream / GA/T 1400（模式 A）** | 扁平数组 + 引用索引（`faces[].person_index`）。对比模式 B（ONVIF 实体+多部件），无需在 C 结构体内维护昂贵的 Optional/NaN 填充，内存紧密且天生匹配宿主平级落库表 |
| D9 | **`out.count` 必须严格等于 `person_count + face_count`** | 杜绝 Runner 在无身体特写帧（`person_count==0`）因 `count==0` 误判为空结果将帧吞没 |
| D10 | **宿主反序列化入口单向归一化 (Host Normalizer)** | 在 `ParseInferenceResult` 将旧包 type 3 自动平级化，使下游业务层仅维护一套平级数据逻辑，避免在 Record/Region/Socket 处散落双重分支 |
| D11 | **ROI 过滤实行联动丢弃与索引重映射 (闭环 Q1)** | 区域判定锚定人体；人体丢弃则人脸连带丢弃；人体保留则更新索引映射表；孤立人脸独立按脸框中点判定，杜绝下标错位与 panic |
| D12 | **宿主 Tracker 为孤立人脸开辟独立跟踪桶** | 孤立人脸走 `bucketKey{kind: "face"}` 获得真实 `track_id`，告别坐标哈希去重在移动中的重复抓拍 |

---

## 6. 当前状态 (Current State)

本期已完成的前置修复（工作区已落地，供衔接，不再重复实施）：

- **算法包评审修复**：对齐 scale 符号错（`alignment/aligner.cpp`，已用「已知变换逆算」单测钉住）、
  会话指针逃出锁（改 `shared_ptr<Session>`）、SCRFD 输出形状/dtype 校验、头文件与实现分离、
  face/person 解码去重、`Matcher` 部分排序、常量与死代码清理。
- **契约文档**：`plugin.h` / `algo.hpp` / spec §2 已改为"退化项 = 人脸质量分 + 可识别"，并注明 v2 方向。
- **测试**：`native/tests/face_recognition_onnx_test.cpp` 退化项断言；两个 target 的包内自测
  （对齐/比对/挂载几何）；`make pkg-drift-check` 进 `make check`。
- **宿主过渡**：`aitypes.PersonItem.IsFaceCloseup()` + `record` 记 `target_type="face"` +
  `internal/record/capture_face_closeup_test.go`（兼容期保留，R8 明确了删除时机）。

---

## 7. 风险 (Risks)

1. **兼容期双路径**：宿主同时维护 type 3/5 两套解析，容易只在一条上修 bug。缓解：采用 D10 入口归一化，下游业务层仅一套平级逻辑，兼容期结束时仅移除入口归一化适配。
2. **`person_index` 越界与切片重排破坏**：跨 arena 索引与切片过滤会导致外键失效。缓解：适配层打包断言 + 宿主 fail closed（R4）+ 区域过滤严格执行索引重映射（R7）+ 畸形载荷测试。
3. **空人体帧被 Runner 丢弃**：近景特写下若 `count=person_count=0` 会被 `algo_main.cpp` 吞没。缓解：D9 强制 `count = person_count + face_count`。
4. **两个 target 漂移**：三个 .cpp 属允许差异文件，漂移检查无法覆盖语义差异；实现时列入 `implement.md` 检查清单并逐项对比。
5. **包版本与宿主版本错配**：新包 + 旧宿主 → 旧宿主不认识 type 5，必须"如实丢弃并记日志"，不得当成 type 3 解析。缓解：宿主按 `result_type` 严格分派，未知类型走既有未知类型路径。

---

## 8. 开放问题 (Open Questions)

- **Q1（已定稿闭环，产品语义）**：区域过滤对**已关联人体**的脸用哪个框判 ROI —— 人体框还是人脸框？
  **决议**：维持人体框（先归属到人、再判区域）。人站在 ROI 外、脸探进 ROI 不单独记抓拍；
  孤立人脸按人脸框底边中点判定；父人体被丢弃时关联人脸连带丢弃，并由索引重映射保证下标合法性（详见 R7 & D11）。

---

## 9. 参考 (References)

- `research/abi-and-object-model.md` — 布局算术、载荷形状、退化语义、宿主存储现状、行业对照（带 file:line）。
- `.trellis/spec/native/algo-plugin-contract.md` §2（结构化语义与空间挂载）、§5.5（测试归属）。
- `native/sdk/include/zhulong/plugin.h`（结果类型与 item 布局）、`zhulong/algo.hpp`（C++ 门面语义）。
