# native 核心代码注释补齐 — 执行计划 (Implementation Plan)

> 父任务**不承担实现工作**。本文件是三个子任务的共同执行框架：验收命令、审查门禁、回滚点。

---

## 1. 前置条件

| 项 | 要求 |
| --- | --- |
| 基线 | `make native-build` 与 `make native-test` 在**未修改**状态下全绿（当前 15/15） |
| 依赖 | `native/vendor/` 已就位（离线构建；`make native-deps` 是唯一联网步骤，本任务不需要） |
| 环境 | 已装 `g++`（用于注释剥离校验）；交叉工具链**不需要** |

⚠️ **工作区可能不干净**：仓库中存在其他进行中的任务（如 `10-14-inference-result-egress`、`10-14-person-primary-face`），它们会修改 `native/` 下的文件。因此：

1. **基线必须是「子任务开始时的文件状态」，不是 `HEAD`** —— 校验命令见 §3，已据此设计（先快照到 `/tmp`，再比对）。
2. **同一文件不得被两个任务同时编辑**。开始子任务前执行：

   ```bash
   git status --porcelain -- native/
   ```

   若输出中包含本子任务范围内的文件，**先与对应任务的负责人确认**，或等待其提交后再开始。
3. 若某文件已被其他任务修改且尚未提交，本子任务**跳过该文件**并在任务 `implement.md` 中登记，待其提交后补做。

**基线确认命令：**

```bash
make native-build && make native-test       # 期望：15/15 全绿
```

---

## 2. 每个子任务的统一执行循环

```txt
Step 1  生成范围清单与基线快照（§7.0），检查并发修改
   → 验证：清单不含 vendor/ 与 tests/；无未声明的并发修改

Step 2  逐文件补注释（一个文件一次编辑）
   → 验证：文件内注释剥离不变性（§3.2 单文件版检查）

Step 3  全部文件完成后，执行全量剥离不变性检查
   → 验证：全部输出 OK，无 DIFF、无 SKIP

Step 4  make native-build && make native-test
   → 验证：构建成功 + 15/15 全绿

Step 5  覆盖率人工核对（对照 AC-D1 与父任务 prd.md §4.3 风险点清单）
   → 验证：逐项打勾，缺项即回退补写

Step 6  提交（按分区拆 3 次提交，单次 diff ≤ 800 行）
   → 验证：git show --stat 中无非注释行变更
```

---

## 3. 注释剥离不变性检查

⚠️ **基线口径**：比对基准是**子任务开始时的文件快照**，不是 `HEAD`。原因见 §1 —— 仓库中可能存在其他任务对同一文件的未提交修改，用 `HEAD` 作基准会把别人的改动误判成自己的「代码骨架变更」。

### 3.1 开始子任务时先快照

```bash
mkdir -p /tmp/zhulong_docs_base
for f in $(cat /tmp/zhulong_docs_scope.txt); do
  mkdir -p "/tmp/zhulong_docs_base/$(dirname "$f")"
  cp "$f" "/tmp/zhulong_docs_base/$f"
done
```

`/tmp/zhulong_docs_scope.txt` 为本子任务的文件清单（由 §7 生成）。

### 3.2 校验命令

```bash
norm() {
  g++ -P -fpreprocessed -dD -E -x c++ "$1" 2>/dev/null \
    | sed '/^[[:space:]]*$/d' | sed 's/[[:space:]]\+/ /g; s/[[:space:]]*$//'
}

# 用法：check <file>
check() {
  base="/tmp/zhulong_docs_base/$1"
  if [ ! -f "$base" ]; then echo "SKIP $1 (无基线快照)"; return; fi
  if diff <(norm "$base") <(norm "$1") >/dev/null; then
    echo "OK   $1"
  else
    echo "DIFF $1"
    diff <(norm "$base") <(norm "$1") | head -30
  fi
}

# 单文件
check native/sdk/include/zhulong/algo.hpp

# 全量（Step 3）
while read -r f; do check "$f"; done < /tmp/zhulong_docs_scope.txt
```

⚠️ 出现 `DIFF` 时的处置顺序：
1. 先看 diff 内容，判断是**真的代码骨架变了**还是**归一化不足**（例如新增注释改变了某行的 token 分隔）；
2. 若是代码骨架变了 → 立即回退该文件（从 `/tmp/zhulong_docs_base/` 恢复），重新逐段编辑；
3. 若是归一化不足（极少见）→ 先确认原因，**不要**直接放宽检查命令。

### 3.3 与 HEAD 比对（可选，仅用于无并发修改的文件）

对**确认未被其他任务修改**的文件，可额外用 `git show HEAD:$f` 作交叉验证：

```bash
git show "HEAD:$f" > /tmp/zhulong_head.cpp
diff <(norm /tmp/zhulong_head.cpp) <(norm "$f")
```

⚠️ 若该文件已被其他任务修改，此比对**必然**产生 diff，属预期，不是错误。

---

## 4. 审查门禁 (Review Gates)

