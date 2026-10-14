# 结构化结果 C ABI 技术设计（G2 定案）

## 1. 背景与现状勘测

### 1.1 现状问题
在 `10-14-face-recognition-e2e` 中，人脸识别链路需要承载人体-人脸-TopK候选人的层级结构化结果。
但既有 Native SDK（`native/sdk/include/zhulong/`）存在以下边界缺陷：
1. **载荷结构缺失**：`plugin.h` 中的 `zhulong_bbox` 是扁平单层结构（`{x, y, width, height, confidence, class_id, label[64]}`），无法表达 1:N 嵌套的人脸与候选人列表。
2. **Adapter 缺失**：`algo.hpp` 仅有 `DetectionResultAdapter` 和 `ClassificationResultAdapter`。
3. **占位混用**：`internal/algorithm/verifier.go:249` 临时把 `TaskTypePersonStructure` 映射为 `result_type == 1`。

### 1.2 源码勘测发现（修正早期假设）
在对 `native/sdk/include/zhulong/plugin.h` 与 `native/src/algorithm/runner/runner.cpp` 进行源码探测后，确认了四个事实：
1. **`struct_size` 字段是死字段**：插件虽然写入了 `sizeof(zhulong_plugin_api_v1)`，但 Runner 只校验 `rc != ZHULONG_PLUGIN_OK || api_.api_version != 1`，从未读取校验 `struct_size`。原先设想的「依赖 `struct_size` 进行自动字段偏移协商」并不存在。
2. **容器 `zhulong_inference_result` 已具备多态能力**：
   ```c
   typedef struct zhulong_inference_result {
       uint64_t frame_id;
       uint64_t task_id;
       zhulong_result_type_t result_type;
       uint32_t count;
       void* data;              /* 指向不同类型 item 的数组 */
       void* internal_token;    /* 插件私有销毁凭据 */
   } zhulong_inference_result;
   ```
3. **调用点与 `result_type` 解耦**：Runner 调用 `api.process(task, &view, &result)` 是纯通用指针传递；代码库中没有任何穷举 `switch(result_type)` 的硬编码。
4. **枚举值 `3` 处于空闲状态**：0=UNKNOWN, 1=DETECTION, 2=CLASSIFICATION, 99=CUSTOM。

---

## 2. 方案抉择：为什么选择方案 D（扩充数据层，不动函数表）

在父任务最初的推演中列出了三种方案：
- 方案 A：在 `zhulong_plugin_api_v1` 尾部追加 `process_structured` 函数指针。
- 方案 B：新增 `zhulong_plugin_api_v2` 函数表，要求双版本分派。
- 方案 C：将推理结果改为 JSON 字符串。

**最终决议：采纳方案 D（数据层多态扩展，保持 v1 表完全稳定）。**

| 方案 | 函数表变动 | 运行时兼容性 | 内存开销 | 评估结论 |
| :--- | :--- | :--- | :--- | :--- |
| A. 原地追加函数指针 | 修改 v1 表 | 依赖非显式契约，老 Runner 尺寸不匹配 | 低 | 否决（破坏 ABI 稳定性） |
| B. 新建 v2 函数表 | 新增 v2 结构体及入口 | Runner 需维护两套分派与加载生命周期 | 低 | 否决（过度设计：现有 `process` 签名完全通用） |
| C. JSON 载荷 | 无 | 零版本依赖 | ⚠️ 极高序列化/反序列化与内存拷贝开销 | 否决（边缘端每秒数十帧，不合性能要求） |
| **D. 扩充数据层（推荐）** | **完全不变（仍为 v1）** | **完全向后兼容老插件，极小化 Runner 负担** | **最低（连续内存 POD 零碎片）** | **采纳** |

**采纳理由**：
`zhulong_plugin_api_v1.process` 的签名本就可以返回任意类型的 `zhulong_inference_result*`。既然容器天生多态，就应当在载荷类型（`result_type`）与数据布局（POD 结构体）上做扩展，而不是增设多余的函数指针或版本表。

---

## 3. C ABI 契约设计

