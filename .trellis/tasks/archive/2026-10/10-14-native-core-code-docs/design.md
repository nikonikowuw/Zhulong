# native 核心代码注释补齐 — 技术设计 (Technical Design)

> 本任务**无行为变更**，因此本文不是功能设计，而是**注释工程的设计**：术语口径、风格基线、变更不变性验证方法与 diff 组织方式。

---

## 1. 设计目标与约束

| 维度 | 约束 |
| --- | --- |
| 语义 | 零行为变更：编译产物、ABI 布局、运行期行为完全一致 |
| 可验证 | 「零代码变更」必须能被**机械验证**，不能只靠人工 review |
| 可审查 | 每个子任务的 diff 必须能在一次 review 内读完（约 ≤ 20 文件 / ≤ 800 行新增注释） |
| 一致性 | 同一概念在三个子任务中使用**完全相同**的中文术语 |
| 可回滚 | 纯注释变更，回滚 = `git revert`，无数据迁移、无兼容性影响 |

---

## 2. 核心机制：注释剥离不变性验证

### 2.1 问题

"纯注释变更" 最容易出现的意外是：编辑时误删一个分号、误改一个字符串字面量、误动一个 `static_assert` 数值、误调一个宏参数。这类错误在 review 中极易漏过，且会真实地改变行为。

### 2.2 方案：编译器预处理 + 注释剥离 + 逐字节比对

使用 GCC/Clang 的 `-fpreprocessed -dD -E` 组合：告诉编译器"输入已是预处理后的形式，不要再做预处理"，`-dD` 保留宏定义，`-E` 只输出。其效果是**剥离注释、不展开宏、不做条件编译剔除**，得到的文本是"注释无关的代码骨架"。

```bash
norm() {
  g++ -fpreprocessed -dD -E -x c++ "$1" 2>/dev/null \
    | sed '/^[[:space:]]*$/d' \
    | sed 's/[[:space:]]\+/ /g; s/[[:space:]]*$//'
}
```

- `sed '/^[[:space:]]*$/d'`：删除空行 —— 增删注释块会在原位置留下空行，必须归一化掉，否则每处注释都会产生伪 diff。
- `sed 's/[[:space:]]\+/ /g; s/[[:space:]]*$//'`：折叠连续空白并去行尾 —— 防止注释引起的**意外缩进变化**（这正是我们要抓的真问题之一，但行尾空白不算）。

### 2.3 已验证行为（本仓库实测）

| 检验项 | 结果 |
| --- | --- |
| 对未修改文件执行 `norm`，输出行数略少于原文件（`shm_layout.hpp`: 29 → 28） | ✅ 正常，注释与尾随空行被剥离 |
| `rtsp_input.cpp` 中的 `rtsp://` 字符串字面量在 `norm` 输出中完整保留（命中 2 处） | ✅ 字符串字面量未被误伤 |
| 对同一文件重复执行 `norm` 结果稳定（可用于 diff 比对） | ✅ |
| 宏定义是否被展开 | ❌ 不展开（`-fpreprocessed` 关闭宏展开），`#define` 保持原样 |

### 2.4 使用方式

⚠️ **基线是子任务开始时的文件快照，不是 `HEAD`**：仓库中存在其他进行中的任务（如 `10-14-inference-result-egress`）会修改 `native/` 下文件，用 `HEAD` 作基准会把别人的改动误判成自己的代码骨架变更。完整命令（含基线快照与并发修改检查）见 `implement.md` §3。

```bash
norm() {
  g++ -fpreprocessed -dD -E -x c++ "$1" 2>/dev/null \
    | sed '/^[[:space:]]*$/d' | sed 's/[[:space:]]\+/ /g; s/[[:space:]]*$//'
}

while read -r f; do
  diff <(norm "/tmp/zhulong_docs_base/$f") <(norm "$f") >/dev/null \
    && echo "OK   $f" \
    || { echo "DIFF $f"; diff <(norm "/tmp/zhulong_docs_base/$f") <(norm "$f") | head -20; }
done < /tmp/zhulong_docs_scope.txt
```

⚠️ **预期全部 `OK`**。任何 `DIFF` 都意味着**代码骨架被改动**，必须立即回退该处。

⚠️ **局限性（必须如实告知）**：
- 该检查只能证明"代码骨架未变"，**不能**证明"注释里的技术描述是准确的"。后者依赖人工 review 与 §4 的术语表。
- 若注释中包含了影响行号的 `#line` 或 `__LINE__` 依赖，本检查不会发现。本仓库未使用这类机制（已确认）。
- 该检查不覆盖 `CMakeLists.txt` / `.json` 等非 C++ 文件；本任务不修改这些文件。

### 2.5 与既有质量门禁的关系

| 门禁 | 命令 | 本任务的要求 |
| --- | --- | --- |
| 原生构建 | `make native-build` | 必须成功 |
| 原生测试 | `make native-test` | 必须保持既有全绿（15/15） |
| 交叉编译 | `make native-cross-build` | 不要求（无行为变更；如需可在子任务收尾时抽查一次） |
| 全栈门禁 | `make check` | 由父任务在最终集成审查时执行一次 |