| 门禁 | 检查内容 | 阻塞条件 |
| --- | --- | --- |
| **G1 范围** | 修改的文件属于本子任务 §7 清单，且不含 `native/vendor/`、`native/tests/`、`native/CMakeLists.txt`、`native/profiles/` | 任一出现 → 阻塞 |
| **G2 不变性** | §3.2 全量检查全部 `OK`（无 `SKIP`、无 `DIFF`） | 任一 `DIFF` → 阻塞 |
| **G3 构建测试** | `make native-build` + `make native-test` 全绿 | 失败 → 阻塞 |
| **G4 覆盖率** | AC-D1 与 §4.3 风险点清单逐项打勾 | 缺项 → 阻塞 |
| **G5 术语一致** | 注释中未出现 design.md §4 术语表的禁用别名 | 出现 → 阻塞 |
| **G6 保真** | 既有注释未被删除；技术含义未被改变（确凿纠正需登记） | 未登记 → 阻塞 |
| **G7 diff 粒度** | 单次提交 diff ≤ 800 行新增 | 超标 → 拆分 |

---

## 5. 回滚点

| 层级 | 回滚方式 |
| --- | --- | --- |
| 单文件 | 从 `/tmp/zhulong_docs_base/` 恢复该文件（保留其他任务的并发修改） |
| 单次提交 | `git revert <sha>` |
| 整个子任务 | 不合并分支 / `git revert` 该子任务的全部提交 |
| 父任务 | 三个子任务全部 revert |

⚠️ **不要用 `git checkout -- <file>` 回滚**：仓库中存在其他任务的未提交修改，`checkout` 会把它们的改动一并丢弃。必须从 §3.1 的基线快照恢复。

⚠️ 本任务**无数据迁移、无 ABI 变更、无配置变更**，因此回滚是纯代码层面的，不涉及运行时状态清理。唯一的运行时残留是构建产物（`build/`），回滚后建议 `make native-build` 重建一次以消除陈旧目标文件。

---

## 6. 父任务收尾（三个子任务完成后）

```txt
1. 跨层术语一致性检查
   → 在三个子任务的 diff 中 grep design.md §4 的禁用别名
   → 验证：零命中

2. 全栈门禁
   → make check
   → 验证：全绿

3. 风险点清单终审
   → 对照 prd.md §4.3 的 R1~R20，逐个确认在注释中有对应落点
   → 验证：20/20 有落点

4. 归档父任务与子任务
```

---

## 7. 子任务文件清单

### 7.0 生成范围清单与基线快照（每个子任务开始时执行）

```bash
# 以 public-contract 子任务为例
cat > /tmp/zhulong_docs_scope.txt <<'EOF'
native/include/Zhulong/engine.h
native/include/Zhulong/frame.hpp
native/sdk/include/zhulong/plugin.h
native/sdk/include/zhulong/algo.hpp
EOF

# 快照基线（§3.1）
mkdir -p /tmp/zhulong_docs_base
while read -r f; do
  mkdir -p "/tmp/zhulong_docs_base/$(dirname "$f")"
  cp "$f" "/tmp/zhulong_docs_base/$f"
done < /tmp/zhulong_docs_scope.txt

# 检查并发修改
while read -r f; do
  git diff --quiet -- "$f" && echo "clean  $f" || echo "⚠️ MODIFIED $f"
done < /tmp/zhulong_docs_scope.txt
```

⚠️ 输出中带 `⚠️ MODIFIED` 的文件已被其他任务改动：**先与该任务负责人确认，或等其提交后再开始**（见 §1）。

### 7.1 `10-14-native-public-contract-docs`（4 文件 / 1255 行）

```txt
native/include/Zhulong/engine.h          376 行  61% → 目标 100% 声明覆盖
native/include/Zhulong/frame.hpp         146 行  15% → 目标 100% 声明覆盖
native/sdk/include/zhulong/plugin.h      177 行  10% → 目标 100% 声明覆盖
native/sdk/include/zhulong/algo.hpp      556 行   3% → 目标 100% 声明覆盖
```

⚠️ `engine.h` 已有高质量注释，**以补全为主**：重点补 `enum` 分组说明、`Zhulong_video_view` / `Zhulong_stream_status` 的完整字段语义、以及 `Zhulong_engine_start` 与 `Zhulong_engine_stop` 的幂等性边界。

⚠️ `plugin.h` 与 `algo.hpp` 是**对外 SDK**，注释口径需与 `.trellis/spec/native/algo-plugin-contract.md` 严格一致（该 spec 是插件开发者的单一来源）。

### 7.2 `10-14-native-pipeline-docs`（15 文件 / ~2000 行）