### 3.1 枚举与常数定义（`plugin.h`）

```c
#define ZHULONG_PERSON_ID_MAX_LEN    64
#define ZHULONG_PERSON_NAME_MAX_LEN  64
#define ZHULONG_FACE_LANDMARK_COUNT  5

typedef enum zhulong_result_type {
    ZHULONG_RESULT_TYPE_UNKNOWN          = 0,
    ZHULONG_RESULT_TYPE_DETECTION        = 1,
    ZHULONG_RESULT_TYPE_CLASSIFICATION   = 2,
    ZHULONG_RESULT_TYPE_PERSON_STRUCTURE = 3, /* 结构化人体/人脸 */
    ZHULONG_RESULT_TYPE_CUSTOM           = 99
} zhulong_result_type_t;
```

### 3.2 结构体定义与内存布局

结构体遵循纯 C（C99）POD 原则，所有字段显式对齐，杜绝平台/编译器之间的填充隐患。

```c
/* Top-K 候选人 */
typedef struct zhulong_face_candidate {
    char  person_id[ZHULONG_PERSON_ID_MAX_LEN];     /* 底库人员唯一标识符 */
    char  person_name[ZHULONG_PERSON_NAME_MAX_LEN]; /* 人员姓名 */
    float similarity;                               /* 置信度分值 [0.0, 1.0] */
    uint32_t reserved_;                             /* 显式填充对齐 */
} zhulong_face_candidate;

/* 人脸项 */
typedef struct zhulong_face_item {
    float  box[4];                                  /* [x1, y1, x2, y2] 严格归一化 [0.0, 1.0] */
    float  quality;                                 /* 人脸质量分 [0.0, 1.0] */
    float  landmarks[ZHULONG_FACE_LANDMARK_COUNT][2];/* 5点关键点 (x, y)，归一化 */
    int32_t matched;                                /* 1=命中底库，0=未命中 */
    float  similarity;                              /* Top-1 匹配置信度 */
    int32_t is_fused;                               /* 是否多帧融合结果（预留） */
    char   person_id[ZHULONG_PERSON_ID_MAX_LEN];     /* Top-1 人员 ID */
    char   person_name[ZHULONG_PERSON_NAME_MAX_LEN]; /* Top-1 人员姓名 */
    uint32_t candidate_count;                       /* candidates 数组元素数 */
    uint32_t reserved_;                             /* 64位指针前置填充对齐 */
    const zhulong_face_candidate* candidates;       /* 指向候选人数组（只读，指向同一 Arena） */
} zhulong_face_item;

/* 结构化人体项（1 个人体挂载 0~N 张人脸） */
typedef struct zhulong_person_item {
    int64_t  track_id;                              /* 跟踪 ID（纯人脸场景填 0） */
    float    body_box[4];                           /* [x1, y1, x2, y2] 严格归一化 */
    float    body_score;                            /* 人体检出置信度 */
    uint32_t face_count;                            /* faces 数组元素数 */
    uint32_t reserved_;                             /* 64位指针前置填充对齐 */
    const zhulong_face_item* faces;                 /* 指向人脸数组（只读，指向同一 Arena） */
} zhulong_person_item;
```

### 3.3 关键语义约束与退化规则
1. **坐标系强制归一化**：
   - 所有 `box[4]` 严格为 `[x1, y1, x2, y2]`，满足 `0.0 <= x1 < x2 <= 1.0` 且 `0.0 <= y1 < y2 <= 1.0`。
   - `landmarks[5][2]` 坐标严格在 `[0.0, 1.0]` 区间。
2. **纯人脸垂直切片的退化规则**：
   - 当插件仅运行人脸检测与识别（无独立人体检测模型）时：
     - 每个检出的人脸对应生成一个 `zhulong_person_item`；
     - `track_id = 0`；
     - `body_box` 直接赋予对应的 `face_box`；
     - `body_score = 1.0f`；
     - `face_count = 1`，`faces` 指向该人脸。
   - 这样既满足了 ABI 的 1:N 表达能力，又不需要上层 UI/后处理针对有无身体单独分叉。
