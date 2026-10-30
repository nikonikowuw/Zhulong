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
- **宿主存储层已经平级**：`recognitions` 每张脸一条、`captures` 每个目标一条（详见
  `research/abi-and-object-model.md` §4）；嵌套只存在于传输层，这降低了本次改动的风险面。
- **前端已预留 `face` 类型**：`target-type.ts` 的 `TARGET_TYPE_HUE` 已有 `face: 'emerald'`；
  `target_type` 是开放字符串（DB 列 + `ListTargetTypes`），新增取值不需要迁移。

---

## 2. 需求规范 (Detailed Requirements)

### 2.1 对象模型（核心）

- **R1 新增载荷类型**：`ZHULONG_RESULT_TYPE_SCENE_STRUCTURE = 5`。v1 函数表布局不变
  （新增运行时判别值，不新增函数指针、不改既有结构体尺寸）。
- **R2 独立人脸对象**：新增 `zhulong_face_structure_item`，含 `box[4]`、`quality`、`landmarks[5][2]`、
  `matched`、`similarity`、`candidate_count` + `candidates`，以及：
  - `int32_t person_index`：关联到同一载荷内 `persons[i]`；`-1` = 无关联（近景特写 / 身体未检出）；
  - `person_id` / `person_name` / `matched_face_id`：**底库身份**，与 `person_index` 是两个不同概念，
    命名与文档必须区分（前者是"挂在哪个检出人体"，后者是"这是底库里的谁"）。
- **R3 双数组载荷**：`zhulong_scene_structure { persons; person_count; faces; face_count; }`。
  `persons` 只装**真人体**，永不含占位框；人脸只在 `faces` 里出现一次（不在 `persons[i].faces` 重复挂载）。
- **R4 索引合法性**：`person_index` 必须满足 `-1 或 [0, person_count)`。适配层打包时断言，
  宿主解析时丢弃非法索引（fail closed，不得按越界索引取 person）。
- **R5 关联判据唯一实现**：沿用 `packages/face_recognition/*/src/common/face_association.hpp` 的纯函数，
  v2 不重写几何逻辑（`make unit-test` 已覆盖上半身/IoF/零尺寸/NaN）。

### 2.2 宿主适配

- **R6 双类型兼容**：宿主同时接受 type 3（旧包：嵌套 + 别名）与 type 5（新包：平级），持续一个版本周期；
  两条路径共用同一套关联判据，兼容期结束时删除 type 3 分支及其测试。
- **R7 区域过滤框语义**：按目标种类取框——真人体用人体框、孤立人脸用人脸框。
  现状 `internal/region/filter.go:33` 无条件取 `person.BodyBox`，需分支并补单测（见 Q1）。
- **R8 记录映射**：人脸对象直接落 `recognitions`（每脸一条，含 face 裁剪 + 全幅图）；
  人体对象落 `captures`（`target_type="person"`）；孤立人脸落 `captures`（`target_type="face"`、
  `Score` = 人脸质量分、`box` = 人脸框）。`aitypes.PersonItem.IsFaceCloseup()` 在兼容期保留，
  type 5 路径不再使用，兼容期结束后删除。

### 2.3 算法包侧

- **R9** `packages/face_recognition/{onnx,rk3568}` 改为输出 type 5：
  `detect_persons` → 平级构造（真人体进 `persons`，全部人脸进 `faces` 并填 `person_index`）；
  不再生成占位框。两个 target 必须同步（`make pkg-drift-check`）。

### 2.4 同目标下的收尾

- **R10 目标类型本地化**：记录列表徽标显示中文「人脸 / 人体 / 车辆」。
  现状 `captures-view.tsx:182` 直接渲染原始字符串；三种类型同时映射，未声明类型仍回落中性兜底。
- **R11 文档收敛**：`plugin.h` / `algo.hpp` / `.trellis/spec/native/algo-plugin-contract.md` 的
  「退化语义」段落改为"v1 历史兼容说明"，并指向 type 5 的新语义。