⚠️ 依据仓库铁律：涉及 `internal/engine` 的构建/测试**必须**走 `python3 native/scripts/build.py` wrapper，禁止裸 `go build` / `go test`。`make` 与 Air 已走 wrapper。

---

## 3. 风格基线（从既有代码反推，非新发明）

### 3.1 文件头块注释

对**头文件**（`.h` / `.hpp`）与有实质内容的实现文件，使用 `@file` 块，说明职责与关键设计原则：

```cpp
/**
 * @file frame.hpp
 * @brief 专为异构多算法推理管线设计的统一硬件帧对象
 *
 * 核心设计原则：
 * 1. 极简高效（MVD）：...
 * 2. 1:N 零拷贝共享：...
 * 3. 防死锁生命周期闭环：...
 */
```

来源：`native/include/Zhulong/frame.hpp:1-8`、`native/include/Zhulong/engine.h:1-18`。

### 3.2 类型与函数

```cpp
/**
 * @brief 写入帧数据到共享内存槽位并向所有已连接的 Runner 广播分发
 * @return true 若帧成功写入并至少分发或进入槽位；false 若全槽位枯竭且无法驱逐导致丢帧
 */
bool publish_frame(const HardwareFrame& frame);
```

来源：`native/src/algorithm/ipc/frame_broker.hpp:59-63`。

带输出参数时使用 `@param[out]`：

```cpp
/**
 * @brief 创建并分配 Native 引擎实例
 *
 * @param[out] out_engine 输出参数，接收创建成功后的引擎句柄指针
 * @return Zhulong_status_t Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 参数空；Zhulong_ERR_OUT_OF_MEMORY 内存不足
 */
Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);
```

来源：`native/include/Zhulong/engine.h:178-184`。

### 3.3 结构体成员

短结构体成员用**行尾块注释**（`/**< ... */`），与 `engine.h` 既有风格一致：

```cpp
int32_t codec;               /**< 视频编码格式（Zhulong_CODEC_H264 或 Zhulong_CODEC_H265） */
const uint8_t *extradata;    /**< 视频头/参数集数据（SPS/PPS/VPS，原始 FFmpeg 表示，不保证 Annex B 格式） */
```

### 3.4 风险点标注

使用 ⚠️ + **加粗**，与 `.trellis/spec/native/*.md` 的既有语气一致：

```cpp
// ⚠️ 本函数假定输入已通过参数校验（正尺寸），不做错误返回。
// 与 CpuVisionBackend::letterbox 调用前的校验保持一致。
```

来源：`native/src/algorithm/runtime/letterbox_geometry.hpp:18-22`。

### 3.5 实现文件（`.cpp`）的块级注释

在**非平凡逻辑块**上方写"意图 + 为什么"，不写"做了什么"：

```cpp
// 丢弃至下一个关键帧：H.264/H.265 帧间依赖意味着丢失参考帧后的
// 中间帧解出来是花屏，重同步比逐帧硬解更划算。
```

不写：

```cpp
// 循环遍历 packets
for (auto& p : packets) {   // ← 逐行复述，禁止
```

---

## 4. 术语表（单一来源，三个子任务必须逐字使用）

> ⚠️ 这是本父任务的核心产出。三个子任务在注释中表达同一概念时**必须使用同一术语**，否则跨层阅读时会误以为是不同机制。

### 4.1 内存与所有权

| 英文 / 代码标识符 | 统一中文 | 禁用别名 | 说明 |
| --- | --- | --- | --- |
| borrowed pointer | **借用指针** | ~~借出指针~~、~~只读指针~~、~~临时指针~~ | 指向他人内存、不得释放、有明确有效期 |
| owned / owner | **持有者** / **所有权** | ~~拥有者~~ | 谁分配谁释放 |
| deep copy | **深拷贝** | ~~拷贝副本~~ | 需长期保留借用数据时必须执行 |
| view (struct) | **视图** | ~~视图结构体~~（首次出现可全称） | 借用语义的结构体，如 `Zhulong_packet_view` |
| Arena (packing) | **Arena（单块连续内存）** / **Arena 打包** | ~~内存池~~、~~竞技场~~ | 一次 `malloc`、嵌套子指针指向块内偏移 |
| internal_token | **内部销毁凭据** | ~~内部令牌~~、~~私有句柄~~ | 指向 Arena 首地址，交给 `result_release` |
| in-flight | **在途** | ~~飞行中~~、~~处理中~~ | 已分发但未归还的租约/帧 |
| lease | **租约** | ~~租赁~~（可作动词"租赁"） | 槽位借用凭证，需归还 |
| generation | **代次** | ~~代数~~、~~版本号~~ | 槽位复用时递增，防 ABA |
| slot | **槽位** | ~~插槽~~ | SHM 中的帧缓冲单元 |
| graceful shutdown / drain | **优雅停机** / **排空** | ~~清理~~、~~冲刷~~（flush 才是冲刷） | `drain` = 排空，`flush` = 冲刷（解码器语义） |