3. **静态断言保证**：
   - 在 SDK 中为上述结构体提供 `static_assert`，保证 64 位平台上的内存大小与对齐绝对稳定。

---

## 4. 内存所有权模型：单块连续 Arena 打包

### 4.1 核心痛点
既有 `algo.hpp` 中 `result_release` 的默认实现为：
```cpp
delete[] static_cast<item_type*>(result->internal_token);
delete result;
```
如果结构体嵌套了动态分配的 `faces` 和 `candidates` 指针，简单的单个 `delete[]` 将造成严重的子数组内存泄漏！若采用多层递归遍历释放，则跨 ABI 的调用开销大且容易出现悬挂指针。

### 4.2 连续 Arena（Contiguous Arena）解决方案
为保持「一次释放、零碎片、无深层析构」的优势，采用**单块连续内存打包（Single Contiguous Arena）**策略：

1. **单次申请**：
   插件计算出一帧所需的所有内存总量：
   $$\text{TotalBytes} = (N_{\text{person}} \times \text{sizeof}(P)) + (N_{\text{face}} \times \text{sizeof}(F)) + (N_{\text{cand}} \times \text{sizeof}(C))$$
   通过 `std::malloc(TotalBytes)` 一次性分配整块内存。
2. **内部切片与指针固定**：
   - 块起始存放 `zhulong_person_item` 数组；
   - 紧随其后存放所有 `zhulong_face_item`；
   - 尾部存放所有 `zhulong_face_candidate`；
   - 每个 `person.faces` 与 `face.candidates` 均存储指向该 Arena 内部的绝对地址偏移。
3. **单点释放（O(1) 无泄漏）**：
   - `result->data = arena_base;`
   - `result->internal_token = arena_base;`
   - `result->count = person_count;`
   - `result_release` 函数实现：
     ```cpp
     static void result_release(zhulong_inference_result* result) {
         if (result) {
             std::free(result->internal_token); // 一键彻底释放整个树！
             delete result;
         }
     }
     ```
4. **生命周期原则**：
   - **分配方**：插件在 `process` 期间分配。
   - **有效期**：从 `process` 返回直至调用方显式调用 `api.result_release(result)` 之间有效。
   - **调用方权限**：只读视图。严禁调用方修改或保留指向该 Arena 内部的裸指针。

---

## 5. SDK 适配层（`algo.hpp`）

在 `native/sdk/include/zhulong/algo.hpp` 中提供专门的结构化适配器基类与打包工具：

```cpp
struct PersonStructureResultAdapter {
    using item_type = zhulong_person_item;
    static constexpr zhulong_result_type_t result_type = ZHULONG_RESULT_TYPE_PERSON_STRUCTURE;

    // 提供通用单块 Arena 打包助手函数：
    // PackResult(const std::vector<PersonDto>& persons, zhulong_inference_result** out_result);
};

#define ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(PluginClass) \
    ZHULONG_REGISTER_PLUGIN_IMPL(PluginClass, ::zhulong::sdk::PersonStructureResultAdapter)
```

---

## 6. 验证与门禁更新

1. **Go 侧 Verifier 校验门禁（`internal/algorithm/verifier.go`）**：
   - 将 `TaskTypePersonStructure` 期望的 `ResultType` 从 `1` 正式改为 `3`。
   - 明确拒绝：非 3 的结构化结果即触发 `ErrResultTypeMismatch`。
2. **跨语言契约测试**：
   - 在 `internal/algorithm/ipc_contract_test.go` 中解析 `plugin.h`，断言 Go 侧 `ResultTypePersonStructure == 3` 与 Native 的 `ZHULONG_RESULT_TYPE_PERSON_STRUCTURE` 保持数值同步。
3. **Native 测试验证**：
   - 在 C ABI 测试（`tests/plugin_c_abi_test.c`）中构造并验证结构化数据在纯 C 边界下的解构。
   - 运行 AddressSanitizer（ASan）确保单块 Arena 释放无任何泄漏或野指针。