---

## 3. 验收标准 (Acceptance Criteria)

1. **孤立人脸独立存在**：只含近景特写的输入 → `person_count == 0` 且 `face_count >= 1`，
   载荷中不存在任何占位人体项。
2. **关联正确且与 v1 一致**：全身 + 正脸场景下 `faces[i].person_index` 指向正确人体项；
   同一帧上 type 3 与 type 5 的挂载结果一致（同一判据）。
3. **身份与关联不混淆**：同一脸项上 `person_index`（视觉关联）与 `person_id`（底库身份）分别取到期望值；
   无底库命中时 `person_id` 为空串而 `person_index` 仍可非 -1。
4. **索引安全**：构造 `person_index` 越界的畸形载荷时宿主丢弃该脸而非崩溃/越界。
5. **记录层**：孤立人脸 → `captures.target_type="face"`、`Score` = 人脸质量分、裁剪图为人脸；
   真人体 → `captures.target_type="person"` 且 `track_id` 非 0；人脸同时产出 `recognitions` 一条。
6. **区域过滤**：配置 ROI 后两类目标各按 Q1 定稿的框判定，两类场景均有单测。
7. **兼容期**：只用 type 3 的旧包仍能跑通并正确落库（`native/tests/face_recognition_plugin_test.cpp` 覆盖）。
8. **不回归**：`body_score` 不再出现在任何退化语义中；`make native-test`、
   两个 target 的 `make unit-test`、`make pkg-drift-check`、`go test -race ./cmd/... ./internal/...`、
   `make check` 全绿；前端三语文案齐全（R10）。

---

## 4. 不在范围 (Out of Scope)

- **vehicle/plate 解耦**：车牌天然在车上，退化只来自"只识别车牌"的配置，收益极低；维持 v1 嵌套语义。
- **跟踪器接入**：`track_id` 目前恒为 0；本任务只要求 `person_index` 与未来 `track_id` 语义不冲突。
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

1. **兼容期双路径**：宿主同时维护 type 3/5 两套解析，容易只在一条上修 bug。缓解：共用关联判据与
   `IsFaceCloseup`，兼容期结束时整段删除并保留回归用例。
2. **`person_index` 越界**：跨 arena 索引。缓解：适配层断言 + 宿主 fail closed（R4）+ 畸形载荷测试。
3. **ROI 语义变化**：孤立脸改按人脸框判 ROI 可能与现网配置期望不一致（Q1）。
4. **两个 target 漂移**：三个 .cpp 属允许差异文件，漂移检查无法覆盖语义差异；实现时列入
   `implement.md` 检查清单并逐项对比。
5. **包版本与宿主版本错配**：新包 + 旧宿主 → 旧宿主不认识 type 5，必须"如实丢弃并记日志"，
   不得当成 type 3 解析。缓解：宿主按 `result_type` 严格分派，未知类型走既有未知类型路径。

---

## 8. 开放问题 (Open Questions)

- **Q1（阻塞，产品语义）**：区域过滤对**已关联人体**的脸用哪个框判 ROI —— 人体框（现状语义）还是人脸框？
  推荐：维持人体框（先归属到人、再判区域），只对孤立脸用人脸框；否则"人站在 ROI 外、脸探进 ROI"
  会多记一条。若选另一条，需同步调整 `internal/region` 的单测与现网配置说明。

---

## 9. 参考 (References)

- `research/abi-and-object-model.md` — 布局算术、载荷形状、退化语义、宿主存储现状、行业对照（带 file:line）。
- `.trellis/spec/native/algo-plugin-contract.md` §2（结构化语义与空间挂载）、§5.5（测试归属）。
- `native/sdk/include/zhulong/plugin.h`（结果类型与 item 布局）、`zhulong/algo.hpp`（C++ 门面语义）。