```txt
native/src/abi/engine.cpp                215 行  23%
native/src/pipeline/engine.hpp           111 行  32%
native/src/pipeline/engine.cpp           401 行   3%
native/src/pipeline/stream.hpp           190 行  23%
native/src/pipeline/stream.cpp           487 行   8%
native/src/pipeline/bounded_queue.hpp    170 行  17%
native/src/pipeline/frame_publisher.hpp   57 行  29%
native/src/pipeline/frame_publisher.cpp   85 行  10%
native/src/pipeline/subscription.hpp      51 行  39%
native/src/pipeline/subscription.cpp      50 行  22%
native/src/nodes/capture/rtsp_input.hpp  138 行  57%
native/src/nodes/capture/rtsp_input.cpp  384 行  20%
native/src/nodes/decode/decoder.hpp       61 行  44%
native/src/nodes/decode/ffmpeg_decoder.hpp 34 行  11%
native/src/nodes/decode/ffmpeg_decoder.cpp 158 行  6%
```

⚠️ 本子任务覆盖 R15~R20（锁外 join、有界重同步、PTS 有效性、异常防爆、`release_fn` 防二次释放）。这些约束的权威来源是 `.trellis/tasks/archive/2026-10/10-11-native-pipeline-stability/{prd,design}.md`，**必须**读后再写，不得凭实现反推意图。

### 7.3 `10-14-native-algorithm-docs`（28 文件 / ~2400 行）

⚠️ **本子任务必须在 `10-14-inference-result-egress` 提交之后执行。**

截至本任务规划时，下列 3 个文件正被 `10-14-inference-result-egress`（状态 `in_progress`）修改，且是**真实功能代码**而非注释：

| 文件 | 并发改动 |
| --- | --- |
| `native/src/algorithm/ipc/ipc_protocol.hpp` | 新增 `MSG_RESULT = 7` 消息类型 |
| `native/src/algorithm/ipc/frame_broker.hpp` | 新增 `ResultCallback` / `set_on_result` |
| `native/src/algorithm/runner/runner.cpp` | 新增 `serialize_inference_result`（约 +118 行） |

**处理约定**：

1. 若开始本子任务时上述文件**仍未提交** → 按 §1 的约定，**跳过这 3 个文件**，只做其余 25 个文件，并在任务 `implement.md` 中登记为待补项。
2. 若已提交 → 重新生成基线快照（§7.0），全部 28 个文件一并处理。
3. ⚠️ **绝不可**在这 3 个文件上叠加注释后再让 `10-14-inference-result-egress` 继续编辑 —— 会造成 diff 交错、`git add -p` 难以切分。

```txt
native/src/algorithm/ipc/shm_layout.hpp              28 行   0%
native/src/algorithm/ipc/ipc_protocol.hpp            96 行   5%
native/src/algorithm/ipc/shm_pool.hpp                69 行  20%
native/src/algorithm/ipc/shm_pool.cpp               163 行   1%
native/src/algorithm/ipc/unix_socket.hpp             83 行   0%
native/src/algorithm/ipc/unix_socket.cpp            440 行   3%
native/src/algorithm/ipc/frame_broker.hpp           101 行   5%
native/src/algorithm/ipc/frame_broker.cpp           395 行   4%
native/src/algorithm/runner/runner.cpp              494 行   4%
native/src/algorithm/runner/json.hpp                 37 行   0%
native/src/algorithm/runtime/tensor.hpp              84 行   0%
native/src/algorithm/runtime/inference_backend.hpp   39 行   7%
native/src/algorithm/runtime/vision_backend.hpp      37 行   8%
native/src/algorithm/runtime/runtime_factory.hpp     14 行  14%
native/src/algorithm/runtime/runtime_factory.cpp     32 行   0%
native/src/algorithm/runtime/cpu_inference_backend.hpp 30 行  3%
native/src/algorithm/runtime/cpu_inference_backend.cpp 57 行   5%
native/src/algorithm/runtime/cpu_vision_backend.hpp  18 行   0%
native/src/algorithm/runtime/cpu_vision_backend.cpp 160 行   0%
native/src/algorithm/runtime/letterbox_geometry.hpp  24 行  37%
native/src/algorithm/runtime/letterbox_geometry.cpp  33 行   9%
native/src/backends/rknn/rknn_types.hpp              59 行  13%
native/src/backends/rknn/rknn_error_map.hpp          43 行  18%
native/src/backends/rknn/rknn_inference_backend.hpp  22 行  18%
native/src/backends/rknn/rknn_inference_backend.cpp 262 行   8%
native/src/backends/rga/rga_error_map.hpp            48 行  35%
native/src/backends/rga/rga_vision_backend.hpp      115 行  35%
native/src/backends/rga/rga_vision_backend.cpp      525 行   7%
```

⚠️ 本子任务覆盖 R1、R2、R6~R14 —— **风险点密度最高**。`.trellis/spec/native/algo-plugin-contract.md` §6（IPC/SHM）、§8（RKNN）、§9（RGA）是权威来源，且其中的数字（88/216/136/40 字节、64KB、50ms、8~16 槽位、4MB、114 补边值、worst ≤ 128 容差）**必须与代码中的 `static_assert` 和常量逐一对齐**后才可写入注释。

⚠️ **不得声称零拷贝**：RKNN 与 RGA 后端当前都有拷贝，注释必须如实描述（spec §8.9 / §9.6 明确要求）。