### 4.2 并发与生命周期

| 英文 / 代码标识符 | 统一中文 | 禁用别名 | 说明 |
| --- | --- | --- | --- |
| Model Group | **模型资源组** | ~~模型组~~ | 整包共享的不可变模型资源 |
| Worker Context | **Worker 执行上下文** | ~~工作线程上下文~~ | 线程绑定的独占执行会话 |
| Task State | **任务状态** | ~~任务上下文~~（`TaskContext` 是承载者） | 单路摄像机的时序状态 |
| session | **会话** | ~~session~~ | 统一译"会话" |
| backpressure | **背压** | ~~反压~~、~~压力回退~~ | 有界队列满载时的策略 |
| Drop-Oldest / Drop-Latest | **丢弃最旧** / **丢弃最新** | ~~丢老~~、~~丢新~~ | 保持英文原词亦可，但需与 spec 一致 |
| exception firewall | **异常防爆门** | ~~异常屏障~~、~~异常隔离层~~ | 导出函数全量 `try-catch (...)` |
| cancel | **取消** | ~~中止~~ | 队列的 `cancel()` 语义 |
| join | **汇合** | ~~等待~~、~~合并~~ | `std::thread::join`，spec 中译作"汇合" |
| reentrant | **可重入** | ~~可重入性~~ | 队列/会话可重复激活 |

### 4.3 算法与视觉

| 英文 / 代码标识符 | 统一中文 | 禁用别名 | 说明 |
| --- | --- | --- | --- |
| letterbox | **Letterbox（等比缩放 + 居中补边）** | ~~信箱化~~、~~黑边填充~~ | 保留英文，首次出现附中文解释 |
| normalized coordinates | **归一化坐标** | ~~标准化坐标~~ | `[x1, y1, x2, y2] ∈ [0.0, 1.0]` |
| landmark | **关键点** | ~~特征点~~ | 5 点人脸关键点 |
| candidate (Top-K) | **候选人** | ~~候选者~~ | 底库检索结果 |
| gallery | **底库** | ~~图库~~、~~画廊~~ | 人脸特征底库 |
| fused | **融合** | ~~合并~~ | 多帧融合结果 |
| quality | **质量分** | ~~质量~~ | 人脸质量评分 |
| similarity | **相似度** | ~~相似性~~ | 底库匹配置信度 |
| quantization | **量化** | — | PTQ/QAT 等 |
| operator | **算子** | ~~操作符~~ | NPU 算子（C++ operator 才叫"操作符"） |
| NMS | **NMS（非极大值抑制）** | ~~非最大值抑制~~ | 首次出现附中文 |

### 4.4 平台与后端

| 标识符 | 统一中文 | 说明 |
| --- | --- | --- |
| inference backend | **推理后端** | `IInferenceBackend` |
| vision backend | **视觉前处理后端** | `IVisionBackend` |
| runtime | **运行库** | `libzhulong_algo_runtime.so` |
| runner | **Runner（验包/执行器）** | 保留英文，首次出现附中文 |
| broker | **Broker（帧分发宿主）** | 保留英文 |
| plugin | **插件** | — |
| profile | **构建 profile** | 保留英文 |
| manifest | **manifest** | 保留英文（仓库既有用法） |

---

## 5. Diff 组织方式

1. **一个文件一个提交单元**（或按目录分组），便于 `git log --stat` 逐文件 review。
2. **不做批量格式化**：禁止在任何一次提交中混入与注释无关的空白/换行调整。
3. **既有注释的纠正单独提交**：若发现确凿的过期注释，单独一次提交，message 写明"文档纠正"并列出原文与现文，便于追溯。
4. **提交粒度**：每个子任务至少拆成 3 次提交（按分区），避免单次 diff 超过 800 行。

---

## 6. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| 编辑误伤代码骨架 | 行为变更，可能引入回归 | §2 的剥离不变性检查，逐文件执行；`make native-test` 兜底 |
| 注释与实现不符（"看起来对"） | 误导后续开发者，比没有注释更糟 | §4 术语表 + 依据 spec 与任务文档核实；不确定的写成"待验证"或不写 |
| 注释膨胀（逐行复述） | 噪音，违背 spec 铁律 7 | review 时逐块检查；对 getter/setter 豁免 |
| 三个子任务术语漂移 | 跨层阅读误解 | 父任务持有 §4 术语表；子任务 PRD 引用本文件 |
| 触碰 `vendor/` | 污染第三方代码，升级时冲突 | §2.2 明确排除；review 时检查 `git diff --name-only` 无 `vendor/` |
| 三个子任务并发构建 | 共享 build 目录竞争导致假失败 | 顺序执行；或在子任务 implement.md 中显式声明串行 |
